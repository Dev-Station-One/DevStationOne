# DevStationOne Runtime and Terminal Protocol

This document defines the protocol boundary between Control Server and Development Runtime.

The implementation must choose a versioned, authenticated, binary-safe protocol.

## Requirements

- outbound runtime connection
- mutual/runtime identity
- heartbeat
- reconnect
- capability advertisement
- request/response correlation IDs
- server-to-runtime commands
- runtime-to-server events
- terminal byte streaming
- terminal resize
- signals
- session lifecycle
- file operations
- service operations
- database operations

## Terminal stream

A terminal stream must preserve raw terminal bytes.

Recommended logical messages:
```text
session.open
session.input
session.output
session.resize
session.signal
session.exit
session.close
```

The protocol must be binary-safe and must not JSON-escape arbitrary terminal bytes.

## Session lifecycle

```text
requested
starting
running
detached
exited
closed
```

A detached browser does not imply an exited process.

## Runtime capabilities

Runtime registration advertises:
- terminal
- filesystem
- services
- database drivers
- systemd availability
- `os_username`, `home_dir` — the OS user the runtime process runs as and
  its home directory (a runtime always runs as one fixed user, ADR-014).
  Replaces the old per-Project `os_user`: the Control Server reads it live
  from the runtime instead of trusting a client-typed value.
- `shells` — login shells parsed from `/etc/shells` on the runtime host, so
  the Project setup UI can offer a real dropdown instead of a free-text
  path. Empty if unreadable; no fallback list.

## Versioning

Protocol messages must include a protocol version.

Backward compatibility rules must be documented once the first implementation exists.

## Implementation (Slice 2): control-plane envelope

The control-plane connection is a single WebSocket. Every message is a JSON
text frame with this envelope:

```json
{"v": "1", "type": "hello", "id": "", "payload": { ... }}
```

- `v` — protocol version, currently `"1"`.
- `type` — one of `hello`, `heartbeat`, `revoked` (Slice 2). Later slices add
  session/file/service/database message types.
- `id` — optional correlation ID, reserved for future server-to-runtime
  request/response exchanges. Unused by Slice 2 messages.
- `payload` — message-specific body.

Terminal byte streams (Slice 4) are a separate concern from this envelope:
they must remain binary-safe and are not JSON-wrapped.

### Connection lifecycle

1. Runtime dials `GET /api/v1/runtimes/connect` with
   `Authorization: Bearer <enrollment token>`.
2. Server authenticates the token (hash lookup), rejecting unknown tokens
   with `401` and revoked tokens with `403` before upgrading.
3. Runtime sends `hello` immediately after the WebSocket upgrade:
   ```json
   {"v": "1", "type": "hello", "payload": {
     "hostname": "dev01",
     "os": "linux",
     "arch": "amd64",
     "runtime_version": "0.1.0",
     "capabilities": {"terminal": false, "filesystem": false, "services": false}
   }}
   ```
   Capability fields are only `true` once the slice implementing that
   capability exists; Slice 2 advertises none.
4. Server marks the runtime `online`, records `enrolled_at` (first time
   only) and the hello fields, and registers the live connection.
5. Runtime sends `heartbeat` every 15s. The server updates `last_seen_at`
   and treats a 45s silence (3 missed heartbeats) as a dead connection.
6. On disconnect, the server marks the runtime `offline` (unless it has been
   revoked) and the runtime reconnects with exponential backoff (1s, capped
   at 30s), resetting the backoff after each successful handshake.
7. If an administrator revokes the runtime while connected, the server
   sends `revoked` and closes the connection. Any subsequent connection
   attempt with that token is rejected with `403` at step 2.

## Implementation (Slice 4): terminal sessions

Terminal sessions multiplex over the same control-plane connection used for
`hello`/`heartbeat`/`revoked` (no second connection to the runtime). Two
kinds of messages travel on it:

**Control messages** — JSON `Envelope`s on WebSocket text frames, same
shape as Slice 2:

