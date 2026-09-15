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
/events
/editor
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
POST   /api/v1/runtimes/{runtimeID}/reactivate
DELETE /api/v1/runtimes/{runtimeID}

GET    /api/v1/runtimes/{runtimeID}/browse
GET    /api/v1/runtimes/{runtimeID}/browse/search
POST   /api/v1/runtimes/{runtimeID}/browse/mkdir

WSS    /api/v1/runtimes/connect
```

`POST /api/v1/runtimes` (register), `revoke`, and `reactivate` all require an
authenticated administrator session. `GET /api/v1/runtimes` (list) only
requires an authenticated session, since a non-admin user needs it to select
a runtime when creating a Project (see Projects below). `POST
/api/v1/runtimes` returns the created runtime plus its raw enrollment token;
the token is shown exactly once and only its SHA-256 hash is stored.

`POST .../reactivate` returns a revoked runtime to `pending` status and
clears `revoked_at` — it does not issue a new token, so the runtime machine
needs no reconfiguration and can reconnect as soon as it retries (or is
restarted). `404` if `runtimeID` doesn't exist. Calling it on a runtime that
isn't currently revoked still succeeds (idempotent), forcing its status to
`pending` regardless.

`DELETE /api/v1/runtimes/{runtimeID}` removes the record entirely —
permanently, unlike revoke: the row and its enrollment token's hash are
gone, and a live agent's connection is dropped immediately (the agent can
no longer reconnect). A runtime whose projects still reference it answers
`400` — delete or re-point those projects first. Administrator-only;
audited as `runtime.deleted`.

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
above are implemented in Slice 4 (see Sessions below). Access is
server-wide (ADR-013): every authenticated user sees every project, and may
open sessions and use the workspace. Modifying a project's definition
(`PATCH`/`DELETE`) stays restricted to its owner (`created_by`) or an
administrator; a project the caller may not modify responds `404`, the same
as one that does not exist, so the response never confirms another user's
project.

Project responses include `description` (optional human-readable summary),
`owner_name` (the owner's display name with an email fallback), and
`last_accessed_at` (stamped when a terminal session is last opened on the
project; `null` if never). Project fields accept and return a
`description`.

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

## Dashboard events

```text
WSS    /api/v1/events/stream
```

`GET /api/v1/projects` and `GET /api/v1/projects/{projectID}` include
`active_sessions`: a count of that project's terminal sessions not in a
terminal status (`exited`/`failed`/`closed`). It is computed at request
time, not cached.

`WSS /api/v1/events/stream` is a live push channel for the dashboard,
authenticated the same way as any other browser route (session cookie). It
carries JSON text frames — unlike the binary-only terminal stream below —
shaped `{"type": "...", "payload": {...}}`:
- `runtime.status` — `{"runtime_id": "...", "online": bool}`. Broadcast to
  every connected browser: runtimes are shared infrastructure, not owned
  per-user (same visibility as `GET /api/v1/runtimes`).
- `project.status` — `{"project_id": "..."}`, sent only to that project's
  owner, fired whenever one of its sessions' activity changes (attach,
  detach, or exit). The payload deliberately carries no session count —
  the caller is expected to refetch `GET /api/v1/projects` on receipt
  rather than trust a stale count on a message that may have coalesced
  several changes.
- `ping` — no payload, sent every 30s on an otherwise idle connection to
  keep intermediate proxies from timing it out.
- `notification.created` — a full notification object (see "Notifications"
  below), sent only to the notification's own user. Unlike the other event
  types, the receiving browser can render it directly (update the bell
  badge/dropdown) without a REST refetch, since a notification's fields
  never change after creation other than `read_at`.

The bus is in-memory and scoped to a single Control Server process, the
same limitation already documented on the runtime registry and terminal
session hub (ADR-012) — a multi-instance deployment would need a shared
broker.

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
Project through `authorizedProject` — any authenticated user may use any
project's sessions, `404` on unknown project). Opening a session stamps the
project's `last_accessed_at`.

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

`render_mode` (`"xterm"` default, or `"cards"` — Slice 17) selects the
client rendering mode at creation and is immutable afterward; switching
means opening a new session. An invalid value is a `400`. It round-trips
through the normal session-info response, so a reattach from another
device (the existing `GET`/`attach` flow) picks it up without a dedicated
endpoint.

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
| chmod | `PATCH /api/v1/projects/{projectID}/files/mode` `{"path","mode","recursive"}` — `mode` is an octal string, e.g. `"755"`; only owner/group/other rwx bits are settable |
| chown | `PATCH /api/v1/projects/{projectID}/files/owner` `{"path","owner","group","recursive"}` — `owner`/`group` are account/group names from `RuntimeCapabilities.users`/`.groups`, either omittable |

The canonical request/response shapes are `docs/openapi.yaml` (tag `Files`).
HTML/SVG content is always served with an attachment disposition so it can
never render as a document on the DevStationOne origin.

`chown`'s `owner` almost always fails with `502` on a real deployment: the
runtime never runs as root (ADR-014), and POSIX only lets root (or
`CAP_CHOWN`) reassign a file's owning user. `group` succeeds for any group
the runtime's OS user belongs to. See ADR-032.

## Editor (Code tab)

The Code tab's live editing uses a project-scoped WebSocket stream plus a
REST pair for the signed-in user's persisted editor tab state.

| endpoint | purpose |
|---|---|
| stream | `GET /api/v1/projects/{projectID}/editor/stream` — cookie-authenticated WebSocket (same authorization as the files endpoints: session + the project's runtime online) |
| get state | `GET /api/v1/projects/{projectID}/editor/state` → `{"state": {...}}` (`{}` when nothing stored) |
| put state | `PUT /api/v1/projects/{projectID}/editor/state` ← `{"state": {...}}` — opaque JSON object, 256 KiB cap, 204 |

Browser→server stream messages (JSON text frames):
```text
{"type":"open","path":"src/main.go"}
{"type":"watch","paths":["src/main.go","app.js"]}
{"type":"save","path":"src/main.go","content":"<whole buffer>","base_mtime":"RFC3339","base_size":1234}
```

Server→browser stream messages:
```text
{"type":"content","path","mtime","size","data"}   open reply or external-change push
{"type":"saved","path","mtime","size"}            clean save
{"type":"stale","path","mtime","size","data"}     save whose base fingerprint no longer matched; `data` is the overwritten on-disk content
{"type":"deleted","path"}                          a watched file vanished
{"type":"error","path","error"}                    open/binary/oversize/save failure
```

Semantics (ADR-033, details in docs/PROTOCOL.md): the client autosaves the
whole buffer ~300ms after the last keystroke; saves are last-write-wins
with the pre-write content returned on a stale save; external changes to
open files arrive as whole-file `content` pushes (detected by the runtime's
1-second mtime+size poll). Files above 2 MiB (`MaxEditorFileBytes`) and
binary files are refused with an `error` message — open them from the
Files tab, which carries up to 24 MiB.

## Git

Git operations are live RPCs to the Project's runtime (same shape as
Files), executing the real `git` CLI directly (`os/exec`, no shell) in the
Project's working directory — reusing whatever `git`, SSH keys, and
credential helper are already configured for the runtime's OS user
(`docs/DECISIONS.md` ADR-028).

| Operation | Endpoint |
|---|---|
| status | `GET /api/v1/projects/{projectID}/git/status` — changed-path list |
| diff | `GET /api/v1/projects/{projectID}/git/diff?path=&staged=` — unified diff text; `staged=1` diffs the index against HEAD |
| stage | `POST /api/v1/projects/{projectID}/git/stage` `{"path"}` |
| unstage | `POST /api/v1/projects/{projectID}/git/unstage` `{"path"}` |
| commit | `POST /api/v1/projects/{projectID}/git/commit` `{"message"}` — replies `{"commit": "<sha>"}` |
| log | `GET /api/v1/projects/{projectID}/git/log?limit=` — recent commits (SHA, subject, author, datetime), newest first; feeds the journal commit picker. A repo with no commits yet is a 502 |

`status`/`diff` are scoped to the Project's working directory with a git
pathspec (`-- .` or the given path) even when it is a subdirectory of a
larger git checkout, so a repo rooted above it can't leak paths outside the
Project into the result. Staging/committing content from outside the
Project's working directory (already staged there by something other than
DevStationOne, in that same larger-checkout scenario) is a known
limitation — see ADR-028.

The canonical request/response shapes are `docs/openapi.yaml` (tag `Git`).

## Journal

Developer notes on a Project (`docs/JOURNAL_REQUIREMENTS.md`). Rich-text
HTML bodies (Summernote), optional captured git-commit association, and
attachments (25 MB / 10 per note) stored Control-Server-side. Note HTML
is untrusted — never rendered without DOMPurify; the list endpoint
carries an HTML-stripped snippet, never the full body. Any user with
project access may create, edit, or delete notes; edits preserve
creator/creation time and bump `updated_at` (the UI shows an "Edited"
marker). Deletion cascades to attachments.

```text
GET    /api/v1/projects/{projectID}/journal?limit=&offset=   — server-side page + total
POST   /api/v1/projects/{projectID}/journal                  — create
GET    /api/v1/projects/{projectID}/journal/notes/{noteID}
PUT    /api/v1/projects/{projectID}/journal/notes/{noteID}
DELETE /api/v1/projects/{projectID}/journal/notes/{noteID}   — cascades to attachments
POST   /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments    — multipart "file"
GET    /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments/{attachmentID}
```

The canonical request/response shapes are `docs/openapi.yaml` (tag
`Journal`).

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
GET  /api/v1/runtimes/{runtimeID}/services
GET  /api/v1/runtimes/{runtimeID}/services/{unit}
GET  /api/v1/runtimes/{runtimeID}/services/{unit}/logs
POST /api/v1/runtimes/{runtimeID}/services/{unit}/start
POST /api/v1/runtimes/{runtimeID}/services/{unit}/stop
POST /api/v1/runtimes/{runtimeID}/services/{unit}/restart
POST /api/v1/runtimes/{runtimeID}/services/{unit}/enable
POST /api/v1/runtimes/{runtimeID}/services/{unit}/disable
```

