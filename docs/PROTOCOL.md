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
session.open    server -> runtime   {session_id, shell, dir, os_user, env, command?, cols, rows}
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
  dirs_only, mode, owner, group, recursive}`. `action` is one of `list`,
  `stat`, `read`, `write`, `create`, `mkdir`, `rename`, `copy`, `move`,
  `delete`, `search`, `chmod`, `chown`. `dirs_only` restricts a `search`
  action's matches to directories (used by the Project setup UI's tree
  search). `chmod` takes `mode` (an octal permission string, e.g. `"755"` —
  only the low 9 bits are honored; setuid/setgid/sticky are dropped) and
  optional `recursive`. `chown` takes `owner`/`group` (account/group names,
  either omittable to leave that half unchanged) and optional `recursive`;
  see ADR-032 for why an owner change almost always fails on a non-root
  runtime.
- `file.result` (runtime→server): exactly one of `{entries}`, `{info}`,
  `{data}`, `{matches}` populated, matching the action (`chmod`/`chown`
  populate none — success is the empty `file.result` itself).
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

## Implementation (Slice 16): git commands

Slice 16 adds three message types, reusing the Slice 5 correlated
request/response machinery unchanged:

- `git.command` (server→runtime): `{action, root, path, staged, message, limit}`.
  `action` is one of `status`, `diff`, `stage`, `unstage`, `commit`, `log`.
  `log` runs `git log --max-count=<limit>` (limit defaults to 50, capped at
  100; Slice 18's journal commit picker) and replies `git.result` with
  `commits: [{sha, subject, author, datetime}]`, newest first. `root`
  is the Project's working directory, filled by the Control Server — never
  accepted from a client (same rule as `file.command`). `path` scopes
  `diff`/`stage`/`unstage`, root-relative, validated with the same
  `filesystem.Clean` rule as file paths on both sides. `staged` selects
  `git diff --cached` instead of the working-tree-vs-index diff. `message`
  is a commit message, sent runtime-side over `git commit -F -`'s stdin
  rather than as a process argument (never logged, `docs/SECURITY.md`).
- `git.result` (runtime→server): exactly one of `{files}`, `{diff}`,
  `{commit}` populated, matching the action (`stage`/`unstage` populate
  none of them — success is the empty result).
- `git.error` (runtime→server): `{error}` — the same payload shape as
  `service.error`.

Runtime-side, `internal/agent/git_manager.go` runs the real `git` CLI as a
direct child process (`os/exec`, argv only, no shell) with `cmd.Dir` set to
`root` — not a Go git library; see `docs/DECISIONS.md` ADR-028 for why.
`status` runs `git status --porcelain=v2 -z -- .` and parses the NUL-
delimited porcelain v2 records into `[]GitFileStatus{path, orig_path,
index, worktree}` (git-status(1) "Porcelain Format Version 2" — a
rename/copy record is followed by one extra NUL-terminated record carrying
the original path). `diff` runs `git diff` (or `git diff --cached` when
`staged`), optionally scoped to `path`, and returns its raw stdout
verbatim — no parsing, the browser renders the unified diff text as-is.
`stage`/`unstage` run `git add --` / `git reset --` (not `git restore
--staged`, which refuses to run in a repo with no commits yet — `git
reset --` handles that case via an implicit empty tree). `commit` runs
`git commit -F -` followed by `git rev-parse HEAD` to report the new
commit's SHA (`git commit`'s own stdout isn't machine-parseable).

Containment: `status` and `diff` (when no `path` is given) pass a `.`
pathspec (`-- .`) rather than running a bare `git status`/`git diff`, so a
Project whose working directory is a subdirectory of a larger git checkout
still only sees changes under its own root — without it, `git` would walk
up to the enclosing `.git` and report the whole outer repository. `stage`/
`unstage`/`commit` don't need the same treatment: every path they touch is
already root-relative and `filesystem.Clean`-validated, so they can't name
anything outside `root`.

## Implementation (Code tab): editor commands

The Code tab's editor stream (see `docs/API.md`) exchanges four new message
types over the same runtime control connection, reusing the Slice 5
correlated request/response machinery unchanged:

- `code.command` (server→runtime): `{action, root, path, paths, data,
  base_mtime, base_size}`. `action` is one of:
  - `open` — stat + read one file (`path`, root-relative, same
    `filesystem.Clean` containment rule as `file.command`). Refuses
    directories, files above `protocol.MaxEditorFileBytes` (2 MiB — the
    editor autosaves the whole buffer per debounce tick, so a 24 MiB cap
    would be unusable on the wire; the Files REST path keeps 24 MiB), and
    binary content (NUL byte in the first 8 KiB, flagged `binary: true`
    rather than failed). Replies `code.result` with
    `{data, mtime, size, binary}`.
  - `save` — write the client's whole buffer (`data`). `base_mtime`/
    `base_size` are the client's last-known on-disk fingerprint: when the
    current stat matches, the write succeeds and the reply is
    `{mtime, size}`; on a mismatch the runtime captures the pre-write disk
    content, writes anyway (last-write-wins), and replies
    `{stale: true, data: <pre-write content>, mtime, size}` so the editor
    can offer the overwritten version in its conflict prompt. A save of a
    vanished file recreates it.
  - `watch` — replace the root's watched path set (`paths` is always the
    Control Server's full per-root union of every subscribed editor's open
    files; one idempotent message per change). Paths whose stat the
    runtime already knows keep it, so a retained file is not re-pushed;
    newly watched paths are seeded silently on the first poll tick.
  - `unwatch` — remove `paths` from the root's set — an empty `paths`
    list clears the root's whole set (roots with nothing left are
    dropped).
- `code.result` / `code.error` (runtime→server): the same correlation shape
  as `file.result`/`file.error` (`code.error` reuses `ServiceErrorPayload`).

- `code.file.changed` (runtime→server push, no `Envelope.ID`):
  `{root, path, mtime, size}` or `{root, path, deleted: true}`. The
  runtime's edit manager (`internal/agent/edit_manager.go`) polls
  `stat` (mtime+size) on the watched set once per second — not inotify
  (ADR-033: the open set is tiny, so a 1s stat poll beats watch-management
  complexity and works on every filesystem). It records the post-save stat
  after each `save`, so the editor's own writes never re-push; a vanished
  watched file pushes `{deleted: true}` once and stays in the set (it may
  reappear). The Control Server fans these out to the project's editor
  stream subscribers as `content`/`deleted` messages.

**Browser-side stream** (`WSS /api/v1/projects/{projectID}/editor/stream`,
cookie-authenticated; shapes in `docs/API.md`): the browser sends
`{"type":"open"|"watch"|"save", ...}`; the server replies per-connection
(`content` for an open, `saved`/`stale` for a save, `error` for a
refusal/failure) and fans runtime pushes out to every subscribed
connection of the project as `content`/`deleted`/`error`. Tab-state
persistence goes over the REST pair (`editor/state`), not this stream.

Accepted semantics (ADR-033): concurrent editing of the same file is
last-write-wins — the side whose buffer loses gets the winner's content in
the `stale` reply, nothing else is preserved. There is no OT/CRDT, no
server-side revision numbers. A runtime restart drops the in-memory watch
set; the browser re-sends `watch` when it sees the runtime come back
online, so changes during the gap go undetected.

## Implementation (Docker tab): docker commands (ADR-040)

Project-scoped Docker management (ADR-040, `docs/REQUIREMENTS_DOCKER_TAB.md`):
the runtime talks to the local daemon via the moby client over the local
unix socket (standard paths only) and shells out to the `docker compose`
CLI plugin for compose actions. Visibility is strictly project-scoped:
containers carrying the derived compose project label
(`com.docker.compose.project`, default = working directory basename) or
bind-mounting the Project working directory. The runtime re-validates every
container ID against that scope independently of the Control Server.

- `docker.command` (server→runtime, correlated): `{action, root, container?,
  remove_volumes?, stream_id?, tail?}`. `root` is the Project's working
  directory, taken from the Project record — never from a client request.
  Actions: `list` (capability + compose-file detection + scoped container
  list in one `DockerStatusPayload`), `resources` (read-only
  images/volumes/networks referenced by scoped containers), `start`/`stop`/
  `restart`/`remove` (scoped container, returns the refreshed list),
  `compose_up`/`compose_down`/`compose_stop`/`compose_restart` (bounded CLI
  subprocess — up/down carry a 10-minute bound, output capped at 64 KiB tail),
  `logs_start`/`logs_stop` (`stream_id` keys the follow; `tail` replays past
  lines before following).
- `docker.result` / `docker.error` (runtime→server): the same correlation
  shape as `file.result`/`file.error` (`docker.error` reuses
  `ServiceErrorPayload`).
- `docker.logs` (runtime→server push, no `Envelope.ID`):
  `{stream_id, lines[], end?}` — one batch of followed log lines, batched
  at ~200 ms/100 lines. `end: true` on the final push before a stream ends
  (follow over, or cancelled by `logs_stop`).

`Capabilities.docker` (`{available, version, compose}`) reports real,
current daemon availability — nil when no socket is reachable, same
"report reality" rule as `services`.

**Browser-side log stream** (`WSS
/api/v1/projects/{projectID}/docker/containers/{containerID}/logs`,
cookie-authenticated): the server upgrades the request, starts
`logs_start` with a fresh stream ID, and relays pushed batches as JSON
frames (`{stream_id, lines, end}`) until the browser disconnects or the
follow ends; disconnect triggers `logs_stop` on the runtime. Slow browsers
drop batches rather than block the runtime read loop.

## Implementation (Backups/Services relocation): backup commands (ADR-041/042)

Backup files live only on the runtime host — a fixed root
(`~/.local/share/devstationone/backups/{db,projects}/`) — never on Control
Server disk. `backup.command` reuses the Slice 5 correlated request/response
machinery, with one addition: `start_db`/`start_project` reply immediately
with a runtime-assigned job ID and run the dump/archive in a background
goroutine, since a large dump can take minutes — far longer than the
15–30 s timeout every other command uses. Completion is learned by polling
`status`; there is no unsolicited "job finished" push (`backup.event` was
considered and dropped for the MVP — status polling is simpler and already
needed for the reconnect case where a push would be lost anyway).

```text
backup.command   server -> runtime   {action, kind_dir, params, backup_id,
                                       file_name, source_dir, restore_dir,
                                       target_database, job_id, offset, length}