```text
session.open    server -> runtime   {session_id, shell, dir, os_user, env, cols, rows}
session.ready    runtime -> server  {session_id}
session.error    runtime -> server  {session_id, error}
session.resize   server -> runtime  {session_id, cols, rows}
session.signal   server -> runtime  {session_id, signal}
session.close    server -> runtime  {session_id}
session.closed    runtime -> server {session_id}
session.exit      runtime -> server {session_id, exit_code}
```

`session.open` correlates its `session.ready`/`session.error` response by
`session_id` alone (the payload's own field) rather than the envelope's
`id` — one is already unique per session, so a second correlation
identifier would be redundant.

**Terminal bytes** — binary WebSocket frames, never JSON-wrapped:

```text
[36-byte session ID][raw payload bytes]
```

The session ID is a Postgres `uuid` in its canonical 36-character string
form, so the prefix is fixed-width — no length delimiter needed
(`protocol.EncodeFrame`/`DecodeFrame`). Direction is implicit: a frame the
server sends is input, a frame the runtime sends is output. This satisfies
the "binary-safe, must not JSON-escape" requirement above while reusing the
single connection from Slice 2.

The Control Server's WSS `/api/v1/sessions/{sessionID}/stream` (browser
side, see `docs/API.md`) is a separate, simpler connection: one session
per socket, so it carries binary terminal bytes with no framing at all —
the Control Server's `internal/sessions.Hub` is what translates between
that per-session socket and the runtime's multiplexed frames.

Runtime-side, a real OS PTY per session (`internal/pty`, wrapping
`github.com/creack/pty` — ADR-006) is started for `session.open` and its
output is streamed back via a `pump` goroutine per session until the
process exits. A `session.open` whose `os_user` does not match the
runtime's actual OS user is rejected with `session.error` (ADR-014).

## Implementation (Slice 5): service commands

Unlike terminal sessions, `systemd --user` operations are simple
request/response — list/status/start/stop/restart/enable/disable/logs each
produce one answer, with no ongoing byte stream. They reuse the
control-plane connection with a single message pair:

```text
service.command   server -> runtime   {action, unit, lines}
service.result     runtime -> server  {services} | {service} | {logs}
service.error       runtime -> server {error}
```

`action` is one of `list`, `status`, `start`, `stop`, `restart`, `enable`,
`disable`, `logs`; `unit` is required for every action except `list`;
`lines` is the tail length for `logs`.

This is the first use of `Envelope.ID` for its originally-reserved purpose
(see "Implementation: control-plane envelope" above): several service
commands can be in flight on one runtime connection at once, with no other
natural per-request identifier the way session messages have `session_id`,
so the server generates a random correlation ID per command and the
runtime's `service.result`/`service.error` reply carries the same ID back.
`runtimes.Conn.SendCommand` on the Control Server implements the
generic wait-for-matching-ID half of this exchange; `connection.Sender`'s
`SendReply(id, ...)` implements the runtime side. Both are intentionally
generic (not service-specific), for reuse by future request/response RPCs
(file operations, database queries) — see ADR-016.

Runtime-side, `internal/services` runs real `systemctl --user`/`journalctl
--user` commands (direct process execution, no shell — SECURITY.md) via
`os/exec`, never as root. `hello.Capabilities.Services` reflects whether a
`systemd --user` instance is actually reachable at connect time, not merely
whether this runtime build supports the capability.

## Implementation (Slice 6): file commands

Slice 6 adds three message types, reusing the Slice 5 correlated
request/response machinery unchanged:

- `file.command` (server→runtime): `{action, root, path, to, data, query,
  dirs_only}`. `action` is one of `list`, `stat`, `read`, `write`, `create`,
  `mkdir`, `rename`, `copy`, `move`, `delete`, `search`. `dirs_only`
  restricts a `search` action's matches to directories (used by the
  Project setup UI's tree search).
- `file.result` (runtime→server): exactly one of `{entries}`, `{info}`,
  `{data}`, `{matches}` populated, matching the action.
- `file.error` (runtime→server): `{error}` — the same payload shape as
  `service.error`; the correlation resolver (`wsConn.tryResolve`) treats
  both types as failures.