Services are `systemd --user` units on a runtime host (PRD §7) — runtime-level
resources (ADR-043), not scoped to any one Project. They are not a stored
resource — every request is a live RPC to the runtime (`internal/services`,
real `systemctl`/`journalctl` invocations, no root) over its existing
control-plane connection (see `docs/PROTOCOL.md` "Implementation: service
commands"), so there is no service table or create/delete route.
Authorization reuses `resolveRuntime` (the same lookup the Runtimes area and
the directory browser use): `400` on an unknown or revoked runtime, `409` if
it is not currently connected.

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

## Backups

Database backups (per connection):
```text
GET    /api/v1/projects/{projectID}/databases/{connectionID}/backups
POST   /api/v1/projects/{projectID}/databases/{connectionID}/backups
GET    /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule
PUT    /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule
DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule
GET    /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}
DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}
GET    /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/download
POST   /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/restore
```

Project backups:
```text
GET    /api/v1/projects/{projectID}/backups
POST   /api/v1/projects/{projectID}/backups
GET    /api/v1/projects/{projectID}/backups/schedule
PUT    /api/v1/projects/{projectID}/backups/schedule
DELETE /api/v1/projects/{projectID}/backups/schedule
GET    /api/v1/projects/{projectID}/backups/{backupID}
DELETE /api/v1/projects/{projectID}/backups/{backupID}
GET    /api/v1/projects/{projectID}/backups/{backupID}/download
POST   /api/v1/projects/{projectID}/backups/{backupID}/restore
```

Backup files live only on the runtime host, under a fixed root
(`~/.local/share/devstationone/backups/{db,projects}/`) — never on Control
Server disk (ADR-041). The Control Server stores metadata rows (Postgres
`backups`/`backup_schedules`) and proxies downloads by issuing chunked
`read` RPCs to the runtime and streaming the bytes straight to the browser.

`POST .../backups` (both kinds) starts the dump/archive on the runtime and
returns immediately with the row in `state: "running"` and a runtime-side
job ID; the runtime completes it in the background and the row transitions
to `"done"` or `"failed"` once the Control Server's poller observes the
job's completion (`ADR-042`). Clients re-fetch the list to see the update —
there is no push notification for this yet.

Database restore (`POST .../backups/{backupID}/restore`) takes
`{"target_database": "...", "confirm": true}`: it restores into any database
on the same connection, including the original; `confirm` must be `true` —
a server-side gate independent of the UI, since restoring into an existing
database replaces its data.

Project restore (`POST .../backups/{backupID}/restore`) takes
`{"mode": "new" | "overwrite", "confirm_name": "..."}`:
- `"new"` creates a new Project record and extracts the archive into a
  fresh working directory derived from the runtime's home directory and the
  sanitized project name (collision-suffixed `-2`, `-3`, ...).
- `"overwrite"` requires `confirm_name` to equal the project's current name
  exactly (FR-PJ-5) — typed confirmation, not a checkbox. It then closes
  every open terminal session of the project, takes an automatic
  `pre_restore` safety snapshot of the current directory (itself a normal
  backup row, restorable the same way), and only then wipes the directory
  and extracts the backup.

A project archive is a `tar.gz` plus a sidecar JSON manifest
(`<id>.json` next to `<id>.tar.gz`) recording the format version, source
directory, and file count; every entry in the archive is every file in the
working directory — `.gitignore` is not honored, nothing is excluded. The
runtime validates the manifest and re-checks every archive entry's
containment (rejects `..`, absolute paths, and out-of-root symlinks)
independently of anything the Control Server already checked.

Schedules (`PUT .../backups/schedule`, body `{"cadence": "hourly"|"daily"|
"weekly", "retention": 1-100}`) are one per connection or per project. A
background scheduler on the Control Server fires due schedules; if the
target runtime is offline at fire time, that run is recorded
`last_result: "skipped_offline"` and not queued. After each successful
scheduled backup, the oldest schedule-triggered backups beyond `retention`
are deleted automatically; manual and `pre_restore` backups are never
auto-deleted.

The canonical request/response shapes are `docs/openapi.yaml` (tag
`Backups`).

## Docker

```text
GET  /api/v1/projects/{projectID}/docker
GET  /api/v1/projects/{projectID}/docker/resources
GET  /api/v1/projects/{projectID}/docker/containers/{containerID}/logs   (WebSocket)
POST /api/v1/projects/{projectID}/docker/containers/{containerID}/{dockerAction}
POST /api/v1/projects/{projectID}/docker/compose/{dockerAction}
```

Project-scoped Docker management on the Project's runtime (ADR-040,
`docs/REQUIREMENTS_DOCKER_TAB.md`). Not a stored resource — every request
is a live RPC over the runtime's control-plane connection (`docs/PROTOCOL.md`
"Implementation (Docker tab): docker commands"), so there is no docker
table. Authorization reuses `authorizedProject` (ADR-013): `404` on a
project the caller does not own, `409` if the runtime is not connected.

