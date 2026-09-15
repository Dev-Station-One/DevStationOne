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

## ADR-024 — Slice 10: a unified Principal/Grant model over three incompatible native ones, privilege whitelisting, and SQLite left out on purpose

Date: 2026-09-14

**Decision.** `internal/dbconnectors.Connector` gains
`SupportsUserManagement`, `Principals`, `Grants`, `CreatePrincipal`,
`DropPrincipal`, `SetPrincipalPassword`, `GrantPrivilege`,
`RevokePrivilege`, backed by two new shared types: `Principal{Name,
CanLogin, Superuser}` and `Grant{Privilege, Schema, Table, Raw}`. Every
engine maps its own account model onto this shape rather than the API
exposing three different shapes:

- **PostgreSQL** — native `ROLE`s. `CanLogin`/`Superuser` are literally
  `LOGIN`/`NOLOGIN` and `SUPERUSER`/`NOSUPERUSER`. `Grants` reads
  `information_schema.role_table_grants`, giving structured
  `{Privilege, Schema, Table}` rows.
- **MySQL/MariaDB** — accounts are keyed by `(user, host)`, not a single
  name. `Principal.Name` round-trips this as `"user@host"`
  (`mysqlPrincipal`/`mysqlSplitPrincipal`), defaulting `host` to `"%"` on
  create. There's no `NOLOGIN` equivalent, so `CanLogin` is always `true`.
  `Grants` has no single-privilege-per-row catalog view covering every
  grant type the way Postgres does, so it returns `SHOW GRANTS`' own
  statement text verbatim in `Grant.Raw` instead of trying to parse it —
  the UI renders `Raw` as-is and skips offering a `Revoke` button next to
  it, since there's no single structured privilege to revoke.
- **MSSQL** — no single statement creates "a user who can log in"; a
  server login and a database user are two separate objects.
  `CreatePrincipal` creates both as a pair (`CREATE LOGIN` +
  `CREATE USER ... FOR LOGIN`), `CanLogin: false` disables the login
  afterward (`ALTER LOGIN ... DISABLE`) rather than being expressible
  inline, and `Superuser` adds the login to the `sysadmin` server role.
  `DropPrincipal` drops the user before the login — MSSQL refuses to drop a
  login that still owns a mapped user in the current database.
- **SQLite** — every method returns the new `dbconnectors.ErrUnsupported`
  sentinel and `SupportsUserManagement()` is `false`. A SQLite "database" is
  a file; access control is OS file permissions the runtime's filesystem
  containment already enforces, not a server-side concept this app can
  honestly represent (PRD §6 says as much explicitly). The API layer
  (`dbUserManagementSupported`) rejects a sqlite connection's principal/
  grant request with `400` before it ever reaches the runtime, and the UI
  hides the Users tab for a sqlite connection — `ErrUnsupported` is the
  defense-in-depth backstop, not the primary path.

**Why privilege is whitelisted instead of bound as a parameter.**
`GRANT`/`REVOKE ... TO/FROM` syntax takes a privilege keyword, not an
expression — there is no placeholder position for it the way `Commit`
(Slice 9) binds row values. `normalizePrivilege` upper-cases and checks
against a fixed set (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `ALL`),
independently in both the API layer's request validation and each
connector's `GrantPrivilege`/`RevokePrivilege` (defense in depth,
`docs/SECURITY.md`) — the same trust boundary this package already applies
to identifiers (`quoteIdent`), not the raw-SQL trust model `filter`/`query`
get (ADR-021/023). A user typing "grant SELECT" is not the same act as a
user typing SQL into the editor, so it doesn't inherit that posture.

**Why `CREATE ROLE ... PASSWORD` / `ALTER USER ... IDENTIFIED BY` /
`CREATE LOGIN ... WITH PASSWORD` take an escaped literal, not a bound
parameter.** None of the three engines' account-DDL statements accept a
placeholder in the password position — same fundamental limitation as the
privilege keyword above, just for a different reason (DDL, not a keyword
enum). `sqlLiteral` escapes by doubling embedded `'`, the standard SQL
string-literal rule, shared by all three engines' implementations rather
than three near-identical copies.

## ADR-025 — Slice 11: best-effort recording at the call site, no runtime protocol, and principal/grant events added beyond SECURITY.md's list

Date: 2026-09-14

**Decision.** `internal/audit.Record` is called directly from each Control
Server HTTP handler that performs a security-relevant action, right after
that action succeeds — there is no event bus, no outbox table, and no
runtime-side involvement. Every action this slice audits (auth, runtimes,
projects, sessions, database connections/principals/grants, services,
files) is already resolved entirely on the Control Server by the time the
handler knows the outcome, so there is nothing for `docs/PROTOCOL.md` to
carry; a runtime-side action (e.g. a PTY actually exiting) was deliberately
left out of scope rather than inventing a new protocol message for it.

**Why best-effort, not transactional with the action it describes.**
`Record` swallows its own error (logged via `slog.Default()`, doesn't
return one) rather than wrapping the triggering database write in a
transaction with the audit insert, or failing the request when the insert
fails. `docs/SECURITY.md` requires the *action* (login, revoke, delete...)
to be safe and correct; making that action's success conditional on the
audit log's availability would let an audit-table outage (e.g. a botched
manual `DELETE` while debugging) take down login or project management with
it. The failure mode this accepts — a gap in the log instead of a rejected
request — matches the existing precedent for non-critical side effects in
this codebase (`invalidateRuntimeCache`, `sessions.MarkFailed` are both
fire-and-forget for the same reason).

**Why principal/grant events (`database_principal.*`, `database_grant.*`)
are recorded even though `docs/SECURITY.md`'s "Audit" list only names
"database connection changes".** That list was written before Slice 10
added principal/grant management; a database user creation or privilege
grant is at least as security-relevant as a connection metadata edit, and
leaving it unaudited would be a silent gap a reviewer would flag. Recorded
instead of skipped; `docs/SECURITY.md` is updated to name it explicitly
rather than leaving the API as the only source of truth.