Containment: the Control Server fills `root` from the Project record (its
working directory) — `root` is never accepted from a client. `path`/`to`
are root-relative and validated with the same `filesystem.Clean` rule on
both sides (reject empty/absolute/`..`), then the runtime performs every
operation through an `os.Root` opened at `root`, which also refuses
symlinks escaping the root (SECURITY.md, ADR-018).

The Project setup UI's directory picker (ADR-020) reuses `file.command`
unchanged for the `list`/`mkdir`/`stat` actions, with one difference: `root`
is the runtime's reported home directory (`hello.Capabilities.HomeDir`)
instead of a Project's working directory, since there is no Project yet at
that point in the flow. No new message type exists for this — same
envelope, same runtime-side containment, different source for `root`.

Transfer encoding: `data` (write payloads and read results) is the file's
bytes base64-encoded inside the JSON envelope by `encoding/json`. A file is
capped at `protocol.MaxFileBytes` (24 MB); both ends of the control
connection enforce a read limit of `protocol.MaxMessageBytes` (48 MB),
which comfortably bounds the base64-encoded worst case (~33 MB).
If the 24 MB cap proves too small for real projects, the
upgrade path is chunked streaming messages, not raising the envelope
further.

## Implementation (Slice 7/8/9/10): database commands

Database connection test, schema discovery, data browsing, the SQL editor,
and user/role/permission management all reuse the Slice 5 correlated
request/response machinery unchanged — one message type in each direction,
`Envelope.ID` correlated:

```text
db.command   server -> runtime   {action, connection_id, connection, schema, table, limit, offset, sort, sort_desc, filter, edits, sql, principal, password, can_login, superuser, privilege}
db.result    runtime -> server   {ok} | {schemas} | {tables} | {columns} | {indexes} | {constraints} | {triggers} | {rows} | {principals} | {grants}
db.error     runtime -> server   {error}
```

`action` is one of `test`, `invalidate`, `schemas`, `tables`, `columns`,
`indexes`, `constraints`, `triggers`, `rows`, `commit`, `query`,
`principals`, `grants`, `create_principal`, `drop_principal`,
`set_principal_password`, `grant_privilege`, `revoke_privilege`.
`connection` is a `DBConnectionParams`: engine, host, port, username,
password, database, ssl_mode, instance_name, search_path, extra_params —
built server-side from a decrypted `database_connections` row
(`internal/dbconnections`), never taken from a client request.
`connection_id` identifies the stored connection for the runtime's pool
cache (below) — it is not itself a credential. `schema`/`table` scope every
per-table action, and also `grant_privilege`/`revoke_privilege`'s target
table. `limit`/`offset`/`sort`/`sort_desc`/`filter` scope `rows`; `edits`
(a `[]DBRowEdit{op, original, values}`) scopes `commit`; `sql` scopes
`query`. `principal` names the target user/login/role for `grants`/
`create_principal`/`drop_principal`/`set_principal_password`/
`grant_privilege`/`revoke_privilege`; `password` carries a new plaintext
password for `create_principal`/`set_principal_password` (never logged,
`docs/SECURITY.md`); `can_login`/`superuser` scope `create_principal`;
`privilege` scopes `grant_privilege`/`revoke_privilege`.

`invalidate` is the one exception to the request/response pattern: it's
sent fire-and-forget via `SendEnvelope` (no `Envelope.ID`, no reply) rather
than `SendCommand`, since there's nothing to reply with — see "Connection
pool cache" below.