Visibility is strictly project-scoped: containers carrying the compose
project label derived from the Project's working directory, or bind-mounting
that directory. Everything else on the runtime host is never listed. The
runtime re-validates every container ID against that scope independently
(`502` for an out-of-scope target); the Control Server additionally rejects
malformed IDs (`400`).

`GET .../docker` returns the tab's first-render payload in one round trip:
`{available, version, compose, compose_file, containers}`. `available` is
false when the runtime host has no reachable Docker socket — the UI shows
a not-available state and the other routes still answer `409`/`502` shaped
by the runtime.

`{dockerAction}` on containers is `start`/`stop`/`restart`/`remove`
(`remove` accepts an optional `{"remove_volumes": true}` body); it returns
the refreshed scoped list. On compose, `{dockerAction}` is
`up`/`down`/`stop`/`restart` against the compose file auto-detected in the
Project's working directory — `up` is `docker compose up -d --build`;
`up`/`down` carry a 10-minute bound and return the bounded (64 KiB) tail of
the CLI's combined output. No compose file answers `502`.

`GET .../containers/{containerID}/logs` upgrades to a WebSocket relaying
batched followed log lines (`{stream_id, lines, end}`), replaying the last
200 lines then following, `docker logs -f` semantics — works for stopped
containers too, and ends (`end: true`) when the retained log ends. The
stream stops when the browser disconnects.