**Why filtering is structured query parameters, not the raw-SQL `WHERE`
pattern Slice 9 uses for the data browser/SQL editor.** `internal/audit.List`
takes a typed `Filter{EventType, ActorUserID, ResourceType, Since, Until,
Limit, Offset}`, each bound as its own parameter in a fixed query — unlike
`dbconnectors`' data browser, arbitrary-SQL execution is not this
resource's trust model (ADR-021), and every real dimension an admin filters
audit events by is already a fixed, known set of columns.

**Why retention is a background loop in `cmd/devstationone-server`, not a
`cron`/systemd-timer unit or a lazy per-write check.** A daily
`time.Ticker` started alongside the HTTP server (`auditPurgeLoop`) matches
this binary's existing pattern of self-contained background work (the
runtime connection registry's heartbeat handling lives the same way) rather
than depending on OS-level scheduling the Control Server doesn't otherwise
require. It also runs once at startup so a policy change or a missed day
doesn't wait a full interval to take effect. A per-write purge was rejected
as needless work on every hot-path audit insert for a policy that only
needs day-granularity enforcement.

**Why `actor_email` is stored redundantly alongside `actor_user_id`.**
`actor_user_id` is `ON DELETE SET NULL` — if a user is ever removed, the
historical record of who performed an action would otherwise become
anonymous. `actor_email` is captured at event time and never updated, so a
security review of old events still shows who did what even after the
account is gone. It's also the only actor identity available at all for a
failed login (`auth.login_failed`), which by definition has no
authenticated user to reference.

## ADR-026 — Slice 12: production hardening choices — expvar over Prometheus, Origin-check CSRF over tokens, per-process rate limiters, no CSP nonce infrastructure

Date: 2026-09-14

**Decision.** Four independent hardening mechanisms land together as
`internal/api/middleware.go`: `securityHeaders`, `csrfProtect`,
`rateLimit`/`rateLimitAPI` (backed by `ipLimiter`, a per-IP
`golang.org/x/time/rate` token bucket), plus `withRequestLogging` rewritten
to emit one structured line per request (status, duration, a random
correlation ID) and stamp `X-Request-Id`. `internal/api/metrics.go` adds
`GET /api/v1/metrics`. `wsAcceptOptions` (shared by both WebSocket upgrade
points) disables per-message compression. None of this touches the
protocol (`docs/PROTOCOL.md`) or introduces a stored resource — it's all
process-level, HTTP-layer hardening.

**Why `expvar` (stdlib) instead of a Prometheus client library.** This MVP
has no metrics backend deployed to actually scrape a Prometheus exposition
endpoint yet — adding that dependency now would be speculative. `expvar`'s
default JSON format is trivially scrapeable by a script if/when one is
needed, at zero new dependency cost. `metricsHandler` deliberately doesn't
call `expvar.Publish` for the two live gauges (`runtime_connections`,
`terminal_sessions_attached`): `Publish` panics if the same name is
registered twice, and `NewRouter` — the function that would need to call
it — runs once per test in `internal/api`'s own suite, each with a fresh
`*runtimes.Registry`/`*sessions.Hub`. The handler instead re-implements
`expvar.Handler`'s own JSON-object format inline and appends the two
gauges by reading the request-scoped registry/hub directly.

**Why CSRF defense is an `Origin` header check, not a token
(double-submit or synchronizer).** The session cookie is already
`SameSite=Lax` (ADR-009) — a browser withholds it on a cross-site
`POST`/`fetch` by construction, which is the entire CSRF attack surface for
a cookie-authenticated API. A token scheme would duplicate a protection
this app already has for free, at the cost of every mutating request
needing to carry and every handler needing to verify a second credential.
The `Origin` check in `csrfProtect` is deliberately narrow: it only
rejects a request whose `Origin` header is present *and* disagrees with
the request's own `Host` — never a request with no `Origin` header at
all, which includes the runtime's own bearer-token-authenticated
`POST`-free WebSocket upgrade and any legitimately same-site request an
older browser sends without one.

**Why rate limiters are constructed fresh inside `NewRouter`, not as
package-level variables.** A package-level `*ipLimiter` would be shared
process-wide — fine for the one real Control Server process, but every
test in `internal/api` calls `NewRouter` at least once (some many times
per test via `sessionsTestSetupIn`), all in the same test binary process.
A shared limiter's token bucket would drain across unrelated tests,
producing spurious `429`s with no relationship to what any individual test
is doing. Scoping the limiter to the `NewRouter` call it's used in — one
instance per Control Server, and incidentally one independent instance per
test — is simpler than adding test-only bypass logic and correct in both
contexts.

**Why the general (non-login) rate limit only applies to
`POST`/`PUT`/`PATCH`/`DELETE`, not `GET`.** The UI polls read endpoints by
design — `pollUntil`-style waits for runtime-online/session-ready state
exist throughout `internal/api`'s own test suite, hitting a `GET` endpoint
every 10ms, and the same pattern exists in the real browser client
(service/session status). The actual abuse surface a rate limit needs to
address — brute-forcing login, mass resource creation/deletion — is
entirely mutating requests; exempting `GET` avoids the false choice
between a limit loose enough not to break legitimate polling and one tight
enough to matter.

