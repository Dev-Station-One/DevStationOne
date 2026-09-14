# DevStationOne API

Base path:
```text
/api/v1
```

OpenAPI source:
```text
docs/openapi.yaml
```

## Resource groups

```text
/auth
/users
/devices
/runtimes
/projects
/sessions
/files
/databases
/services
/audit
/settings
```

## Runtimes

```text
POST   /api/v1/runtimes
GET    /api/v1/runtimes
POST   /api/v1/runtimes/{runtimeID}/revoke

GET    /api/v1/runtimes/{runtimeID}/browse
GET    /api/v1/runtimes/{runtimeID}/browse/search
POST   /api/v1/runtimes/{runtimeID}/browse/mkdir

WSS    /api/v1/runtimes/connect
```

`POST /api/v1/runtimes` (register) and the `revoke` action require an
authenticated administrator session. `GET /api/v1/runtimes` (list) only
requires an authenticated session, since a non-admin user needs it to select
a runtime when creating a Project (see Projects below). `POST
/api/v1/runtimes` returns the created runtime plus its raw enrollment token;
the token is shown exactly once and only its SHA-256 hash is stored.

`GET /api/v1/runtimes/connect` is where a Development Runtime opens its
outbound WebSocket connection, authenticating with `Authorization: Bearer
<enrollment token>` instead of a session cookie. See `docs/PROTOCOL.md` for
the message envelope exchanged on that connection.

`GET .../browse`, `GET .../browse/search`, and `POST .../browse/mkdir` back
the Project setup UI's directory picker — used before a Project exists, so
there is no Project working directory to root the browse in. `.../search`
is a directories-only name-substring search over the subtree rooted at
`path` (`dirs_only: true` on the `file.command` RPC), bounded by the same
depth/match caps as `/files/search`. All three require only an authenticated
session (same visibility as `GET /api/v1/runtimes`: runtimes are shared
infrastructure, not owned per-user) and issue a `file.command` RPC to the
runtime with `root` set to the runtime's reported home directory
(`capabilities.home_dir`) instead of a Project's working directory — the
same RPC, and the same runtime-side `os.Root` containment, that the Files
endpoints use (see Files below and `docs/PROTOCOL.md`). `409` if the runtime
is offline or hasn't reported a home directory yet (a runtime built before
this reported nothing here).

## Projects

```text
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{projectID}
PATCH  /api/v1/projects/{projectID}
DELETE /api/v1/projects/{projectID}

GET    /api/v1/projects/{projectID}/sessions
POST   /api/v1/projects/{projectID}/sessions
```

CRUD requires an authenticated session; the session sub-resource routes
above are implemented in Slice 4 (see Sessions below). CRUD is scoped by
ownership (ADR-013):
administrators see and manage every project, other users only ones they
created (`created_by`). A project the caller does not own responds `404`,
the same as one that does not exist, so the response never confirms another
user's project.

A Project has no `os_user` field. A runtime always runs as one fixed OS user
(ADR-014), reported live as `capabilities.os_username` on the runtime
resource — it is never client-supplied. Terminal sessions source it from
there at open time (see Sessions below), not from the Project.

Create and update (`POST`/`PATCH`) validate against the target runtime's
live state, re-checked on every request (including a `PATCH` that only
renames the project — the runtime-offline gate applies to any edit, not only
ones touching runtime-derived fields):
- `runtime_id` must name an existing, non-revoked runtime that is currently
  connected and has reported a home directory (`400` not-found/revoked,
  `409` offline or no home directory yet).
- `shell` must be one of the runtime's currently-reported
  `capabilities.shells` (`400` otherwise) — parsed from `/etc/shells` on the
  runtime host; a client cannot submit an arbitrary shell path.
- `working_directory` must resolve inside the runtime's
  `capabilities.home_dir` and actually exist there. The server does a cheap
  `filepath.Rel` early-rejection, then confirms with a real `stat`
  `file.command` RPC to the runtime — the same `os.Root` containment the
  Files endpoints use, not a second path-safety implementation (`400` if
  outside the home directory or missing).

## Sessions