`GET .../docker/resources` returns read-only images/volumes/networks
referenced by the scoped containers. Docker resources are never created,
deleted, or pulled from the UI (ADR-040). Container exec is deliberately
absent — the Terminal tab is already a PTY in the project directory where
`docker exec -it` works (ADR-040).

## Audit

```text
GET /api/v1/audit
GET /api/v1/audit/settings
PUT /api/v1/audit/settings
```

Both require an administrator session (`requireAdmin`) — the audit log is a
system-wide security log, not scoped to a Project, so ordinary Project
ownership authorization doesn't apply (ADR-025).

`GET /api/v1/audit` lists recorded events, most recent first:
```json
{
  "events": [
    {
      "id": "...",
      "occurred_at": "2026-01-01T00:00:00Z",
      "event_type": "project.created",
      "actor_user_id": "...",
      "actor_email": "dev@local.test",
      "resource_type": "project",
      "resource_id": "...",
      "detail": {"name": "My App"},
      "ip_address": "127.0.0.1"
    }
  ],
  "total": 1
}
```
Query parameters (all optional): `event_type`, `actor_user_id`,
`resource_type` (exact match), `since`/`until` (RFC3339, inclusive),
`limit` (default 100, capped at 500), `offset`. `total` reflects the filter
ignoring `limit`/`offset`, for pagination.