backup.result     runtime -> server  {job_id, state, error, size, files, data}
backup.error       runtime -> server {error}
```

`action` is one of:

- `start_db` — dump a database connection (`params` + assigned `backup_id`).
  Replies with `{job_id}`; the runtime picks the output file name
  (`db/<backup_id>.dump`) and dumps with the engine's native tool on the
  runtime host: `pg_dump -F custom`, `mysqldump`/`mariadb-dump`, `sqlcmd
  BACKUP DATABASE`, or SQLite's `.backup` (never a plain file copy of a live
  database).
- `start_project` — archive a project's working directory (`source_dir` +
  assigned `backup_id`) as `projects/<backup_id>.tar.gz` plus a sidecar
  manifest `projects/<backup_id>.json` (format version, source dir, file
  count) — every file, `.gitignore` is not honored.
- `status` — a job's current `{state: running|done|failed, error, size}`
  by `job_id`.
- `list` — stored backup files under `kind_dir` (`db` or `projects`),
  returned as `{files: [{file_name, size, mod_time}]}`.
- `delete` — remove one stored file (and its manifest sidecar, for a
  project backup) by `file_name`.
- `read` — one download chunk (`file_name`, `offset`, `length` ≤ 4 MiB);
  the Control Server's download proxy walks a file's full length with
  sequential `read` calls and streams the bytes straight to the browser —
  the existing 24 MB whole-file-in-envelope transfer (`file.command`) is
  unusable for multi-gigabyte dumps.
- `restore_db` — restore a stored dump (`file_name`) into `target_database`
  on the connection described by `params`; the target may be the original
  database or a new name, existing data is replaced.
- `restore_project` — extract a stored archive (`file_name`) into
  `restore_dir`. The runtime validates the sidecar manifest's format
  version, then extracts with the same containment discipline as
  `internal/filesystem` (rejects `..`, absolute paths, and symlink targets
  escaping the destination) independently of anything the Control Server
  already checked. The Control Server is responsible for everything around
  the extraction itself — closing the project's open sessions and taking a
  `pre_restore` safety snapshot (a normal `start_project` job) before ever
  issuing an overwrite-mode `restore_project`.

`backup.result`/`backup.error` reuse the generic correlated-reply mechanism
(`runtimes.Conn.SendCommand` / `connection.Sender.SendReply`) unchanged from
Slice 5; `restore_db`/`restore_project` reply only once the operation itself
has finished (success or failure), since the Control Server's restore flow
needs the outcome before doing anything else (e.g. rolling back a
newly-created Project record on failure).

## Implementation (Backups/Services relocation): services move to the runtime (ADR-043)

Services were originally scoped to a Project (`service.command` resolved by
project → runtime). They are now scoped directly to a Runtime — the concept
they actually describe (a `systemd --user` instance on one host) never
depended on any one Project. The `service.command`/`service.result`/
`service.error` message shapes and the runtime-side `internal/services`
implementation are unchanged; only the Control Server's resolution changed,
from `runtimeConnForProject` (project → runtime) to the same
`resolveRuntime` helper the Runtimes area's directory browser already uses
(runtime ID directly, `400` unknown/revoked, `409` offline).

## Security

Every runtime connection is authenticated.

Every server request to a runtime is authorized before dispatch.

Runtime must validate command/resource IDs against its own known state.

## Implementation (Slice 29): Quick Commands (ADR-045)

A Project's stored CLI commands (`project_commands`, `internal/commands`)
run with one click. There is **no new execution path**: the run endpoint
opens a normal PTY session whose `session.open` payload carries an optional
`command` field — when set, the runtime starts the shell as
`shell -c <command>` (still a real PTY, `internal/pty.Options.Command`), so
the session exits with the command's own exit code and every pre-existing
lifecycle/exit-code rule applies unchanged. Empty `command` is byte-for-byte
the pre-ADR-045 interactive shell.

Run history (`command_runs`, `internal/commandruns`) records each run: who,
when, exit status, and the **full session PTY output, capped at 100 MiB**
captured at exit — the one deliberate exception to ADR-007's never-persist
rule, scoped to command-run sessions only. Capture needs no new protocol
message: `sessions.Hub.Forward` is already the single point every runtime
output byte passes through, so a capture buffer registered before
`session.open` rides it (`Hub.RegisterCapture`/`Hub.TakeCapture`), and the
runtime's `session.error`/`session.exit` handling finalizes the history row.
An explicit `session.close` (or a failed session.open) finalizes the row
directly; if the runtime dies mid-run with no further messages, the row
stays `running` until the session row is deleted (then SET NULL by the FK) —
a known, bounded gap.

The import scan (Makefile targets / `package.json` scripts / executables in
`scripts/`) reuses the existing `file.command` `read`/`list` RPCs — no new
protocol messages.
