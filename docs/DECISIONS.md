# Architecture Decisions

## ADR-001 — Go Control Server

Go is used for the Control Server because the product requires concurrent HTTP/WebSocket/runtime connections, PTY/process integration, portability, and a small self-contained deployment.

## ADR-002 — PostgreSQL without PostgREST

The application has a dedicated Go API, so PostgREST is not used.

PostgreSQL is the persistent datastore. Go owns business logic, authorization, API contracts, and runtime orchestration.

## ADR-003 — Separate Control Server and Development Runtime

The Internet-facing Control Server is separated from the machine where development code executes.

The Runtime makes an outbound secure connection, avoiding the need for inbound ports on development machines.

## ADR-004 — Browser as MVP client

The browser is the first client. All product functions are API-accessible so native clients can be built later without redesigning the backend.

## ADR-005 — Projects own execution context

Working directory, user, shell, and environment are properties of a Project. Sessions reference Projects and cannot override the authoritative working directory.

## ADR-006 — Real PTYs

Interactive sessions use operating-system PTYs rather than command/request simulation. This is required for Claude Code, editors, tmux, interactive shells, and arbitrary terminal applications.

## ADR-007 — WebSocket for terminal transport

Terminal byte streams are latency-sensitive and binary. WebSocket is used for live transport. PostgreSQL is not used for byte-by-byte terminal streaming.

## ADR-008 — systemd --user

User services are managed through the normal user's systemd instance where available. Root is not required for normal development-service operations.

## ADR-009 — Server-side browser sessions, not JWT

Browser authentication uses an opaque, high-entropy session token in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. The server stores only a SHA-256 hash of the token, keyed to a `sessions` row (user, expiry, last-seen, user agent, IP).

JWTs were rejected for this slice: DevStationOne is a single self-hosted Control Server, not a distributed set of services needing stateless verification, so JWT's main advantage does not apply here. A server-side session gives immediate, per-session revocation (required by PRD §4/§5 "account/session management" and by runtime/device revocation elsewhere in the product) without token-blocklist workarounds, and keeps the WebSocket terminal transport (ADR-007) authenticating the same way as the REST API.

## ADR-010 — Runtime identity reuses the bearer-token/hash pattern

Runtime enrollment (Slice 2) authenticates the same way as browser sessions
(ADR-009): a high-entropy random token is generated at registration, the
runtime presents it as `Authorization: Bearer <token>` on connect, and the
server stores/looks up only its SHA-256 hash.

mTLS or per-runtime client certificates were considered and rejected for
this slice: they add a certificate issuance/rotation/revocation subsystem
the product does not otherwise need, whereas the bearer-token pattern is
already implemented, tested, and immediately revocable (`UPDATE ... SET
status = 'revoked'` is checked on every connect attempt). If a future
threat model requires mutual TLS between Runtime and Control Server, it can
be layered underneath this application-level credential without changing
the protocol envelope.

## ADR-011 — `coder/websocket` for the runtime protocol transport

The Control Server <-> Development Runtime connection (ADR-003, ADR-007)
needs a WebSocket implementation on both ends. `coder/websocket` (the
maintained successor to `nhooyr.io/websocket`) was chosen over
`gorilla/websocket` because it is actively maintained, has a smaller
context-aware API, and its concurrency-safe `Read`/`Write` model fits the
heartbeat-plus-read-deadline pattern used for liveness detection. The same
library will carry Slice 4's binary terminal byte stream.

## ADR-012 — Single-process, in-memory runtime connection registry

The Control Server tracks live runtime connections in an in-memory map
(`internal/runtimes.Registry`), scoped to one server process. This is
sufficient for the current single-instance deployment model (ADR-001/PRD
§11: self-hosted, customer-operated). A future horizontally-scaled Control
Server would need a shared broker (e.g. Postgres `LISTEN/NOTIFY` or a
message bus) so any instance can reach a runtime connected to another
instance; that is out of scope until multi-instance deployment is a
requirement.

## ADR-013 — Project ownership: `created_by`, not a shared workspace