`actor_user_id` is `null` for an event with no authenticated actor (a failed
login attempt) — `actor_email` still carries the attempted email so
repeated-failure monitoring doesn't need a user record. A referenced user
can later be deleted; `actor_user_id` becomes `null` (`ON DELETE SET NULL`)
but `actor_email` (captured at event time) still identifies who it was.

Recorded event types — authentication, runtime registration/revocation,
project changes, terminal session lifecycle, database connection/principal/
grant changes, service actions, and privileged filesystem operations
(docs/SECURITY.md "Audit") — are the `audit.Event*` constants in
`internal/audit/audit.go`. Recording is best-effort: a write failure is
logged and never blocks the action it describes.

`GET`/`PUT /api/v1/audit/settings` read/write the retention policy:
```json
{"retention_days": 90, "updated_at": "2026-01-01T00:00:00Z"}
```
`retention_days <= 0` means keep events forever. A daily background job on
the Control Server (`cmd/devstationone-server` `auditPurgeLoop`) deletes
events older than the policy; it also runs once at startup.

## Notifications

```text
GET  /api/v1/notifications
GET  /api/v1/notifications/unread-count
POST /api/v1/notifications/{id}/read
POST /api/v1/notifications/read-all
```

All four require an authenticated session and are always scoped to the
caller's own notifications — there is no admin/cross-user view, unlike
Audit.

`GET /api/v1/notifications` lists the caller's notifications, most recent
first:
```json
{
  "notifications": [
    {
      "id": "...",
      "user_id": "...",
      "type": "task.completed",
      "title": "bash finished",
      "body": "Exited successfully",
      "detail": {"session_id": "...", "project_id": "...", "exit_code": 0},
      "read_at": null,
      "created_at": "2026-01-01T00:00:00Z"
    }
  ],
  "total": 1
}
```
Query parameters: `limit` (default 50, capped at 200), `offset`. `total`
reflects the caller's full notification count, ignoring `limit`/`offset`.

`GET /api/v1/notifications/unread-count` returns `{"unread": 3}`.

`POST /api/v1/notifications/{id}/read` marks one notification read
(idempotent — marking an already-read notification again is a no-op success)
and returns `204`. `404` if `id` doesn't exist or belongs to another user.

`POST /api/v1/notifications/read-all` marks every unread notification for
the caller read and returns `204`.

Recorded notification types (`notifications.Type*` in
`internal/notifications/notifications.go`):
- `task.completed` — a terminal session exited (`internal/api/runtime_ws.go`
  `MessageSessionExit`), for the session's Project owner. `detail` carries
  `session_id`, `project_id`, `exit_code`.
- `session.awaiting_input` — a session has produced no output for
  `DEVSTATIONONE_IDLE_NOTIFY_SECONDS` (default 120s; see
  `cmd/devstationone-server` `idleNotifyLoop`), for the session's Project
  owner. Re-armed by new output — a session is only notified once per idle
  period, and again the next time it goes idle after producing more output.
  `detail` carries `session_id`, `project_id`.