Runtime-side, `internal/dbconnectors` opens a `database/sql` connection for
the requested engine (PostgreSQL via `pgx`/`stdlib`, MySQL/MariaDB via
`go-sql-driver/mysql`, MSSQL via `microsoft/go-mssqldb`, SQLite via
`modernc.org/sqlite` — all four pure Go, no cgo) and, for `schemas`/
`tables`/`columns`/`indexes`/`constraints`/`triggers`, introspects it via
each engine's own catalog (PostgreSQL/MySQL/MSSQL: `information_schema` for
tables/columns/constraints, engine-specific catalog views —
`pg_index`/`pg_attribute`, `information_schema.statistics`,
`sys.indexes`/`sys.index_columns` respectively — for indexes, since none of
the three expose index columns through `information_schema`; SQLite:
`sqlite_master`/`PRAGMA table_info`/`PRAGMA index_list`/`PRAGMA
index_info`/`PRAGMA foreign_key_list`, since it has no `information_schema`
at all). `hello.Capabilities.Databases` always advertises all four engine
names (`dbconnectors.SupportedEngines`), since the drivers are compiled in
rather than depending on anything else installed on the runtime host —
unlike `Services`, there's no live-availability check to make here.

For `rows`, each engine builds `SELECT * FROM <quoted schema.table>`, an
optional `WHERE <filter>` (raw, unquoted — the one deliberate exception
alongside `query`'s SQL text), an optional `ORDER BY <quoted sort column>`,
and `LIMIT n+1 OFFSET m` (MSSQL: `OFFSET m ROWS FETCH NEXT n+1 ROWS ONLY`,
which requires an `ORDER BY` — `ORDER BY (SELECT NULL)` when the caller
didn't sort) — requesting one extra row lets the runtime report `has_more`
without a separate `COUNT(*)`. `commit` builds parameterized
`INSERT`/`UPDATE`/`DELETE` statements per edit — column identifiers quoted,
every value bound (never interpolated, unlike `filter`), using each
engine's own placeholder syntax (`$1` Postgres, `?` MySQL/SQLite, `@p1`
MSSQL — the same `?`-doesn't-work-on-MSSQL discovery from schema
introspection applies here too). `query` runs the given SQL text verbatim;
a keyword sniff (`docs/API.md` "Data browsing and the SQL editor") routes
it to `Exec` or `Query`. All three share one generic row-scanning helper
(`dbconnectors.scanRows`) that normalizes driver values for JSON transport
— a `[]byte` that's valid UTF-8 becomes a string (needed for MySQL, whose
driver returns most column types as `[]byte` even when scanned into
`any`); genuinely binary data stays `[]byte`, which `encoding/json`
base64-encodes.

For `principals`/`grants`/`create_principal`/`drop_principal`/
`set_principal_password`/`grant_privilege`/`revoke_privilege`,
`internal/dbconnectors` dispatches to each engine's own
`Principals`/`Grants`/`CreatePrincipal`/`DropPrincipal`/
`SetPrincipalPassword`/`GrantPrivilege`/`RevokePrivilege` — PostgreSQL native
`ROLE`s, MySQL/MariaDB `'user'@'host'` accounts, MSSQL paired server
login + database user, and SQLite returning `dbconnectors.ErrUnsupported`
for all seven (`SupportsUserManagement() == false`) — see `docs/API.md`
"User/role/permission management" and `docs/DECISIONS.md` ADR-024 for the
per-engine mapping and why a `GRANT`/`REVOKE` privilege keyword is validated
against a fixed whitelist rather than bound as a parameter.

**Connection pool cache.** `internal/agent/db_manager.go` keeps at most one
`*sql.DB` per `connection_id` (`dbManager.pools`), opened lazily on first
use and reused by every subsequent `db.command` against that connection —
schema browsing fires several RPCs per tree-node expand, and reopening a
fresh connection (TLS handshake included, for PostgreSQL/MSSQL) on each one
would be needlessly slow. An entry idles out and closes after 5 minutes
unused; a `db.command{action:"invalidate", connection_id}` (sent by the
Control Server right after a PATCH/DELETE on that connection, best-effort —
see `docs/API.md` "Databases") closes and drops it immediately, so changed
credentials take effect on the next request rather than up to 5 minutes
later. See ADR-021/022 for why this is a plain map keyed by connection ID
rather than a session-open/close message pair.

## Security

Every runtime connection is authenticated.

Every server request to a runtime is authorized before dispatch.

Runtime must validate command/resource IDs against its own known state.