Projects are scoped by `created_by` (the DevStationOne account that created
them), separate from `os_user` (the OS account the runtime executes the
Project's sessions as, e.g. "brian" in PRD §3). A non-admin user's project
list/get/patch/delete only ever sees rows they created; an unauthorized
lookup returns 404, not 403, so a non-owner cannot use the response code to
confirm another user's project exists (SECURITY.md "never rely solely on
IDs supplied by the browser"). Administrators can see and manage every
project, mirroring the existing `is_admin` gate already used for runtime
management (Slice 2), which is useful for a self-hosted operator managing
projects on behalf of other accounts on the same installation.

Runtimes themselves remain unscoped by owner (Slice 2 already allows any
admin to see all runtimes); a Project may reference any existing,
non-revoked runtime regardless of who registered it.

## ADR-014 — Session `os_user` must match the runtime's actual OS user

A Project's `os_user` (Slice 3) names the OS account its sessions should
run as. The Development Runtime process itself always runs as one normal
user and never as root (PRD §8, SECURITY.md "never requires root for
ordinary operation") — it has no `setuid`/`sudo`/privilege-escalation path
to actually become a different OS user. Slice 4's `session.open` therefore
compares the requested `os_user` against the runtime's own
`os/user.Current()` and rejects the session with `session.error` on
mismatch (`internal/pty.Start`), rather than silently running as whichever
user the runtime process happens to be, or pretending to support
multi-user impersonation it cannot deliver. This is a real containment
check enforced at the runtime (SECURITY.md "Runtime must validate
command/resource IDs against its own known state"), not a stub: a Project
misconfigured for a different machine's user fails loudly at session
creation instead of silently running as the wrong (or coincidentally
matching) user.

A future multi-user runtime host would need a real privilege boundary
(e.g. a small root-owned setuid helper, or per-user runtime processes) to
lift this restriction; that is out of scope for the MVP's single-dev-user
deployment model.

## ADR-015 — Terminal sessions: table name, binary framing, and scrollback

Three related Slice 4 design choices:

**Table name.** The PRD and UI call PTY sessions "Sessions," but a
`sessions` table already exists for browser authentication (ADR-009,
`0002_users_sessions.up.sql`). The new table is named `terminal_sessions`
to avoid colliding with that unrelated, pre-existing concept; the Go
package is still `internal/sessions`, matching ARCHITECTURE.md's suggested
package layout — only the SQL table name differs from the product-level
term.

**Binary framing over the existing runtime connection.** Rather than open
a second connection per session between Control Server and Runtime, all
sessions multiplex over the one control-plane WebSocket from Slice 2
(ADR-011/ADR-012). Terminal bytes travel as binary WebSocket frames
prefixed with a fixed-36-byte session ID (`protocol.EncodeFrame`); this
keeps ADR-007's "no JSON-escaping" requirement without inventing a new
transport, and reuses `runtimes.Registry`'s existing per-runtime
connection lookup to route commands. The browser-facing
`/api/v1/sessions/{id}/stream` socket, by contrast, is already scoped to
one session per connection, so it carries raw bytes with no framing at
all — the frame prefix only exists where multiplexing is actually needed.

**Bounded server-side scrollback.** `internal/sessions.Hub` keeps the last
256KB of each session's output in memory (per Control Server process,
matching ADR-012's single-instance scope) and replays it when a browser
(re)attaches, so "browser refresh/reconnect preserves process" (PRD §4)
also means the reattaching terminal isn't blank. This is an in-memory
convenience, not a durability guarantee: output produced while the Control
Server is restarted, or while the runtime's own connection is down, is
lost — PTY bytes are still deliberately not persisted to PostgreSQL
(ADR-007).

## ADR-016 — Generic correlated request/response over the runtime connection

Slice 5 (services) needed a server-to-runtime request/response exchange
with no natural per-request key the way Slice 4's session messages have
`session_id` — several `service.command`s (e.g. two browser tabs both
listing services) can be in flight on one runtime connection at once. This
is exactly what `protocol.Envelope.ID` was reserved for since Slice 2
("optional correlation identifier for future request/response exchanges")
but had gone unused through Slice 4.

Rather than build service-specific correlation bookkeeping,
`runtimes.Conn` gained a generic `SendCommand(ctx, msgType, payload)
(json.RawMessage, error)`: it generates a random ID (reusing
`auth.NewSessionToken`, already exactly "a high-entropy random token" —
overkill length for a short-lived in-memory key, but a one-line reuse beats
a second RNG helper), registers a pending channel keyed by it, sends the
envelope, and blocks until `wsConn.tryResolve` — called from the runtime
connection's read loop for every inbound envelope carrying a non-empty
`ID` — delivers the matching reply or `ctx` is done. The runtime side is
symmetric: `connection.Sender.SendReply(id, msgType, payload)` sends a
reply carrying a given correlation ID, alongside the existing
`SendEnvelope` (id `""`) for fire-and-forget messages like heartbeats and
session lifecycle events, which don't need correlation and shouldn't be
forced to carry an unused ID field.

This machinery is deliberately not services-specific: `service.error` is
the only message type `tryResolve` currently special-cases as a failure
(vs. treating the payload as a success), but the pattern — one request
type, one result type, one error type, correlated by ID — is exactly what
file operations and database queries (Slices 6+) will need too. Building
it generically now, while there are two real call sites to keep it honest
(services today, sessions' existing non-correlated flow as the contrast
case), is judged cheaper than two more rounds of "session_id-shaped"
one-off correlation later.

## ADR-017 — Services: direct systemctl/journalctl exec, and a live-state union for List

`internal/services` shells out to `systemctl --user` and `journalctl
--user` via `os/exec` (argv arrays, no shell — SECURITY.md "never
construct shell commands by concatenating untrusted strings") rather than
talking to systemd's D-Bus API directly. systemd ships no official Go D-Bus
binding in this project's dependency set, and the CLI's plain-text/`show`
output is already a stable, documented interface; parsing it is a smaller,
more auditable surface than vendoring a D-Bus client for this MVP. Unit
names are validated (`services.ValidUnitName`, checked both server-side for
an early `400` and runtime-side before exec) against
`^[A-Za-z0-9:_.@-]+\.service$` as defense-in-depth — `os/exec` already
makes shell injection impossible by construction, so this instead rejects
nonsensical input with a clean error instead of a raw systemctl failure.

**`List` merges two systemctl sources, discovered necessary by testing
against a real systemd --user instance, not anticipated up front.**
`systemctl --user list-units --all` only shows units currently loaded into
the manager's live table — a unit systemd has garbage-collected after it
stopped (observed happening within about half a second on this project's
own dev machine, systemd 255) silently disappears from `--all` even though
`systemctl --user show`/`start` on it still work fine. For a service
manager whose core workflow is "stop a project's dev service, come back
later and start it again," a list that drops exactly the units a user just
stopped is a real defect, not an edge case. `List` therefore unions
`list-units --all` with `list-unit-files` (which lists every installed
unit file regardless of live-table membership) and fills in status for the
difference with one additional bulk `systemctl show unit1 unit2 ...` call
— not one `show` call per missing unit, which would mean dozens of
subprocess spawns on a typical desktop (119 installed unit files vs. 24
loaded, measured on this same dev machine). A bare template unit
(`app@.service`, no instance) is excluded from that union: `systemctl show`
and `start` both reject it outright ("is missing the instance name"), so
listing it would offer Start/Stop actions that error every time. If the
bulk `show` call itself fails for an unexpected unit name, `List` degrades
to the `list-units`-only result rather than failing the whole listing over
one bad entry — found necessary the same way: a real unit
(`gnome-session-manager@.service`) on this dev machine's own systemd
instance broke a naive "error on any exec failure" implementation.

## ADR-018 — Files: os.Root containment, root-relative paths, whole-file base64 transfers (Slice 6)

Date: 2026-09-13

**Decision.** The runtime's filesystem capability (Slice 6) is built on
Go's `os.Root` (`internal/filesystem`). Every file.command is executed
against a Root opened at the Project's working directory; paths travel as
root-relative strings, never absolute, and are validated by
`filesystem.Clean` on the Control Server (early 400) *and* re-checked by
the runtime before the Root call (SECURITY.md's independent containment).
`os.Root` supplies the actual guarantee: it resolves every path under the
root fd and refuses both `..` traversal and symlinks that escape the root —
including symlinks planted inside the root pointing outside it, which
plain `filepath`-based checks miss. Transfers (read results, write
payloads) carry the file's bytes base64-encoded inside the JSON envelope,
capped at `protocol.MaxFileBytes` (24 MB), with the control connection's
WebSocket read limit raised to `protocol.MaxMessageBytes` (48 MB) on both
ends to bound the encoded worst case.

**Why not alternatives.** Validating paths with `filepath.Clean` +
prefix checks is the classic approach but is a known source of
containment CVEs (symlink races, mixed separators); `os.Root` is the
stdlib answer to exactly this. A separate upload/download HTTP channel
(e.g. the runtime exposing a local port) would break the outbound-only,
single-connection transport model. Chunked transfer messages would triple
the protocol surface before any project needed >24 MB files over this
path; the cap is documented in PROTOCOL.md and the upgrade path is noted
there.

**Consequence.** Copy/move are confined to one filesystem (the root) —
acceptable, since a Project's tree lives on one device in practice. HTML
and SVG are served with an attachment disposition so stored project files
can never render as documents on the Control Server origin.

## ADR-019 — devstationone-admin CLI replaces env-var admin bootstrap

Date: 2026-09-13

**Decision.** `internal/auth.BootstrapAdmin` and the
`DEVSTATIONONE_ADMIN_EMAIL`/`DEVSTATIONONE_ADMIN_PASSWORD` server env vars
are removed. A new binary, `cmd/devstationone-admin`, seeds or resets the
administrator account instead: it takes `--email` and the password on
stdin, connects using the same `DEVSTATIONONE_DATABASE_URL` the server
uses (`config.LoadServer`), and upserts via a new `auth.UpsertAdmin`
(`INSERT ... ON CONFLICT (email) DO UPDATE`, `xmax = 0` distinguishing
insert from update for the CLI's "created"/"updated" message). A minimum
8-character password floor is enforced in the CLI — the first password
rule in the project; `auth.CreateUser`/`Authenticate` still validate
nothing beyond bcrypt's own limits.

**Why not env vars.** The env-var path only ever fired once (first boot,
empty `users` table) and had no story for resetting a lost admin password
short of manually editing the database or wiping the table. A CLI upsert
covers both seeding and recovery with one code path, and keeps credential
material out of a long-lived `server.env` file on disk.

**Consequence.** The Control Server no longer creates any account at
startup; an operator must run `devstationone-admin` after first deploy
before anyone can log in. No transition period — this is a pre-1.0
project with no installed base to migrate. Release tarball
(`.github/workflows/release.yml`) and `install.sh` now build/ship/install
three binaries instead of two.

## ADR-020 — Project setup UI: runtime-reported home dir/shells replace typed `os_user`/shell path/cwd

Date: 2026-09-13

**Decision.** A Project's `os_user` column is dropped (superseding the
"Project's `os_user`" framing in ADR-014, which still holds — `session.open`
still carries `os_user` and the runtime still refuses a mismatch — only the
source changes). `protocol.Capabilities` (`internal/protocol/protocol.go`)
gains `os_username`, `home_dir`, and `shells`, populated once at connect
time from `os/user.Current()` and a plain-text parse of `/etc/shells`
(`internal/agent/agent.go`) — a runtime always runs as one fixed OS user
(ADR-014's premise), so that identity is reported live instead of typed into
a Project. `internal/api/sessions.go` sources `session.open`'s `os_user`
from `runtimes.GetByID(...).Capabilities.OSUsername` at session-create time.

Project create/update now validate against that live state instead of
trusting client-typed strings: `shell` must be a member of the runtime's
reported `shells`; `working_directory` must resolve inside the runtime's
`home_dir`, checked with a cheap `filepath.Rel` early-rejection followed by
a real `stat` `file.command` RPC to the runtime for the actual containment
guarantee; both create and update require the target runtime to be online
and to have reported a home directory, re-checked on every update (even one
that only renames the project).

The Project setup UI (`web/static/projects.html`, `project.html`) replaces
the "OS user" text input (removed entirely) and the free-text shell/cwd
inputs with a shell `<select>` populated from the runtime's reported
`shells`, and a directory-picker modal (`DS.browseDirectory` in
`web/static/js/dialogs.js`) that browses the runtime's home directory. The
picker is a new pair of Control Server endpoints,
`GET/POST /api/v1/runtimes/{runtimeID}/browse[/mkdir]`
(`internal/api/runtime_browse.go`) — reusing the existing `file.command`
RPC and its runtime-side `os.Root` containment
(`internal/filesystem`, ADR-018) with `root` set to the runtime's home
directory instead of a Project's working directory. No new protocol message
types were needed.

**Why runtime-reported instead of client-typed.** The runtime already knows
its own OS user, home directory, and available shells; asking the browser
user to type them invites typos that only surface as a cryptic PTY-start
failure (ADR-014) or a session that can't find its working directory.
Reusing `file.command` for the picker, rather than inventing a
directory-listing RPC, means the picker inherits the Files feature's
existing containment guarantees for free.

**Consequence.** `migrations/0008_drop_project_os_user.up.sql` drops the
column outright (down migration re-adds it `NOT NULL DEFAULT ''` — no
backfill; this is a pre-1.0 project with no installed base to migrate, same
posture as ADR-019). A Project's `os_user` is gone from the API request and
response shapes; existing API clients sending it now have that field
silently ignored on decode. Any signed-in session may browse any runtime's
home directory (matching `GET /api/v1/runtimes`' existing visibility) —
runtimes are shared infrastructure, not owned per-user, so this is not a new
privilege boundary.

## ADR-021 — Database connections: per-request pool cache instead of a session table, credentials encrypted at rest, SQL Editor as an intentional arbitrary-execution surface

Date: 2026-09-13

**Decision.** A saved database connection (`database_connections`, scoped to
a Project like `terminal_sessions`) stores no live connection state — only
engine, non-secret fields, and an AES-256-GCM-sealed `encrypted_secret`
(password + free-form extra params, one blob). `internal/dbconnections`
(Control Server) is the only package that ever decrypts one, and only to
build a `db.command` RPC payload dispatched to a runtime; the plaintext
never appears in an API response or a log line. The encryption key comes
from a new required env var, `DEVSTATIONONE_DB_ENCRYPTION_KEY` (32 raw
bytes, base64), validated at `config.LoadServer` startup the same way
`DEVSTATIONONE_RUNTIME_TOKEN` is validated for the runtime binary.

Runtime-side, `internal/dbconnectors` defines one `Connector` interface
(`Open(ctx, Params) (*sql.DB, error)`) with four engine implementations —
PostgreSQL (`pgx`/`stdlib`), MySQL/MariaDB (`go-sql-driver/mysql`), MSSQL
(`microsoft/go-mssqldb`), SQLite (`modernc.org/sqlite`) — all four pure Go,
no cgo. `Open` builds the engine's native DSN and opens+pings via
`database/sql`, which already pools connections internally; Slice 7 doesn't
add a second, custom pooling/session layer on top; `SQLite` intentionally
skips SQLCipher-style encrypted-file support given the pure-Go constraint —
noted as a ceiling in `sqlite.go`, not an oversight.

**Why not a session table.** Terminal sessions need a table because a PTY's
process must survive a browser refresh and support reattach across
requests. A database connection test/query is one-shot: the runtime opens,
uses, and (in this slice) closes it within a single RPC. Inventing
`db.session.open`/`db.session.close` messages and a session-ownership model
before there's a real multi-request-per-connection use case (the schema
tree, data grid, and SQL editor land in a later slice) would be exactly the
kind of unrequested abstraction the project's simplicity rules forbid. When
that need arrives, the natural extension is a `map[connectionID]*sql.DB`
cache in the runtime's `db_manager.go`, keyed by connection ID rather than
by browser session — `database/sql`'s own pool already gives concurrent
callers independent-feeling access to one cached `*sql.DB`, so per-tab
isolation doesn't require per-tab pools either.

**Why AES-256-GCM over a KMS/vault integration.** DevStationOne is
self-hosted, single-tenant per install (PRD "the customer owns and operates
the Control Server"); a symmetric key in an env var matches every other
secret in this project's threat model (`DEVSTATIONONE_RUNTIME_TOKEN` et al.)
and needs no new infrastructure dependency. GCM's authenticated encryption
means a tampered ciphertext fails to decrypt rather than silently producing
garbage credentials.

**SQL Editor is a deliberate parallel to the terminal, not an oversight.**
The brainstorm/design pass for the combined schema+data-browsing slices
settled on full SQL power (arbitrary DDL/DML, no confirm-before-write gate)
for the same reason `docs/SECURITY.md` already accepts the terminal as
"arbitrary code execution by design" — the user is operating their own
database. This ADR records the decision now so a future reviewer doesn't
mistake the lack of a write-confirmation dialog for a missed control.

**Consequence.** `DEVSTATIONONE_DB_ENCRYPTION_KEY` is a new required env var
for the Control Server — a clean checkout that omits it fails to start, same
posture as ADR-019/ADR-020 (pre-1.0, no installed base to migrate). Every
`NewRouter` call site (production and test) now takes the key. Real test
databases for MySQL and MSSQL were added to `docker-compose.yml`
(`dbtest-mysql`, `dbtest-mssql`, started via `make dbtest-up`) alongside the
existing Postgres service, so `internal/dbconnectors`' engine connectors are
covered by integration tests against real engines, not mocks.

## ADR-022 — Slice 8: schema introspection queries and the runtime pool cache go live

Date: 2026-09-13

**Decision.** `internal/dbconnectors.Connector` gains `Schemas`, `Tables`,
`Columns`, `Indexes`, `Constraints`, `Triggers`, implemented per engine:

- **Schemas.** PostgreSQL/MSSQL query `information_schema.schemata` (MSSQL
  additionally excludes the fixed-role schemas, which share a name with
  `db_owner` et al. and aren't user-browsable). MySQL/MariaDB has no
  separate schema concept — `Schemas` returns `[Params.Database]` without a
  query. SQLite returns `nil` — the UI skips the schema level of the tree
  for both.
- **Tables/Columns.** `information_schema.tables`/`.columns` for all three
  network engines (MySQL's `column_type` is used over `data_type`, since it
  includes length/unsigned detail `data_type` drops). SQLite:
  `sqlite_master`/`PRAGMA table_info`.
- **Indexes.** None of the three engines expose index *columns* through
  `information_schema`, only per-column engine-native catalogs: PostgreSQL
  (`pg_index`/`pg_attribute`/`unnest(...) WITH ORDINALITY`), MySQL
  (`information_schema.statistics`, which despite the name does carry
  index/column data), MSSQL (`sys.indexes`/`sys.index_columns`/
  `sys.columns`). SQLite: `PRAGMA index_list`/`PRAGMA index_info`.
- **Constraints.** `information_schema.table_constraints` joined to
  `key_column_usage` (the constrained columns) and, for a foreign key, the
  referenced schema/table/column — `constraint_column_usage` directly for
  PostgreSQL/MySQL, routed through `referential_constraints` for MSSQL
  (its `constraint_column_usage` doesn't carry the FK's own name, only the
  unique/PK constraint it points at). SQLite has no such information at
  all: primary key comes from `PRAGMA table_info`'s `pk` column, foreign
  keys from `PRAGMA foreign_key_list`, and unique constraints from
  `PRAGMA index_list` entries with `origin='u'`.
- **Triggers.** `information_schema.triggers` for PostgreSQL/MySQL,
  `sys.triggers`/`sys.tables`/`sys.schemas` for MSSQL (no
  `information_schema.triggers` there), `sqlite_master WHERE
  type='trigger'` for SQLite — name only, since SQLite exposes no
  structured event/timing metadata short of parsing the trigger's stored
  SQL text, which is out of scope (represented honestly: left empty rather
  than guessed).

All four engines' introspection is verified by `internal/dbconnectors/
introspect_test.go` against real PostgreSQL/MySQL/MSSQL/SQLite databases —
each test creates a real parent/child table pair with a foreign key, an
index, and a trigger, then asserts the connector finds them, not just that
the queries don't error.

**Why not parameterize PRAGMA statements.** SQLite's `PRAGMA` doesn't
accept bind parameters, so a table/index name has to be interpolated
directly into the statement text. `quoteSQLiteIdent` double-quotes the
identifier and doubles any embedded `"` (the standard SQL identifier-quoting
rule), which SQLite honors for `PRAGMA` targets — safe against a
maliciously-named table without needing bind parameters.

**The runtime connection pool cache activates.** ADR-021 predicted this
exact trigger for upgrading from "open a connection per RPC" to "cache one
per connection ID": schema browsing fires several RPCs per tree-node
expand (a table's columns, indexes, constraints, and triggers all load at
once), and reopening a fresh connection — a TLS handshake included, for
PostgreSQL/MSSQL — on each one is a real, user-visible latency cost. Per
ADR-021's stated upgrade path, this is a plain `map[connectionID]*sql.DB`
in `internal/agent/db_manager.go` (`dbManager.pools`), not a
`db.session.open`/`close` message pair — `database/sql`'s own pool already
handles concurrent use of one cached `*sql.DB` from multiple browser tabs.
A `db.command{action:"invalidate"}` — fire-and-forget via `SendEnvelope`,
no reply — is dispatched by `internal/api/databases.go` after every
PATCH/DELETE on a connection, so edited or removed credentials take effect
immediately rather than waiting out the 5-minute idle-eviction window.
`TestDatabaseConnectionHTTP_EditInvalidatesCachedPool` (internal/api)
proves this end-to-end: without the invalidate call, a `test` after
breaking the stored password would still report `ok: true` off the
still-open pool from before the edit.

**Consequence.** `protocol.DBCommandPayload` gains `ConnectionID` (the pool
cache key — not itself a credential) and `Schema`/`Table` (request scope);
`DBResultPayload` gains `Schemas`/`Tables`/`Columns`/`Indexes`/
`Constraints`/`Triggers`. The Databases tab UI grows a "Browse schema"
button per connection card, opening a lazy-loading tree (schemas → tables →
columns/indexes/constraints/triggers) in a panel below the card grid —
additive to the Slice 7 card grid, not the full split-pane
connections-tree-replaces-cards redesign from the brainstorm/design pass;
that larger UI consolidation, plus data browsing and the SQL editor, remain
Slice 9 and later.

## ADR-023 — Slice 9: raw-SQL filter, client-side export, and column-type-driven value coercion

Date: 2026-09-14

**Decision.** `internal/dbconnectors.Connector` gains `Rows`, `Commit`,
`Query`. `Rows` paginates via `LIMIT n+1 OFFSET m` (MSSQL:
`OFFSET/FETCH`, which forces an `ORDER BY` — `(SELECT NULL)` when the
caller didn't sort) and reports `has_more` from the extra row rather than a
separate `COUNT(*)`. `filter` is accepted as a raw SQL boolean expression
appended after `WHERE`, not a parameterized value or a structured
filter-builder DSL. `Commit` builds parameterized `INSERT`/`UPDATE`/
`DELETE` per staged edit — every column value bound, column/table
identifiers quoted per engine, using whichever placeholder syntax that
engine's driver actually accepts (`$1` Postgres, `?` MySQL/SQLite, `@p1`
MSSQL). `Query` runs SQL-Editor text verbatim, routed to `Exec` or `Query`
by a keyword sniff (INSERT/UPDATE/DELETE/CREATE/DROP/ALTER/TRUNCATE/GRANT/
REVOKE/REPLACE → Exec).

**Why `filter` is raw SQL, not parameterized or structured.** The
brainstorm/design pass that scoped Slices 7–9 together already decided the
SQL Editor gets full, unconfirmed arbitrary-SQL power — the same posture
`docs/SECURITY.md` already grants the terminal ("arbitrary code execution
by design"). Building a separate structured filter-expression language for
the data grid, when the SQL Editor sitting one click away already accepts
anything, would be real effort spent hiding a boundary that doesn't
actually exist for this user. `filter` inherits the SQL Editor's trust
model explicitly rather than accidentally.

**Why row edits stay parameterized despite that.** `filter`/`query` are
text the user is knowingly typing as SQL. A row edit's *values* are just
data the user typed into a spreadsheet-like cell — nothing about that
interaction implies "this is SQL," so binding them is both the safe choice
and the one that matches user intent (a value with a literal `'` in it
should be stored as typed, not treated as SQL syntax).

**Why export is client-side.** The rows already sitting in the browser
after a `GET .../rows` or SQL Editor run are the export — turning them into
CSV/JSON/SQL text and triggering a `Blob` download needs no round trip.
A server-side streaming export endpoint would only earn its cost if export
needed to cover more rows than are practical to page through in the UI
first, which nothing in this slice's scope requires.

**Consequence — a real bug this design surfaced.** The obvious
implementation of "coerce an edited cell back to the right type" is to
inspect the *previous* value's JS type (`typeof original === 'number'`).
That breaks for exactly the two cases that matter most for editing: a cell
whose original value is `NULL` (no JS type to infer from) and any cell in
a row the user just added with "+ Row" (no original value at all). Both
sent a plain numeric-looking string to the runtime, which Postgres
correctly rejected as a type mismatch on the bind parameter. Fixed by
coercing from the column's actual SQL type (already available from the
columns endpoint, fetched once per table) instead of the previous value's
JS type — this also makes an empty cell mean "set to NULL" rather than "set
to empty string," which is the more useful default for a nullable column.
Caught by a real browser smoke test inserting a new row with a numeric
column, not by the Go-level `rows_test.go` suite, since that suite only
exercises `internal/dbconnectors` directly and the coercion bug lived
entirely in `project.html`'s JavaScript.