Recording is best-effort, like `internal/audit.Record`: a write failure is
logged and never blocks the action it describes.

## Users (user manager)

```text
GET    /api/v1/users
POST   /api/v1/users
PATCH  /api/v1/users/{userID}
DELETE /api/v1/users/{userID}
```

Administrator-only (ADR-038; `requireAdmin`, like Metrics). List returns
every account with `id`, `email`, `display_name`, `role` (`user`/`admin`),
`created_at`, and `last_accessed_at` — the account's last authenticated
request, derived live from `max(sessions.last_seen_at)` (null until first
sign-in). Password material is never in a response.

`POST` creates an account: unique `email` (409 when taken), `password`
(at least 8 characters), optional `display_name`, `role`. `PATCH` changes
only the fields present: `display_name`, `role`, and `password` (admin
password reset — the user's current password is not required; absent
password keeps the existing one). `DELETE` removes the account and, with
it, its auth sessions (FK cascade) — audit history is kept via
`actor_user_id ... ON DELETE SET NULL`.

Guard rails (400 with a message): you cannot demote or delete your own
account; the last remaining administrator cannot be demoted or deleted;
a user who still owns projects or runtimes cannot be deleted until those
resources are cleaned up. Every mutation is audited (`user.created`,
`user.updated`, `user.deleted`).

Ownership lookup and transfer (ADR-038):

```text
GET  /api/v1/users/{userID}/resources
POST /api/v1/users/{userID}/reassign
```

`GET .../resources` returns `{projects: [{id, name}], runtimes: [{id, name}]}`.
`POST .../reassign` with `{target_user_id, project_ids?, runtime_ids?}`
moves ownership to the target user: an absent array leaves that kind
untouched, an empty array moves everything of that kind, a non-empty array
moves just those IDs. A runtime transfer only changes `created_by` — the
enrollment token and live connection are untouched. The target must exist
and differ from the source (400/404 otherwise). Audited as
`ownership.transferred` with the moved counts.

## Metrics

```text
GET /api/v1/metrics
```

Administrator-only. Returns Go's `expvar` JSON format: every published
counter plus two live gauges, `runtime_connections` and
`terminal_sessions_attached`. See docs/SECURITY.md "Observability" and
docs/DECISIONS.md ADR-026 for why `expvar` rather than a Prometheus
exposition endpoint in this MVP.

## Cross-cutting hardening (Slice 12)

Every response — not just `/api/v1/...` — carries security headers
(`X-Content-Type-Options`, `X-Frame-Options`, `Content-Security-Policy`,
etc.) and an `X-Request-Id` correlation header; every request is logged as
one structured line (method, path, status, duration, request ID, client
IP). Every mutating (`POST`/`PUT`/`PATCH`/`DELETE`) request is checked
against an `Origin`-based CSRF guard and a per-IP rate limit (a much
tighter one specifically on `POST /api/v1/auth/login`). None of this
changes any endpoint's request/response shape — see docs/SECURITY.md for
the specifics and docs/DECISIONS.md ADR-026 for why each was built the way
it was.

## Errors

Use consistent machine-readable errors with:
- HTTP status
- stable error code
- human message
- request/correlation ID where appropriate

Never return secrets in errors.

## Authentication

Browser authentication: server-side session, identified by a random 256-bit token stored in an `HttpOnly`, `SameSite=Lax` cookie. The cookie carries the `Secure` attribute only when the request arrived over TLS (directly or via `X-Forwarded-Proto: https`), because browsers drop `Secure` cookies on plain-`http` origins other than localhost. The server stores only a SHA-256 hash of the token, never the raw value. Sessions expire 7 days after creation and are individually revocable (`/api/v1/auth/sessions`). See `docs/openapi.yaml` for the concrete contract.