```text
GET    /api/v1/projects/{projectID}/sessions
POST   /api/v1/projects/{projectID}/sessions

GET    /api/v1/sessions/{sessionID}
PATCH  /api/v1/sessions/{sessionID}
DELETE /api/v1/sessions/{sessionID}

POST   /api/v1/sessions/{sessionID}/attach
POST   /api/v1/sessions/{sessionID}/detach
POST   /api/v1/sessions/{sessionID}/resize
POST   /api/v1/sessions/{sessionID}/signal

WSS    /api/v1/sessions/{sessionID}/stream
```

A Session is a persistent, real PTY (ADR-006) running on the Project's
runtime. All routes require an authenticated session and are authorized the
same way as Projects (`authorizedTermSession` resolves the session's
Project and applies the same ownership check as `authorizedProject`,
`404` on mismatch — ADR-013).

`POST /api/v1/projects/{projectID}/sessions` creates the row, then
synchronously sends `session.open` to the Project's runtime over its
existing control-plane connection (Slice 2) and waits up to 10s for
`session.ready`/`session.error`. Responses:
- `201` — the session started; `status` is `detached` (alive, unattached).
- `409` — the Project's runtime is not currently connected.
- `502` — the runtime rejected session.open (e.g. `os_user` does not match
  the runtime's actual OS user — see ADR-014) or the command could not be
  dispatched.
- `504` — the runtime did not respond within the timeout.

Status values (`docs/PROTOCOL.md` lifecycle, plus `failed`):
`requested`, `starting`, `running` (attached), `detached` (alive,
unattached), `exited`, `failed`, `closed`.

`PATCH` renames a session (`{"name": "..."}`) — it is the only mutable
field; working directory/shell/environment come from the Project and
cannot be overridden per-session.

`DELETE` sends `session.close` to the runtime (best-effort — it also
succeeds if the runtime is offline) and marks the session `closed`. This
is a terminal state: a closed session's row is kept for history but can no
longer be attached to.

`POST .../resize` `{"cols": int, "rows": int}` and `POST .../signal`
`{"signal": "SIGINT"|"SIGTERM"|"SIGKILL"|"SIGHUP"|"SIGQUIT"|"SIGTSTP"|"SIGCONT"}`
both require the runtime to be online (`409` if not) and forward directly
to it; resize additionally persists the new size. Signal is for explicit
process control (e.g. a "Force stop" UI action) — normal input, including
Ctrl+C/Ctrl+D as bytes, goes through the WSS stream instead.

`POST .../attach` and `POST .../detach` are control-plane conveniences,
not the attach mechanism itself: opening the WSS stream is what attaches a
browser (and closing it detaches). `attach` just confirms the session is
still live before a client opens the stream (`409` if `exited`/`failed`/
`closed`); `detach` forcibly disconnects whichever browser is currently
attached (e.g. so another device can take over) without closing the
session.

`WSS .../stream` is the terminal byte stream. Opening it attaches the
caller (marking the session `running`) and immediately replays up to 256KB
of recent output (Hub scrollback) so a reattach isn't a blank screen;
closing it marks the session `detached` if the process is still alive.
Messages are **binary-only** WebSocket frames carrying raw terminal
bytes in both directions — no JSON envelope, matching `docs/PROTOCOL.md`'s
binary-safety requirement. `409` (not upgraded) if the session is not in a
live status or the runtime is offline.

## Files

All file operations are live RPCs to the Project's runtime, scoped to the
Project's working directory (the authorized root). Every path argument is
**root-relative** (`.` is the root itself); absolute paths and `..`
traversal are rejected with `400` before dispatch, and the runtime
re-checks containment independently via `os.Root` (symlink escapes included)
and replies `502`. The runtime is offline → `409`; RPC timeout → `504`;
oversized transfer (> 24 MB) → `413` on upload, `502` on read.

| Operation | Endpoint |
|---|---|
| list | `GET /api/v1/projects/{projectID}/files?path=` |
| stat | `GET /api/v1/projects/{projectID}/files/stat?path=` |
| read | `GET /api/v1/projects/{projectID}/files/content?path=` |
| download | same as read with `&download=1` (Content-Disposition attachment) |
| write / upload | `PUT /api/v1/projects/{projectID}/files/content?path=` (raw body = file bytes) |
| create | `POST /api/v1/projects/{projectID}/files/create` `{"path"}` — fails if it exists |
| mkdir | `POST /api/v1/projects/{projectID}/files/mkdir` `{"path"}` — fails if it exists |
| rename | `POST /api/v1/projects/{projectID}/files/rename` `{"from","to"}` — replaces destination |
| copy | `POST /api/v1/projects/{projectID}/files/copy` `{"from","to"}` — fails if destination exists; directories copied recursively |
| move | `POST /api/v1/projects/{projectID}/files/move` `{"from","to"}` — same-filesystem rename |
| delete | `DELETE /api/v1/projects/{projectID}/files?path=` — recursive for directories |
| search | `GET /api/v1/projects/{projectID}/files/search?q=&path=` — case-insensitive name substring, depth/match capped |

The canonical request/response shapes are `docs/openapi.yaml` (tag `Files`).
HTML/SVG content is always served with an attachment disposition so it can
never render as a document on the DevStationOne origin.

## Databases

```text
POST   /api/v1/projects/{projectID}/databases
GET    /api/v1/projects/{projectID}/databases
GET    /api/v1/projects/{projectID}/databases/{connectionID}
PATCH  /api/v1/projects/{projectID}/databases/{connectionID}
DELETE /api/v1/projects/{projectID}/databases/{connectionID}
POST   /api/v1/projects/{projectID}/databases/{connectionID}/test
```

Slice 7 scope: connection CRUD, protected credentials, and connection test
against all four supported engines (PostgreSQL, MySQL/MariaDB, Microsoft SQL
Server, SQLite). Slice 8 adds schema discovery (schemas/tables/columns/
indexes/constraints/triggers), Slice 9 adds data browsing and the SQL
editor, and Slice 10 adds user/role/permission management — all documented
separately below.

A connection is a stored `database_connections` row scoped to a Project
(`project_id`), authorized the same way as everything else under
`/projects/{projectID}/...` (`authorizedProject`, ADR-013): `404` on a
project the caller doesn't own or that doesn't exist.

Request/response body (`database_connections`' non-credential fields):
```json
{
  "name": "primary",
  "engine": "postgresql",
  "host": "localhost",
  "port": 5432,
  "username": "dev",
  "database": "app",
  "ssl_mode": "disable",
  "instance_name": "",
  "search_path": "",
  "has_secret": true
}
```
`engine` is one of `postgresql`, `mysql`, `mssql`, `sqlite`. For `sqlite`,
`database` is a runtime-local file path and `host`/`port`/`username`/
`ssl_mode` are ignored. `instance_name` (optional) is an MSSQL named
instance; `search_path` (optional) is a Postgres default schema.

Credentials are never returned. `POST`/create accepts `password` and
`extra_params` (a free-form `key=value&key2=value2` string passed through to
the engine's connection string — an escape hatch for anything the named
fields don't cover) alongside the fields above; `has_secret` in the response
reports whether a secret is stored, nothing more. `PATCH` only touches the
stored secret when the request includes a `"secret": {"password",
"extra_params"}` object — omitting it (the normal case: editing the name,
host, etc.) leaves the existing encrypted credential untouched. This is how
the edit form's "leave password blank to keep the existing one" behavior
works: the UI only sends `secret` when the user actually typed a new
password.

Credentials are encrypted at rest with AES-256-GCM under
`DEVSTATIONONE_DB_ENCRYPTION_KEY` (see `docs/SECURITY.md` and ADR-021) —
`internal/dbconnections` is the only package that ever decrypts one, and only
to build the `db.command` RPC payload dispatched to a runtime; the plaintext
never appears in an API response or a log line.

`POST .../test` is a live RPC (`db.command` action `test`,
`docs/PROTOCOL.md` "Implementation (Slice 7/8): database commands") to the
Project's runtime, which opens (or reuses a cached, already-open pool — see
below) and pings the connection using `internal/dbconnectors`. Response is
always `200` with `{"ok": true}` or `{"ok": false, "error": "..."}`; a
failed connection (bad credentials, unreachable host) is an expected
outcome, not a server error. `409` if the runtime isn't currently
connected; `504` if it doesn't reply within 30s.

### Schema discovery (Slice 8)

```text
GET /api/v1/projects/{projectID}/databases/{connectionID}/schemas
GET /api/v1/projects/{projectID}/databases/{connectionID}/tables?schema=
GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/columns?schema=
GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/indexes?schema=
GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/constraints?schema=
GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/triggers?schema=
```

Every schema-discovery endpoint is a live RPC, same authorization and
offline/timeout status codes as `test` above. `schema` is a real schema for
PostgreSQL/MSSQL, the connection's single database name for MySQL/MariaDB
(no separate schema concept there), and omitted entirely for SQLite (no
schema concept at all) — the UI skips the schema level of the tree for
those two engines rather than showing a pointless single-item node.

`GET .../schemas` → `["public", ...]`.

`GET .../tables` → `[{"name": "users", "kind": "table"}, ...]` — `kind` is
`"table"` or `"view"`.

`GET .../tables/{table}/columns` → `[{"name", "type", "nullable",
"default", "position"}, ...]`, ordered by position. `type` is the engine's
own native type name (not normalized across engines).

`GET .../tables/{table}/indexes` → `[{"name", "columns": [...], "unique",
"primary"}, ...]`.

`GET .../tables/{table}/constraints` → `[{"name", "type", "columns":
[...], "references"}, ...]`. `type` is `primary_key`, `foreign_key`,
`unique`, or `check`. `references` is `"schema.table(column)"` for a
`foreign_key`, omitted otherwise.

`GET .../tables/{table}/triggers` → `[{"name", "event", "timing"}, ...]`.
`event`/`timing` are best-effort — SQLite in particular only exposes a
trigger's name without parsing its SQL text, so both are omitted for it.

**Connection pool cache.** The runtime opens at most one `*sql.DB` per
`connectionID` and reuses it across every RPC against that connection —
opening a fresh connection (and TLS handshake, for PostgreSQL/MSSQL) on
every tree-node expand would be needlessly slow. An idle pool is closed
after 5 minutes; editing or deleting a connection's stored credentials
immediately invalidates its cached pool (see ADR-021/022) so a changed
password takes effect on the very next request, not after the idle timeout.

### Data browsing and the SQL editor (Slice 9)

```text
GET   /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows?schema=&limit=&offset=&sort=&sort_desc=&filter=
PATCH /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows?schema=
POST  /api/v1/projects/{projectID}/databases/{connectionID}/query
```

All three are live RPCs, same authorization and offline/timeout status
codes as the rest of this section.

`GET .../rows` returns one page: `{"columns": [...], "rows": [[...], ...],
"has_more": bool, "is_exec": false, "rows_affected": 0}`. Each row is an
array of values in `columns` order. `limit` defaults to 100, capped at
5000 (validated by both the Control Server and, independently, the
runtime — defense in depth, `docs/SECURITY.md`). `sort` is a column name
(quoted as an identifier by the runtime, never interpolated raw);
`sort_desc=true` reverses it. `filter`, if given, is **a raw SQL boolean
expression appended after `WHERE`** — not a parameterized value. This is a
deliberate choice, not an oversight: it mirrors the SQL Editor's own
arbitrary-execution trust model (ADR-021) rather than inventing a
structured filter-expression language for what is already the user's own
database, reachable through the SQL Editor regardless.

`PATCH .../rows` commits staged edits: `{"edits": [{"op": "insert",
"values": {...}}, {"op": "update", "original": {...}, "values": {...}},
{"op": "delete", "original": {...}}]}`. `original` is the row's column
values as most recently fetched by `GET .../rows` — the runtime uses it to
build the `WHERE` clause locating the row, as **bound parameters**, never
interpolated (unlike `filter` above). Every column value in `values`/
`original` is likewise always bound, regardless of the raw-SQL `filter`/
SQL-Editor exceptions elsewhere in this endpoint group. Response:
`{"ok": true}`.

`POST .../query` runs `{"sql": "..."}` verbatim — arbitrary SQL, the SQL
Editor's intentional parallel to the terminal (`docs/SECURITY.md`
"terminal sessions provide arbitrary code execution by design"; ADR-021
extends the same posture here). A simple keyword sniff
(INSERT/UPDATE/DELETE/CREATE/DROP/ALTER/TRUNCATE/GRANT/REVOKE/REPLACE)
decides whether to run it via Exec (reporting `is_exec: true,
rows_affected`) or Query (reporting `columns`/`rows`, capped at `limit`
the same way as `GET .../rows`). A misclassified statement just produces
an error from the wrong call — not a security issue, since both paths run
the identical trusted-arbitrary-SQL text either way.

The canonical request/response shapes are `docs/openapi.yaml` (tag
`Databases`).

### User/role/permission management (Slice 10)

```text
GET    /api/v1/projects/{projectID}/databases/{connectionID}/principals
POST   /api/v1/projects/{projectID}/databases/{connectionID}/principals
DELETE /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}
PATCH  /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}
GET    /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants
POST   /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants
POST   /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants/revoke
```

All seven are live RPCs, same authorization and offline/timeout status
codes as the rest of this section, with one addition: every endpoint here
returns `400` for a `sqlite` connection before ever reaching the runtime —
SQLite has no server-side concept of users, roles, or grants (PRD §6: "SQLite
does not have server-side users/roles; represent that honestly"), so the UI
hides the Users tab for a SQLite connection and there is nothing for the
runtime to be asked about.

A `principal` is a database-level user, login, or role — `{"name",
"can_login", "superuser"}`. For MySQL/MariaDB, `name` is `"user@host"`
(MySQL keys an account by the pair; a bare name on create defaults `host` to
`"%"`, any host). `can_login`/`superuser` map to each engine's closest native
concept: Postgres `LOGIN`/`NOLOGIN` and `SUPERUSER`/`NOSUPERUSER` roles
directly; MySQL has no `NOLOGIN` equivalent so `can_login` is always
reported `true`, and `superuser` means `GRANT ALL PRIVILEGES ON *.* ...
WITH GRANT OPTION`; MSSQL creates a paired server login + database user
(`CREATE LOGIN` + `CREATE USER ... FOR LOGIN`), `can_login: false` disables
the login (`ALTER LOGIN ... DISABLE`), and `superuser` adds the login to the
`sysadmin` server role.

`GET .../principals` → `[{"name", "can_login", "superuser"}, ...]`.

`POST .../principals` creates one: `{"name", "password", "can_login",
"superuser"}`. `201` on success.

`DELETE .../principals/{principal}` drops it. `204` on success.

`PATCH .../principals/{principal}` sets a new password: `{"password"}`.
Response `{"ok": true}`.

`GET .../principals/{principal}/grants` → `[{"privilege", "schema",
"table"}, ...]` for an engine with a structured per-privilege view
(PostgreSQL, MSSQL), or `[{"raw": "GRANT ... "}, ...]` for MySQL, whose only
catalog-agnostic way to list an account's grants is its own `SHOW GRANTS`
statement text — the UI renders `raw` as-is and doesn't offer a `Revoke`
button next to it (there's no single structured privilege to revoke). Only
table-level grants are covered — database/server-level grants (Postgres
`CONNECT`, MSSQL server roles, etc.) are out of scope, matching this app's
per-table granularity everywhere else (the data browser, `POST .../grants`
below).

`POST .../principals/{principal}/grants` grants one privilege on one table:
`{"privilege", "schema", "table"}`. `privilege` is validated against a fixed
whitelist (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `ALL`) independently by
both the Control Server request-shape check and the runtime connector
(`docs/DECISIONS.md` ADR-024) — unlike a row value, a privilege keyword
can't be a bound parameter in `GRANT`/`REVOKE` syntax, so it's checked
against a fixed set instead, the same trust boundary as a quoted
identifier. Response `{"ok": true}`.

`POST .../principals/{principal}/grants/revoke` takes the same body and
reverses it.

The canonical request/response shapes are `docs/openapi.yaml` (tag
`Databases`).

## Services

```text
GET  /api/v1/projects/{projectID}/services
GET  /api/v1/projects/{projectID}/services/{unit}
GET  /api/v1/projects/{projectID}/services/{unit}/logs
POST /api/v1/projects/{projectID}/services/{unit}/start
POST /api/v1/projects/{projectID}/services/{unit}/stop
POST /api/v1/projects/{projectID}/services/{unit}/restart
POST /api/v1/projects/{projectID}/services/{unit}/enable
POST /api/v1/projects/{projectID}/services/{unit}/disable
```

Services are `systemd --user` units on the Project's runtime (PRD §7). They
are not a stored resource — every request is a live RPC to the runtime
(`internal/services`, real `systemctl`/`journalctl` invocations, no root)
over its existing control-plane connection (see `docs/PROTOCOL.md`
"Implementation: service commands"), so there is no service table or
create/delete route. Authorization reuses `authorizedProject` (ADR-013):
`404` on a project the caller does not own, `409` if the Project's runtime
is not currently connected.

`{unit}` must match `^[A-Za-z0-9:_.@-]+\.service$` (`services.ValidUnitName`)
— checked server-side before ever dispatching to the runtime; `400` if it
doesn't.

`GET .../services` lists every unit the runtime's systemd --user instance
knows about: both currently-loaded units and installed-but-not-loaded ones
merged together, since systemd unloads a stopped unit from its live table
soon after nothing references it (confirmed against a real systemd --user
instance — a project's own service would otherwise disappear from the list
the moment it's stopped). A bare template unit (`app@.service`, no
instance) is excluded — it isn't independently startable.

`GET .../services/{unit}` returns one unit's current status; `404`-shaped
as `502` (bad gateway) if the runtime reports it not found, since "unit
doesn't exist" is the runtime's answer, not the Control Server's.

`.../logs?lines=N` returns the unit's most recent journal lines (`journalctl
--user -u {unit} -n N`, default 200, capped at 2000). An empty array is a
normal result, not an error — it also covers a machine with no per-user
journal namespace at all, which `journalctl --user` reports as "No journal
files were found" rather than failing.

Response shape for every unit-returning endpoint:
```json
{
  "unit": "my-app.service",
  "description": "My App",
  "load_state": "loaded",
  "active_state": "active",
  "sub_state": "running",
  "enabled": "enabled"
}
```
`enabled` is `"enabled"`, `"disabled"`, `"static"` (has no `[Install]`
section — enable/disable are meaningless for it), or `"unknown"`.

## Errors

Use consistent machine-readable errors with:
- HTTP status
- stable error code
- human message
- request/correlation ID where appropriate

Never return secrets in errors.

## Authentication

Browser authentication: server-side session, identified by a random 256-bit token stored in an `HttpOnly`, `SameSite=Lax` cookie. The cookie carries the `Secure` attribute only when the request arrived over TLS (directly or via `X-Forwarded-Proto: https`), because browsers drop `Secure` cookies on plain-`http` origins other than localhost. The server stores only a SHA-256 hash of the token, never the raw value. Sessions expire 7 days after creation and are individually revocable (`/api/v1/auth/sessions`). See `docs/openapi.yaml` for the concrete contract.

User preferences (docs/REQUIREMENTS_NERD_FONT.md): read through
`GET /api/v1/auth/session` (`terminal_font`: `system` | `jetbrainsmono-nerd`;
`theme`: `dark` | `light`), updated with a partial `PUT
/api/v1/auth/preferences` (`{"terminal_font": ...}` and/or `{"theme": ...}`)
→ `204`. Per-account, server-stored. Theme applies the
`docs/BOOTSTRAP_COLORS.md` dark workstation palette (default) or its light
variant; `web/static/css/app.css` + `web/static/js/app.js` carry the
tokens and the toggle. UI design deviations from the docs are tracked in
`docs/UI_COMPLIANCE.md`.

Runtime device authentication: a bearer token issued at enrollment (`POST
/api/v1/runtimes`), presented as `Authorization: Bearer <token>` on the
`/api/v1/runtimes/connect` WebSocket upgrade. The server stores only a
SHA-256 hash of the token, the same pattern as browser sessions.