**Why the Content-Security-Policy keeps `'unsafe-inline'` instead of a
nonce/hash-based policy.** Every page under `web/static/` carries its
page-specific logic as an inline `<script>` block (and some inline
`<style>`) — there is no bundler/build step (a deliberate absence; see the
"Files"/UI sections of docs/ARCHITECTURE.md), so a nonce would have to be
generated per-request and templated into static files that are currently
served byte-for-byte from disk via `http.FileServer`. That's a real
templating layer this slice doesn't add. The policy still restricts every
resource type to `'self'` (plus `data:` for images and `ws:`/`wss:` for
the terminal/runtime connections) — the meaningful boundary (no
third-party origin can be a load-bearing dependency, and a reflected-HTML
injection can't pull in an external script) holds either way.

**Why `/api/v1/metrics` requires an administrator session instead of being
open for an unauthenticated scraper.** There's no scrape infrastructure
defined for this MVP (see above) to weigh against the cost of exposing
even coarse operational counts (request volume, connected runtime count)
without authentication. Gating it the same way every other admin-only
endpoint already is costs nothing today; loosening it later for a specific
scrape setup is a one-line change when that setup exists.

## ADR-027 — Slice 13: packaging closed a real gap — the Control Server unit ran as root

Date: 2026-09-14

**Context.** `install.sh` and `.github/workflows/release.yml` (release
tarball build/publish, checksum verification, systemd unit generation for
both binaries) already existed from ADR-019, ahead of this plan formally
reaching Slice 13 — that ADR's "Consequence" section notes the release
tooling picking up the third binary, but Slice 13 itself was never marked
against it. This slice is the verification/close-out pass, not a rewrite.

**What verification found: the generated Control Server unit had no
`User=`, so it ran as root.** `ProtectSystem=strict`/`ProtectHome`/
`NoNewPrivileges` were already present, but none of them drop privilege —
they narrow what root can still do, they don't stop being root. Every
other privilege-boundary decision in this project (the Development Runtime
never needing root; `docs/SECURITY.md`'s whole "defense in depth" section)
argues the same standard should apply to the Control Server's own unit.
Fixed: `install_server` now creates a dedicated `devstationone` system
account (`useradd --system --no-create-home --shell /usr/sbin/nologin`,
idempotent) and the unit runs as it (`User=`/`Group=devstationone`).

**Why this account creation is skipped under `DEVSTATIONONE_INSTALL_ROOT`.**
That variable exists (per the script's own header comment) so a sandboxed
run can be smoke-tested without touching the real host — actually calling
`useradd` there would create a real system user on whatever machine runs
the test (a CI runner, a developer's laptop), which is exactly the side
effect the sandbox exists to prevent. The generated unit file still gets
`User=devstationone` either way; only the real account mutation is gated.

**A second real gap, found the same way: the generated `server.env` had
no `DEVSTATIONONE_DB_ENCRYPTION_KEY`, a required setting** —
`config.LoadServer` refuses to start without it, so a fresh install would
crash-loop until an operator noticed and added it by hand. Fixed by
generating one with `openssl rand -base64 32` the first time `server.env`
is written (never on a later re-install — that branch is the same
already-existing idempotency guard that also preserves a hand-edited
`server.env`, so an upgrade never silently rotates the key, which would
orphan every stored database connection's credentials per ADR-021). A
placeholder value was rejected outright: unlike `DATABASE_URL`'s
`change-me` (an obviously-fake value an operator is expected to notice and
edit), a fixed example encryption key is the kind of placeholder someone
plausibly leaves in place, and doing so would be catastrophic rather than
merely broken.

**Verification.** `scripts/test-install.sh` (`make test-install`) is a new
smoke test — not present before this pass — exercising both `install.sh
--server`/`--runtime` against a locally built release tarball, sandboxed
under `DEVSTATIONONE_INSTALL_ROOT`: binaries land in the right places, the
generated unit carries `User=devstationone`, `server.env` gets a real
generated key, and re-running install preserves a hand-edited env file.
The real-GitHub download+checksum branch (`DEVSTATIONONE_TARBALL_URL`
unset) isn't covered — that needs an actual release to fetch from, so it's
exercised by real installs instead, not something worth building network-
mocking infrastructure for. Also fixed while verifying: two stale docs
(`README.md` claimed "Go 1.22+" against an actual `go.mod` of 1.26;
`docs/TESTING.md` claimed `/readyz` answers `{"status":"ok"}`, but
`readyHandler` answers `{"status":"ready"}`) and a broken README
instruction (`sudo -u devstationone devstationone-admin ...` with no
environment sourced — `devstationone-admin` needs the same two env vars
the server does, and `sudo -u` doesn't inherit them).

## ADR-028 — Git: real `git` CLI via `os/exec`, not go-git, mirroring the Files RPC pattern (Slice 16)

Date: 2026-09-14

**Decision.** The runtime's git capability (Slice 16: status, diff, stage,
unstage, commit) runs the real `git` binary as a direct child process
(`internal/agent/git_manager.go`, `os/exec`, argv only, no shell), with
`cmd.Dir` set to the Project's working directory. It reuses the Slice 6
file.command shape exactly: a `git.command`/`git.result`/`git.error`
message triple over the Slice 5 correlated request/response machinery,
`root` always server-derived from the Project record, `path` root-relative
and `filesystem.Clean`-validated. Commit messages travel over `git commit
-F -`'s stdin, never as a process argument (`docs/SECURITY.md` — process
arguments are visible to other users on the host via `/proc` or `ps`).

**Why not go-git.** `github.com/go-git/go-git` (a pure-Go git
implementation) was evaluated and rejected. It still has no credential
helper support (go-git/go-git#1420, open since 2025) and doesn't run local
git hooks (pre-commit, commit-msg) the way `git commit` does — both would
silently diverge from the git behavior the runtime's OS user already has
configured, which is the same property that made `os/exec` the right
choice for `internal/services`' systemctl/journalctl calls (ADR-017).
GPG commit signing and
custom diff/merge drivers configured for that user would be bypassed the
same way. A future slice (branch/push/pull, noted as reusing this same RPC
pattern with new `Action` values) needs the credential helper specifically
— adopting go-git now would mean a second migration back to `os/exec`
later rather than one clean choice up front. The real `git` binary is
already a required dependency of any development machine this runtime runs
on, so `os/exec` costs nothing go-git would save.

**Containment for status/diff.** Unlike file operations (`os.Root`,
ADR-018), git operations aren't sandboxed by an OS-level containment
primitive — `git` itself decides what it can see, by walking up from `cmd.Dir`
to find `.git`. If a Project's working directory is a subdirectory of a
larger git checkout, a bare `git status`/`git diff` would report the whole
outer repository, leaking paths and diffs outside the Project's authorized
root into the API response. Fixed by passing a `.` pathspec (`-- .`) to
scope both to the Project root, the same one-flag fix in both the
Control Server's early path validation and the runtime's re-validation
(`docs/SECURITY.md` "defense in depth").

**Consequence — a known, accepted gap.** `stage`/`unstage`/`commit` are
*not* given the same pathspec treatment, because git's own semantics make
it unnecessary for the paths this system ever passes (always root-relative
and `filesystem.Clean`-validated, so `git add`/`git reset` can't be made to
touch anything outside `root`). But git's *index* is repository-wide, not
directory-scoped: if a Project's working directory sits inside a larger
checkout and something outside DevStationOne (a human at a shell) has
already staged changes elsewhere in that same repository, this system's
`commit` action commits the whole staged index — including those
unrelated changes — not just what its own `status`/`diff` calls showed.
This is inherent to running any git tool in a subdirectory of a larger
repository, not a defect introduced by scoping status/diff reads; a
pre-commit check that refuses to commit unless every staged path is under
`root` was considered and deferred as unneeded engineering unless a real
deployment actually uses Projects rooted inside larger checkouts (the
documented/expected configuration is one Project, one repository).

## ADR-029 — Notifications: idle heuristic lives in sessions.Hub, offset/limit pagination over the design's cursor note, NewRouter returns its Hub/Bus (Slice 15)

Date: 2026-09-14

**Decision.** Slice 15's `internal/notifications` package mirrors
`internal/audit`'s shape and best-effort `Record`/logged-and-swallowed-error
convention, but scoped per-user with a read flag instead of being a
system-wide log, and it additionally publishes each recorded row on the
Slice 14 `events.Bus` as `notification.created` so an open dashboard updates
live. There is no separate REST/wire DTO the way `internal/api`'s
`auditEventResponse` exists for audit events — `notifications.Notification`
carries its own `json` tags and is used directly both as the REST list
response element and as the live event payload, since no field needs
hiding or reshaping for either consumer (unlike audit, nothing here is
security-log-only).

**Idle heuristic stays inside sessions.Hub.** The "awaiting input" signal
needs two things: knowing when a session last produced output, and not
re-notifying on every scan once a session is already flagged idle. Both are
in-memory, per-session, and `Hub.Forward` is already the single choke point
every runtime output byte passes through (docs/PROTOCOL.md), so
`lastActivity`/`idleNotified` maps and a new `Hub.IdleSessions(threshold)`
method live there rather than in a new type. `IdleSessions` is
self-contained — it never imports `internal/notifications` or
`internal/database` — so `internal/sessions` gains no new dependency; the
10s-interval scan loop that calls it, resolves each session's project
owner, and calls `notifications.Record` lives in
`cmd/devstationone-server/main.go`'s new `idleNotifyLoop`, next to the
existing `auditPurgeLoop` it mirrors.

**Deviation from the design note: offset/limit, not a cursor.** The Slice
15 design entry called `GET /api/v1/notifications` "cursor-paginated,
mirrors audit.List shape" — but `audit.List` itself is offset/limit, not
cursor-based, so the two clauses conflicted. `notifications.ListForUser`
follows `audit.List` literally (limit capped at 200, default 50, plus a
total count) rather than inventing a second, cursor-based pagination
convention for one endpoint. The bell dropdown only ever asks for the most
recent page, so a cursor bought nothing here; product intent (a working,
paginated notification list) is preserved.

**NewRouter now returns its Hub and Bus.** `idleNotifyLoop` needs the exact
`sessions.Hub` instance that `Forward` (driven by real terminal output)
updates, and the exact `events.Bus` instance browsers are subscribed to —
not a second, disconnected pair. `internal/api.NewRouter` already
constructs both internally (as it does `runtimes.Registry`), so rather than
inverting that ownership (callers constructing infrastructure and passing
it in, unlike the existing `registry`/`hub`/`bus` convention) it now
returns `(http.Handler, *sessions.Hub, *events.Bus)`; `main.go` passes them
to `idleNotifyLoop` the same way it already owns `auditPurgeLoop`. All
Go-side test call sites were updated to discard the two new return values.

**Awaiting-input notifications are not started per browser attach/detach,
only per Forward.** A session can go idle while nobody is watching (no
browser attached) just as easily as while attached; gating the idle clock
on attach state would miss the common "left a long build running and
closed the tab" case the whole feature exists for. `Forward` runs
regardless of whether a browser is currently attached (scrollback keeps
buffering either way), so the idle clock is armed the moment a session
produces any output, attached or not.

## ADR-030 — Runtime reactivation reuses the original enrollment token; dashboard shows online runtimes only

Date: 2026-09-14

**Decision.** `POST /api/v1/runtimes/{runtimeID}/reactivate` (admin-only,
mirrors `revoke`) clears a revoked runtime's `revoked_at` and returns its
status to `pending` — `internal/runtimes.Reactivate` — without touching
`token_hash`. The runtime machine needs no reconfiguration: the same
process, still holding the same `DEVSTATIONONE_RUNTIME_TOKEN` it was given
at enrollment, reconnects (or is simply restarted) and `MarkOnline` takes it
the rest of the way. This was an explicit choice over reissuing a new
token, made knowing revoke's original purpose (killing a credential):
reusing the token is right for the common "I fat-fingered revoke" or
"planned maintenance window" case this button targets, but does **not**
undo a revoke made because a token was suspected compromised — that case
still requires deleting the runtime and creating a new one (a fresh token),
which remains possible today and needed no new endpoint.

**Dashboard shows online runtimes only.** The dashboard's Runtimes card
(`web/static/index.html` `loadRuntimes`) now filters to `online: true`
client-side, matching its own framing as a live glance rather than a
management view (docs/ARCHITECTURE.md "Dashboard events" — payloads are
thin, browsers refetch). Pending/offline/revoked runtimes remain fully
visible on `runtimes.html`, which is unchanged and is where
revoke/reactivate live. This means a newly registered runtime that hasn't
connected yet is invisible on the dashboard until it does — accepted,
since the dashboard's job is "what's alive right now," not "what's
registered."

**Login lands on the dashboard.** `login.html`'s post-login redirect
changed from `/account.html` to `/` — the dashboard, not the account
settings page, is the natural landing surface after signing in. No route
changed; `account.html` is unaffected and still reachable from the user
menu.

## ADR-031 — Integration tests get their own Postgres, separate from the dev Control Server's database

Date: 2026-09-14

**Decision.** A second `postgres-test` service is added to
`docker-compose.yml` (port 55433, db `devstation_test`, `make
dbtest-postgres-up`). Every package's `testPool`/`testDB` helper (`internal/auth`,
`internal/api`, `internal/projects`, `internal/runtimes`, `internal/sessions`,
`internal/dbconnections`, `internal/audit`, `internal/notifications`) now
reads `DEVSTATIONONE_TEST_DATABASE_URL` instead of
`DEVSTATIONONE_DATABASE_URL`, and calls `database.Migrate(url)` before
connecting — `migrate`'s `Up()` is idempotent (`ErrNoChange` on repeat
calls), so this needs no separate migrate step and works against a fresh,
never-migrated container.

**Why.** `DEVSTATIONONE_DATABASE_URL` on 55432 is also what the persistent
`devstationone-dev.service` runs against (`bin/devstationone-server`,
enabled with linger). Every `testPool` helper `TRUNCATE ... CASCADE`s
`users` (and related tables) at the start of each test run — so any
developer following the previously-documented `docs/TESTING.md` workflow
(export `DEVSTATIONONE_DATABASE_URL` at 55432, `make test`) wiped their own
logged-in dev session on the shared instance every time they ran the test
suite. Pointing tests at an isolated container removes the collision
entirely rather than papering over it with a re-seed step.

**Consequence.** `docs/TESTING.md` and `CLAUDE.md`'s "Dev instance" section
are updated to the new variable/port. `make db-up` (55432, dev) and `make
dbtest-postgres-up` (55433, test) are now separate, deliberately —
`db-up` is not on the test critical path and shouldn't imply tests are
ready to run. No migration path for existing `DEVSTATIONONE_DATABASE_URL`
exports: this is a pre-1.0 project, and the old var simply stops being read
by tests (a stale export now makes tests skip with "not set" rather than
truncate the wrong database, which is the safer failure mode).

## ADR-032 — File permissions/ownership: chmod and chgrp only, owner is best-effort

Date: 2026-09-14

**Decision.** The file properties modal (`web/static/project.html`) and its
backing `file.command` actions `chmod`/`chown` (`internal/filesystem`,
`internal/agent/file_manager.go`) support changing permission bits, group,
and owner, with an optional recursive apply for directories. `chmod` masks
its input to the low 9 bits — setuid/setgid/sticky are never settable
through this path. Owner/group are entered by name (not uid/gid), resolved
locally against `os/user` on the runtime; the runtime reports every account
and group it can see (`Capabilities.Users`/`.Groups`, parsed unfiltered from
`/etc/passwd`/`/etc/group`, same "report reality" rule as `Shells`) to back
the modal's dropdowns.

**Why.** A runtime never runs as root — it runs as one fixed, normal OS user
(ADR-014) and that is a hard requirement, not a default. POSIX `chown(2)`
only allows the *superuser* (or a process with `CAP_CHOWN`) to reassign a
file's owning user; an unprivileged owner can only change the file's group,
and only to a group they already belong to. So on every real deployment of
this product, a `chown` request that changes `owner` will fail with `502`
("operation not permitted") — this is expected, not a bug, and not worth
designing around by, say, hiding the owner field: a self-hosted operator who
does run their runtime as root (uncommon but not forbidden) gets a field
that actually works, and everyone else gets an honest OS-level error instead
of a client-side lie about what's possible.

**Consequence.** No new privilege escalation path is introduced — every
`chmod`/`chown` still goes through the same `os.Root`-contained `FS` the
rest of the Files feature uses (docs/SECURITY.md), and the OS enforces the
owner-change restriction independently of anything this application does.
`docs/API.md`/`docs/PROTOCOL.md` call out the almost-always-fails owner case
explicitly so it isn't mistaken for a broken feature.

## ADR-033 — Code tab: 1s stat poll + whole-file debounced sync, no inotify, no OT

**Decision.** The Code tab's live editing uses a runtime-side poller, not
inotify, and whole-file buffers over a debounced save, not character-level
operations. The runtime keeps one watched path set per Project root (seeded
by the Control Server, which always sends the full union of every
subscribed editor's open files), re-stats each path's mtime+size once per
second, and pushes `code.file.changed` when a stat drifts. Saves carry the
client's last-known on-disk fingerprint; on a mismatch the runtime returns
the pre-write content with `stale: true` and still writes
(last-write-wins). Editor file size is capped at 2 MiB
(`protocol.MaxEditorFileBytes`), well below the Files REST path's 24 MiB,
because autosave puts the whole buffer on the wire per debounce tick.

**Why.** The editor's open set is a handful of files, so a 1s `stat` poll
costs nothing, detects external changes (terminal, Claude Code) well inside
the ~2s a human notices, works on every filesystem including network
mounts, and needs no watch lifecycle management — inotify buys sub-second
latency nobody perceives in exchange for watch-limit failures and cleanup
bugs. Whole-file sync with a stat-based stale check gives VS Code-like
behavior (autosave, external-change reload, conflict prompt) without an
OT/CRDT engine or server-side revision numbers; two people editing the same
file concurrently is accepted as last-write-wins, with the loser's content
preserved in the winner's `stale` payload rather than silently discarded.

**Consequence.** The poll loop and watch set live in the runtime's memory:
a runtime restart drops them, and the browser re-sends `watch` when the
runtime comes back online (changes during that gap go undetected —
documented in `docs/PROTOCOL.md`). The saver's own write is not re-pushed
because `save` records the post-save stat; a content push equal to the
current buffer is a client-side no-op, so no echo-suppression bookkeeping
exists. If 2 MiB ever bites, chunked/partial sync is the upgrade path
(`ponytail:` marker at the constant).

## ADR-034 — Project workspace tabs use the real Bootstrap 5.3 Tab component, not tabler.min.js's copy, and skip the Data API

**Decision.** `web/static/project.html` now vendors the official Bootstrap
5.3.8 bundle (`web/static/vendor/bootstrap/js/bootstrap.bundle.min.js`,
fetched from jsDelivr like the other vendor libs) and uses `bootstrap.Tab`
for both the app-level workspace tabs (Overview/Terminal/Files/Code/Source
Control/Databases/Services/Journal — now real `role="tablist"`/`role="tab"`
markup with panels wrapped in a `.tab-content`) and the Code tab's dynamic
per-file tab strip (`web/static/js/editor.js`). Neither uses the
`data-bs-toggle="tab"` Data API; both wire a click handler that calls
`bootstrap.Tab.getOrCreateInstance(el).show()` directly, then react to the
`shown.bs.tab` event for side effects (lazy-loading a tab's data, toggling
`#overview-actions`, re-showing the Code tab's editor host).

**Why.** `tabler.min.js` (already loaded, for `Modal`) is Tabler's own
rebuild of the same Bootstrap component sources under `window.tabler`
instead of `window.bootstrap`. It registers the identical
document-delegated `[data-bs-toggle="tab"]` click listener that real
Bootstrap's Data API registers. Loading both scripts and using
`data-bs-toggle="tab"` would fire *two* independent `Tab` instances (one
per library) per click, double-dispatching `shown.bs.tab` and double-running
every lazy-load side effect. Calling `bootstrap.Tab` explicitly — the
documented "via JavaScript" pattern
(https://getbootstrap.com/docs/5.3/components/navs-tabs/#javascript-behavior)
— sidesteps the Data API entirely, so only the real Bootstrap component
ever runs. Terminal session tabs (`.session-tab`) are deliberately left
alone: they are a session-manager widget (status dot, activity dot, close
button, single shared `#terminal-canvas` reattached per session), not a
content-pane switcher, so the Tab component doesn't fit them.

**Consequence.** The Code tab's file-tab hosts are plain `hidden`-toggled
divs, not `.tab-pane` elements — `bootstrap.Tab.show()` still resolves them
via `data-bs-target` and applies `role="tabpanel"`/`active`/`show`, which is
harmless bookkeeping since our own `activate()` (called from `shown.bs.tab`)
is the actual visibility switch. Two overlapping Tab implementations
(tabler's and real Bootstrap's) now ship on `project.html`; migrating
`modalInstance()` off `tabler.Modal` onto `bootstrap.Modal` and dropping
`tabler.min.js`'s component layer entirely is the natural follow-up if a
similar `data-bs-toggle` collision is ever hit for another component.

**Update (2026-09-15).** That predicted collision happened: the shared
topbar's notification bell and account-menu buttons
(`web/static/js/app.js`, used by every page) carry
`data-bs-toggle="dropdown"` for pages that only load `tabler.min.js`.
On `project.html`, real Bootstrap's Dropdown component also matched that
attribute, so one library's click opened the dropdown and the other's
same-click listener immediately closed it again — the bell and account
menu silently stopped opening on this page only. Fixed narrowly rather
than doing the full migration this ADR flagged above: `project.html` now
removes `data-bs-toggle` from those two buttons at runtime (after
`app.js`'s `DOMContentLoaded` handler injects them) and drives
`bootstrap.Dropdown` directly, the same sidestep already used for
modals/tabs. Every other page is untouched — they never load
`bootstrap.bundle.min.js` and keep using tabler's Data API as before.

## ADR-035 — Cards terminal render mode: client-side regex/idle-timer detection, a single persistent input box, and a permanent full-screen-TUI bail-out

**Decision.** Slice 17 adds a second, opt-in terminal render mode
(`terminal_sessions.render_mode`, `'xterm'`/`'cards'`, migration 0016) that
is purely a `web/static/js/termcards.js` rendering choice — the PTY byte
stream, protocol, and runtime are unchanged. `termcards.js` line-buffers
the same bytes xterm.js would receive, waits ~150ms of idle on an
unterminated trailing line, and runs regex detectors for a yes/no prompt,
a numbered menu, and a `diff --git` block; anything unmatched is a plain
scrollback line. A single persistent input box at the pane's bottom
(rather than a separate free-text-prompt card) sends typed lines, and
picks up a detected free-text prompt as its label. The moment
cursor-repositioning/alt-screen/clear-screen sequences are seen (vim,
htop, fzf, an arrow-key menu), the session permanently falls back to
plain text — cards mode never attempts to be a terminal emulator.

**Why.** A real output-format parser per tool (e.g. Claude Code's
`--output-format stream-json`) would be the fidelity-correct approach, but
nothing in this slice's scope needs that precision yet; regex-over-plain-text
is the smallest thing that satisfies the success criteria (`ponytail`,
`docs/IMPLEMENTATION_PLAN.md` Slice 17). The single input box (instead of a
fifth "free-text input" card type, and instead of the progress/spinner
card considered during design) avoids a second input surface and a fussy
`\r`-redraw-as-in-place-update detector for a need nobody has hit yet.

**Consequence — two real bugs the Slice 17 smoke test caught, both fixed
in `termcards.js`.** (1) A PTY's `onlcr` translation (`\n` → `\r\n`) leaves
every "complete" line ending in a bare `\r` once split on `\n` alone; since
JS regex treats `\r` as a line terminator, `.` and `$` both stop short of
it, so the numbered-menu detector's `$`-anchored pattern silently matched
nothing until `write()` strips all `\r` before line-splitting. (2) `less`
(git's default diff pager) sends `ESC =` / `ESC >` (DECKPAM/DECKPNM) around
its session even when it exits immediately because the diff fits on one
screen — these are not CSI sequences, so the original `\x1b\[...` strip
regex left a stray `ESC =` in front of `diff --git `, breaking that
detector's start anchor; `ANSI_RE` now also strips bare `ESC [=>]`. Neither
bug was reachable from Go tests (this logic is JS-only, per the repo's
existing no-JS-test-harness precedent) — both were only found by driving a
real PTY (a live `read -p` menu and a real `git diff`) through the actual
running dev instance in a browser.

## ADR-036 — Help system: static Markdown + frontmatter content, real `bootstrap.Popover` via a second minimal vendor drop (not the full bundle), single dynamic icon on `project.html`

**Decision.** The new help system (`docs/WEB_UI_DESIGN.md`'s Help system
section, `docs/DESIGN_ICON_SYSTEM.md` §8) is pure static content: Markdown
files with flat (non-YAML) frontmatter under `web/static/help/*.md`,
enumerated by `web/static/help/index.json` (an id list only — titles and
summaries stay single-sourced in each file's own frontmatter, never
duplicated into the index). `web/static/js/help.js` fetches/caches/renders
both the contextual popovers (every page) and the full manual page
(`web/static/help.html`). No DB table, no API endpoint, no OpenAPI change.

Contextual popovers use the real Bootstrap `Popover` component, not a
hand-rolled widget — but not via the existing `bootstrap.bundle.min.js`
either. That bundle is already known to collide with `tabler.min.js`'s own
copy of Bootstrap's JS wherever both are loaded on the same page: ADR-034
records this happening for real, once, on `project.html` (the shared
notifications-bell/account dropdowns broke because both libraries bind
`[data-bs-toggle="dropdown"]`'s Data API). Every page except `project.html`
only loads `tabler.min.js`; loading the full bundle there too — just to get
`Popover` — would reintroduce that exact bug on all six of them. Instead,
those six pages (plus `help.html`) load only Bootstrap 5.3.8's Popover
dependency chain as separate vendored files
(`web/static/vendor/bootstrap/js/popover/`: `index.js`, `data.js`,
`manipulator.js`, `event-handler.js`, `selector-engine.js`, `config.js`,
`sanitizer.js`, `template-factory.js`, `base-component.js`, `popper.min.js`
— `@popperjs/core@2.11.8`, the exact peer version Bootstrap 5.3.8 declares
— then `tooltip.js`, `popover.js`, in that dependency order), fetched from
the same official npm/jsDelivr distribution the existing bundle came from.
These files never touch `dropdown.js`, so they can never re-trigger the
ADR-034 collision. `project.html` needs none of this — its existing
`bootstrap.bundle.min.js` (loaded for `Tab`) already includes `Popover`.
`web/static/js/help.js`'s `popoverCtor()` picks `window.bootstrap.Popover`
when present, else falls back to the flat `window.Popover` global the
individual files expose.

One wrinkle: these are UMD builds meant for bundler/AMD consumption as much
as plain `<script>` tags, and `tooltip.js`'s UMD wrapper looks up its
Popper dependency as the literal global key `globalThis["@popperjs/core"]`,
not `globalThis.Popper` (which is what Popper's own UMD build actually
sets). A one-line shim — `window['@popperjs/core'] = window.Popper;` —
bridges this before `tooltip.js` loads. It's the only non-"single vendored
file" JS dependency in the codebase; everywhere else remains one file, one
`<script src>`, matching existing convention as closely as the collision
constraint allowed.

`project.html` itself gets one dynamic help icon (`#app-help-btn`, next to
the workspace tab bar) rather than eight static ones inside each panel:
its `data-help`/`aria-label`/`title` are rewritten by the existing
`shown.bs.tab` handler to match whichever tab is active, and any
currently-open popover instance on it is hidden on tab switch so a stale
topic can never linger visibly into the new tab. This required exposing a
tiny `window.DSHelp.ensureTopics(ids)` from `help.js`, called once with all
eight tab topics up front — `initHelpIcons()` only scans `data-help` values
present in the DOM *at page load* (just the initially-active tab's topic),
so without this the popover would show "Help content unavailable." for
every tab reached by switching rather than a fresh page load (caught by
manual Playwright smoke-testing before the automated test was written).

**Why not the hand-rolled popover drafted first.** A plain fixed-position
`<div>` (mirroring the notifications-bell's own `positionPanel()`
technique) was the initial approach specifically to dodge the
dual-Bootstrap risk with zero new vendoring. Rejected on request in favor
of the real component for better focus/ARIA/Escape/outside-click/
positioning behavior — worth the extra vendored files once the Popover-only
dependency chain was confirmed not to touch Dropdown.

**Consequence.** Fourteen new script tags per non-`project.html` page (the
Popover chain + one shim line) — verbose, but consistent with this
codebase's existing style of individual `<script src>` tags with no bundler
(`project.html` alone already lists ~20). Adding a help topic touches no
code: a new `.md` file with frontmatter, its id appended to `index.json`,
and a `data-help="<id>"` icon or `[text](/help.html#<id>)` link wherever it
should be reachable from.

Verified: `gofmt`/`go vet`/full `go test -p 1 ./...` green; a new
`TestE2E_HelpSystem` (chromedp) drives a real headless browser through the
dashboard's help popover, the manual page's default topic render, and the
search box's filtering; manual Playwright smoke-testing against the
`devstationone-dev` systemd instance covered the popover open/close, the
manual page TOC/search, the tab-aware `project.html` icon across a real
tab switch, and the global `?` shortcut — all with zero browser console
errors.

## ADR-037 — Projects list is server-wide: any authenticated user may use any project; only owner/admin may modify its definition

**Decision.** `GET /api/v1/projects` returns every project on the server
(`projects.ListAll` — `ListForUser` is deleted), and `authorizedProject`
relaxes to "any authenticated user": any user may fetch a project, open
terminal sessions in it, and use its workspace (files, databases, services,
journal). Modifying a project's definition — `PATCH` and `DELETE` — gains a
new `writableProject` gate: the owner (`created_by`) or an administrator
only, still answering `404` (never `403`) so the response never confirms
another user's project. This matches the single-team self-hosted reality
the product targets (docs/PRD.md): developers share one Control Server, and
the projects page's Owner column is meaningless when each user's list is
empty of everyone else's work.

The motivation is also the page's new datatable: description, owner, and
last-accessed columns (migration 0017 adds `projects.description` and
`projects.last_accessed_at`; the latter is stamped in
`createTermSessionHandler` after a successful `session.ready`, an ignored
error — it is metadata, not state) only earn their place when the list
actually spans users. `owner_name` is resolved by a scalar subquery
(`COALESCE(NULLIF(display_name, ''), email)` — the same expression
`internal/journal` uses) appended to `projectColumns`, so every SELECT and
INSERT/UPDATE RETURNING path gets it for free.

The UI keeps the same hand-rolled pattern as `activity.html` — a text
search box plus owner/runtime filter selects plus click-to-sort headers
(`aria-sort` + CSS chevrons in `web/static/css/app.css`), vanilla JS, no
DataTables vendor — and moves the create/edit form into one Bootstrap modal
shared by "New project" (POST) and the row edit button (PATCH, pre-filled,
runtime/shell/cwd seeded from the stored project so the modal opens even
when the runtime is offline).

## ADR-038 — User manager: admin-only CRUD with self/last-admin/ownership guards, live last-accessed from auth sessions

**Decision.** The user manager is four `requireAdmin` routes
(`internal/api/users.go`: `GET/POST /api/v1/users`, `PATCH/DELETE
/api/v1/users/{userID}`) over the existing `users` table — no migration
needed. "Last accessed" is not a new column: the auth-sessions table
already bumps `last_seen_at` on every authenticated request
(`internal/auth/session.go`), so `ListAdminUsers` derives it live with
`MAX(sessions.last_seen_at)` via a `LEFT JOIN` — null until first sign-in,
and never wrong (a stored "last seen" column on `users` would go stale the
moment the write is skipped for cost).

Guard rails live in `guardAdminEdit` and answer 400 with a message: you
cannot demote or delete your own account; the **last remaining
administrator** cannot be demoted or deleted (admin count only applies
when the target is an admin — deleting a regular user with one admin
present is fine); a user who still owns projects or runtimes cannot be
deleted (the FKs have no `ON DELETE` action, so the count is checked up
front and the error names what's in the way — same "block with
explanation" model as revocation-adjacent flows). Deleting a user
cascades their auth sessions and per-user preference rows; audit rows
survive (`actor_user_id` is `ON DELETE SET NULL`).

Passwords: admin-created and admin-reset passwords require ≥8 characters
(a new trust boundary the login path never had — admin-created accounts
should not be weaker than the admin chose to make them). `AdminUpdate` is
one `COALESCE($n, col)` UPDATE rather than a dynamic SET builder — three
optional columns, one boring statement.

UI: `users.html` mirrors the projects page's datatable + shared-modal
pattern exactly (same `sort-th` chevrons, same `vendor/bootstrap/js/modal/`
drop for the Modal class — `tabler.min.js` still exposes no
`window.bootstrap`). The `Users` sidebar link is injected by the existing
`/api/v1/auth/session` fetch in `app.js` when the viewer is an admin; the
API enforces the same gate server-side, so a non-admin probing
`users.html` is redirected by its 403 handling and the data is never
served.

**Addendum (ownership transfer).** The ownership-block error from the
delete guard was a dead end without a fix, so the manager gains two more
admin routes: `GET /api/v1/users/{userID}/resources` (what the user owns,
for the users page's detail panel) and `POST
/api/v1/users/{userID}/reassign` (`{target_user_id, project_ids?,
runtime_ids?}`). The ID lists use a nil-vs-empty contract — absent field
leaves that kind untouched, empty array moves everything of that kind,
non-empty moves those IDs — which is what makes per-resource and bulk
transfers expressible in one endpoint without a second "all" flag. Go's
unmarshalling maps an absent JSON field to a nil slice, so `*[]string`
carries the distinction for free. A runtime transfer only changes
`created_by`: enrollment tokens and the live connection are unaffected
(ownership of a runtime is otherwise cosmetic). Transfers are audited as
`ownership.transferred`; the UI's ownership-blocked delete error now opens
the resources panel directly.

**Addendum (runtime deletion).** Revoke alone left no cleanup path: revoked
rows accumulate forever and cannot be reused, reconfigured, or removed. The
manager gains `DELETE /api/v1/runtimes/{runtimeID}` (requireAdmin,
ADR-039): a hard delete of the record and its enrollment token's hash —
permanent, unlike revoke. A runtime whose projects still reference it
answers `400` (the FK has no `ON DELETE`; the admin deletes or re-points
those projects first, same block-with-explanation model as the user
manager). Online status does not block: deleting drops the live agent's
connection immediately (`registry.Revoke`), and the confirm dialog names
that consequence — a dead machine that never reports offline would
otherwise be undeletable. Audited as `runtime.deleted`. The UI adds a
trash icon on every runtimes row next to revoke/reactivate, with the
confirm text varying on the online badge.

## ADR-040 — Docker tab: moby client over local socket, compose via CLI plugin, project-scoped visibility, no built-in exec

**Decision.** The project workspace gains a Docker tab backed by a new
runtime docker subsystem. The runtime talks to the daemon through
`github.com/moby/moby/client` (v0.6.x, current as of Sep 2026) over the
local unix socket — default `/var/run/docker.sock`, rootless
`~/.docker/run/docker.sock` fallback; never TCP. Compose operations shell
out to the `docker compose` CLI plugin with fixed argv (the moby client has
no compose API; docker.sock doesn't expose compose — it lives in the CLI
plugin). Visibility is project-scoped only: containers belonging to the
detected compose project (`com.docker.compose.project` label) plus
containers whose mounts bind the project working directory; dev-infra
containers outside that scope are invisible by design. The compose file is
auto-detected in the project working directory (containment-checked, no
project setting). Container logs stream with `docker logs -f` semantics
over WebSocket, work on stopped containers, and stop when the tab closes.
Compose up/down is a bounded RPC with an extended timeout (10 min)
returning final output — no live build streaming. Container exec is
excluded: the Terminal tab is already a PTY in the project directory where
`docker exec -it` works today; built-in exec would add a protocol session
type, ExecAttach plumbing, and container-ID scope validation for one saved
keystroke. Images/volumes/networks are read-only lists, filtered to
resources referenced by in-scope containers.

**Why.** Local-socket-only matches the runtime's security posture (no
network-exposed docker); the socket grant itself is the documented trust
boundary — docker-group membership is host-root-equivalent, so SECURITY.md
gets an explicit deployment prerequisite entry. Project-scoped visibility
keeps the tab honest to its context; the filter is one label check to relax
later if it bites. CLI-plugin compose gets full v2 semantics (build, env
interpolation, profiles) for free — reimplementing compose in Go against
raw container APIs is a known tar pit. Logs streaming reuses the existing
WS transport patterns; polling was rejected as worse UX for watching a dev
server boot.

**Consequence.** Runtime user needs docker socket access — a documented
prerequisite, not a code concern. The socket path is standard-only (no
config surface): a socket living elsewhere (podman, `DOCKER_HOST`) simply
reports not-available, and a runtime config field is the upgrade path if
that ever bites. A runtime host without Docker reports it
via `protocol.Capabilities` (new `docker` field: socket reachable, daemon
version, compose plugin present) and the tab renders a not-available empty
state. Compose output arrives only at completion; live progress is the
upgrade path if slow builds annoy (`ponytail:` marker). Exec stays out
until someone actually asks.