User preferences (docs/REQUIREMENTS_NERD_FONT.md, docs/CODE_TAB_REQUIREMENTS.md): read through
`GET /api/v1/auth/session` (`terminal_font`: `system` | `jetbrainsmono-nerd` |
`fira-code` | `cascadia-code` | `source-code-pro`;
`editor_font`: `system` | `fira-code` | `cascadia-code` | `source-code-pro`;
`font_size`: 12–18 shared by terminal + editor;
`theme`: `dark` | `light`; `display_name`), updated with a partial `PUT
/api/v1/auth/preferences` (`{"terminal_font": ...}`, `{"editor_font": ...}`,
`{"font_size": ...}`, `{"theme": ...}`, and/or `{"display_name": ...}` —
trimmed, ≤80 chars, empty clears it) → `204`. Per-account, server-stored.
The plain coding faces lose the Nerd Font's glyph icons in the terminal —
an accepted trade. The coding faces apply to both the terminal and the
Code tab's editor; `web/static/css/fonts.css` self-hosts all of them. `display_name` is the friendly name
shown instead of the email (journal creator column; empty falls back to
the email). Theme applies the
`docs/BOOTSTRAP_COLORS.md` dark workstation palette (default) or its light
variant; `web/static/css/app.css` + `web/static/js/app.js` carry the
tokens and the toggle. UI design deviations from the docs are tracked in
`docs/UI_COMPLIANCE.md`.

Runtime device authentication: a bearer token issued at enrollment (`POST
/api/v1/runtimes`), presented as `Authorization: Bearer <token>` on the
`/api/v1/runtimes/connect` WebSocket upgrade. The server stores only a
SHA-256 hash of the token, the same pattern as browser sessions.

Optional Cloudflare Turnstile challenge on login (docs/DECISIONS.md
ADR-044). `GET /api/v1/auth/turnstile-config` (unauthenticated) returns
`{"enabled": bool, "site_key": string}`; the login page renders the widget
only when `enabled`. When enabled, `POST /api/v1/auth/login` requires a
`turnstile_token` field alongside `email`/`password` — missing or invalid
tokens are rejected with `400` before the password is checked, and audited
as `auth.login_turnstile_failed` (distinct from `auth.login_failed`).
Disabled by default; see docs/SECURITY.md "Authentication" for the
server-side verification and fail-closed behavior.

## Quick Commands (Slice 29, ADR-045)

Per-project named CLI commands (e.g. "Build" → `make build`) rendered as the
project workspace's **Commands** tab. Any user authorized on the project can
create, edit, delete, import, and run them — the same access level as
creating sessions.

- `GET /api/v1/projects/{projectID}/commands` — list (UI order:
  `sort_order` ascending, then created_at).
- `POST .../commands` — `{name, command, sort_order}`; both non-empty, else
  400. Returns `201` + the command.
- `PATCH .../commands/{commandID}` — same body; `404` if the command is not
  found or belongs to another project.
- `DELETE .../commands/{commandID}` — `204`. The run history survives
  (history rows snapshot name/command text; `command_id` goes NULL).
- `POST .../commands/{commandID}/run` — opens a **real PTY session** running
  `shell -c <command>` in the project's working directory and returns the
  same `TerminalSession` object as `POST .../sessions` (errors and status
  codes identical). Each click is a new session, even if a previous run is
  still active. Every run is recorded in
  `GET .../command-runs?command_id=&limit=&offset=` (paged, newest first —
  rows carry the runner, timing, exit code/error, and never the output
  tail) and `GET .../command-runs/{runID}` (adds `output_tail`: the last
  full output of the run's PTY output (capped at 100 MiB), ANSI-stripped, captured at session exit —
  ADR-045). Deleting a project deletes its history (FK CASCADE).
- `POST .../commands/import` — one-shot scan of the project directory via
  the runtime's file RPC: Makefile targets (`make <target>`), `package.json`
  `scripts` (`npm run <key>`), and executable files in `scripts/`
  (`./scripts/<name>`). Returns `{candidates: [{source, name, command}]}` —
  nothing is saved until each accepted candidate is POSTed as a command.
  Missing files are a normal empty result, not an error. The scan reads only
  the fixed filenames `Makefile`/`package.json` and the `scripts/` directory
  inside the project's working directory; no client-supplied path reaches
  the file layer.

Commands are visible to everyone with project access and are **not**
secrets — never store a command string containing credentials. The captured
output tail can contain anything a command echoed (see docs/SECURITY.md
"Command run output capture", ADR-045).
