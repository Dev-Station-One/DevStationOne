# DevStationOne Implementation Plan

The project is implemented as complete vertical slices. Claude Code must not jump ahead.

## Slice 0 — Repository foundation

Deliver:
- Go module(s) and workspace structure
- Control Server executable
- Runtime executable
- configuration system
- logging
- health/readiness endpoints
- PostgreSQL connection layer
- migration system
- frontend shell/build system
- CI/test commands
- CodeGraph maintenance script
- OpenAPI validation workflow

Acceptance:
- clean checkout builds
- server starts
- runtime starts
- PostgreSQL migration works
- health endpoint works
- frontend builds
- tests pass
- CodeGraph index works

**Status: Complete.**

## Slice 1 — Authentication and users

Deliver real:
- initial administrator seeding (`devstationone-admin` CLI, ADR-019 — supersedes the original env-var bootstrap)
- login/logout
- secure browser session
- password hashing
- user model
- authorization middleware
- account/session management
- auth tests
- OpenAPI definitions
- UI login/account screens

**Status: Complete.**

## Slice 2 — Runtime registration and secure connection

Deliver:
- runtime identity
- enrollment
- registration
- revocation
- outbound connection
- heartbeat
- reconnect
- capability advertisement
- runtime UI
- protocol tests

Acceptance:
- runtime appears online in Control Server
- killing/restarting runtime reconnects
- revocation prevents access

**Status: Complete.**

## Slice 3 — Projects

Deliver:
- Project CRUD
- runtime selection
- user selection
- working directory
- shell
- environment policy
- authorization
- Project UI
- sidebar
- project workspace shell

Acceptance:
- create project
- view/edit project
- authorization prevents cross-user access

**Status: Complete.** ("user selection" above is superseded by ADR-020: the
runtime reports its OS user live instead of a typed `os_user` field; working
directory and shell are now a runtime-backed directory picker and dropdown,
not free text — see docs/API.md "Projects" and docs/DECISIONS.md ADR-020.)

## Slice 4 — Persistent PTY sessions

Deliver:
- PTY implementation
- multiple sessions per Project
- session lifecycle
- attach/detach
- resize
- signals
- reconnect
- WebSocket stream
- xterm.js terminal
- terminal tabs
- mobile terminal UX

Acceptance:
- two terminals operate simultaneously
- one can run Claude Code while another runs commands
- browser refresh/reconnect preserves process
- Project cwd is correct

**Status: Complete.**

## Slice 5 — Services / systemd user

Deliver:
- systemd --user detection
- list/status/start/stop/restart
- enable/disable
- journal/log retrieval
- service UI

Acceptance:
- manage a real user service without root

**Status: Complete.**

## Slice 6 — File Manager

Deliver:
- directory navigation
- safe path handling
- create
- read
- write
- rename
- copy
- move
- delete
- upload/download
- file editor
- previews
- responsive UI
- tests for traversal/symlink attacks

Acceptance:
- edit a real project file
- upload/download a file
- traversal attacks rejected

**Status: Complete.** Extended post-Slice-17 with permission/ownership
editing (chmod/chgrp/chown) in the file properties modal — `chmod`/`chown`
`file.command` actions, `RuntimeCapabilities.users`/`.groups`, see
ADR-032.

## Slice 7 — Database foundation

Deliver:
- database connection model
- protected credentials
- runtime database connector interface
- PostgreSQL connector
- MySQL/MariaDB connector
- SQL Server connector
- SQLite connector
- connection test
- connection UI

Acceptance:
- connect to each supported engine using real test databases

**Status: Complete.** Connection CRUD, AES-256-GCM-encrypted credentials
(`internal/dbconnections`), the `Connector` interface plus all four engine
connectors (`internal/dbconnectors`), the `db.command`/`db.result`/
`db.error` protocol messages, and the Databases tab UI (add/edit/test/delete
connections) — see ADR-021. Verified against real Postgres/MySQL/MSSQL/
SQLite test databases (`make dbtest-up`) and a real browser smoke test.

Slices 8 and 9 below were combined into one design during a later
brainstorm/design pass (split-pane connection tree, unified schema+data+
SQL-editor UI) — see ADR-021's SQL Editor note — but have not been
implemented yet; this plan still tracks them as separate slices until that
pass lands.

## Slice 8 — Database browser/schema

Deliver:
- catalogs
- schemas
- tables
- columns
- indexes
- constraints
- views/triggers
- database tree UI

**Status: Complete**, with one deliberate scope note: no separate "catalog"
level. A stored connection already pins one database/catalog (`database`
field, Slice 7); browsing a *different* catalog on the same server would
need a second connection, which is out of scope — see ADR-022. "Schemas"
covers the rest: real schemas for PostgreSQL/MSSQL, the connection's single
database as one implicit schema for MySQL/MariaDB, none for SQLite.

Delivered: `Connector` interface extended with `Schemas`/`Tables`/
`Columns`/`Indexes`/`Constraints`/`Triggers` across all four engines
(`internal/dbconnectors`), the runtime connection pool cache
(`internal/agent/db_manager.go`), six new API endpoints, and a "Browse
schema" lazy-loading tree per connection card (schemas → tables → columns/
indexes/constraints/triggers) — additive to the Slice 7 card grid, not the
full split-pane tree-replaces-cards UI from the brainstorm/design pass
(still open, folded into Slice 9). Verified against all four real engines
(`internal/dbconnectors/introspect_test.go`, real PK/FK/index/trigger
fixtures) and a real browser smoke test against the app's own live schema.

## Slice 9 — Database data browser/query

Deliver:
- paginated data
- filtering
- sorting
- insert/edit/delete
- SQL editor
- query results
- export
- query safety/limits as appropriate

**Status: Complete.** `Connector.Rows`/`Commit`/`Query` across all four
engines (`internal/dbconnectors`), three new API endpoints, and a data grid
+ SQL editor in the connection browser panel. Filtering is a raw SQL
`WHERE` expression rather than a structured filter builder — deliberate,
matching the SQL Editor's own arbitrary-SQL trust model (ADR-021), not a
missing feature. Export (CSV/JSON/SQL) is client-side from the currently
loaded page of rows, not a separate streaming endpoint. Row edits are
always parameterized regardless of the filter/SQL-editor exceptions above.
Query safety: default row limit 100 (capped at 5000, enforced independently
by both the Control Server and the runtime), 30s RPC timeout.

Verified against all four real engines
(`internal/dbconnectors/rows_test.go`: pagination, sort, filter, insert/
update/delete via `Commit`, raw SELECT/INSERT via `Query`) and a real
browser smoke test — which caught and fixed a real bug: an edited cell with
no original value to infer a JS type from (a NULL column, or any cell in a
freshly added row) was never numeric-coerced, so a plain "11" typed into an
`int` column reached Postgres as an unquoted string bind parameter and was
rejected ("invalid input syntax for type integer"). Fixed by coercing from
the column's actual SQL type (fetched from the columns endpoint) instead of
guessing from a JS value that might not exist.

**Update:** the SQL Editor moved from CodeMirror 5 to CodeMirror 6 +
`@marimo-team/codemirror-sql` (`web/static/vendor/codemirror6/entry.js`,
`web/static/project.html`) for dialect-aware syntax/linting (Postgres/
MySQL/MSSQL/SQLite via `@codemirror/lang-sql`'s dialects) and schema-aware
completion/hover. Schema (`{table: [columns]}`) is fetched once per
connection from the existing `/schemas`, `/tables`, `/tables/{t}/columns`
endpoints and cached client-side; not re-fetched on every editor toggle, so
schema changes made mid-session need a page reload to show up in
completions. `node-sql-parser` (the linter/completion parser) lazy-loads as
its own ~2.5MB chunk only when the SQL editor is actually opened, same
lazy-chunk mechanism as the Code tab's language-data (Slice 9's original
CM5 note above still applies otherwise). Verified live against the app's
own dev Postgres connection: dialect highlighting, the statement gutter,
schema-aware table/column completion (real `users` table surfaced), and
Run still execute correctly.

## Slice 10 — Database users/permissions

Deliver database-specific management for:
- PostgreSQL roles
- MySQL/MariaDB users/grants
- SQL Server logins/users/roles/permissions

SQLite must clearly report that server-style users/roles are not supported.

**Status: Complete.** `Connector.Principals`/`Grants`/`CreatePrincipal`/
`DropPrincipal`/`SetPrincipalPassword`/`GrantPrivilege`/`RevokePrivilege`
across PostgreSQL (native roles), MySQL/MariaDB (`'user'@'host'` accounts),
and MSSQL (paired server login + database user); SQLite returns
`dbconnectors.ErrUnsupported` and reports `SupportsUserManagement() ==
false` (`internal/dbconnectors`, ADR-024). Seven new API endpoints under
`.../databases/{connectionID}/principals`, all rejecting a sqlite
connection with `400` before dispatch. A Users panel in the connection
browser (create/delete/set-password, expandable per-user grants with
grant/revoke) — hidden entirely for a sqlite connection. A privilege
(`SELECT`/`INSERT`/`UPDATE`/`DELETE`/`ALL`) is validated against a fixed
whitelist independently by the Control Server and the runtime, since unlike
a row value it can't be a bound parameter in `GRANT`/`REVOKE` syntax; a
password in the handful of DDL statements that don't accept one either is
escaped as a SQL string literal instead.

Verified against all three real engines
(`internal/dbconnectors/principals_test.go`: create → list → set password →
grant → list grants → reject an invalid privilege → revoke → drop → list
again) — MySQL's own `devstation` test user only has privileges scoped to
its own database, so that suite connects as `root` (the official mysql
image's `MYSQL_ROOT_PASSWORD` account) specifically for principal
management, unlike every other `dbconnectors` test. SQLite's
`ErrUnsupported`/`SupportsUserManagement()` are covered directly. A real
browser smoke test (create a user, expand it, grant `SELECT` on a real
table, confirm it renders, revoke it, delete the user with the confirm
dialog) against the dev Postgres connection — passed.

## Slice 11 — Activity/audit

Deliver:
- audit model
- event capture
- filtering
- UI
- retention policy/configuration
- authorization

**Status: Complete.** `audit_events`/`audit_settings` tables, `internal/audit`
(`Record`/`List`/`Filter`/`GetSettings`/`UpdateSettings`/`Purge`), two new API
endpoints (`GET /api/v1/audit`, `GET`/`PUT /api/v1/audit/settings`, both
admin-only — ADR-025), and an Activity page (`web/static/activity.html`,
sidebar SYSTEM section) with filters (event type/actor/resource/date range),
pagination, and a retention-days setting. Event capture covers every
category in docs/SECURITY.md "Audit" (authentication, runtime registration/
revocation, project changes, session lifecycle, database connection/
principal/grant changes, service actions, privileged filesystem operations)
plus database principal/grant changes (Slice 10), which SECURITY.md's list
predates — see ADR-025. Recording is best-effort and never blocks the action
it describes. Retention is enforced by a daily background job
(`cmd/devstationone-server` `auditPurgeLoop`), also run once at startup;
`retention_days <= 0` keeps events forever.

Verified with real-Postgres tests (`internal/audit/audit_test.go`:
record/list/filter, settings, purge; `internal/api/audit_test.go`: admin-only
enforcement, event capture from real requests, filtering, settings) and a
real browser smoke test (login, filter by event type, save/restore
retention policy, sidebar navigation) against the dev Control Server.

## Slice 12 — Production hardening

Deliver:
- rate limits
- security headers
- CSRF protection as applicable
- WebSocket hardening
- runtime revocation hardening
- structured observability
- metrics
- backups/migration documentation
- deployment documentation
- end-to-end test suite
- mobile QA

**Status: Complete.** All cross-cutting hardening lands in
`internal/api/middleware.go`/`metrics.go` (ADR-026): per-IP rate limits
(`ipLimiter`, tight on login, general on every other mutating request),
security headers + a same-origin `Content-Security-Policy` on every
response, an `Origin`-based CSRF check on mutating requests (defense in
depth alongside the existing `SameSite=Lax` session cookie), structured
per-request logging with a correlation ID (`X-Request-Id`), and
`GET /api/v1/metrics` (admin-only, `expvar` format). WebSocket hardening
and runtime revocation were found already solid from Slices 2/4/7
(`coder/websocket`'s own origin check, immediate close-on-revoke,
token-hash-only storage) — this slice's addition there is disabling
per-message compression (`wsAcceptOptions`) as one more real hardening
step, not a re-implementation.

New docs: docs/DEPLOYMENT.md (build/config/systemd/reverse-proxy/upgrade)
and docs/BACKUPS.md (what to back up — PostgreSQL and the DB encryption
key, and explicitly *not* runtime-side files — plus restore steps).
docs/SECURITY.md gained matching Rate limits/CSRF/Security headers/
Observability sections.

`internal/api/e2e_test.go` (`github.com/chromedp/chromedp`, a new but
narrowly-scoped Go dependency — no Node/Playwright toolchain added) is a
real headless-browser test: login form → session cookie → Activity page
→ sidebar render, the one piece of the stack `internal/api`'s httptest-only
suite structurally cannot exercise (no JS execution). Mobile QA was a
manual phone-width (390×844) pass over the login and Activity pages —
no page-level horizontal scroll, sidebar folds correctly, tables scroll
within their own container like every other table in the app.

A real bug surfaced and was fixed during this slice: wrapping
`http.ResponseWriter` for per-request logging (`statusRecorder`) without an
`Unwrap() http.ResponseWriter` method broke every WebSocket upgrade
(`runtimes/connect`, `sessions/{id}/stream`) — `http.ResponseController`
(used by `coder/websocket` to hijack the connection) couldn't see through
the wrapper to the real `http.Hijacker`. Caught by the existing
`internal/api` integration suite (nine tests failed with "condition not met
before timeout" — every online-runtime/session-open wait, since none of
those WebSocket connections could complete) before this reached any real
deployment.

Verified: full `go test -p 1 ./...` green (including the new E2E test),
`gofmt`/`go vet` clean, `redocly lint` valid (`GET /api/v1/metrics` added),
and a real browser smoke test against the dev Control Server confirming
security headers/`X-Request-Id` are live on real responses.

## Slice 13 — Packaging and deployment

Deliver:
- Control Server deployment
- PostgreSQL setup
- Runtime systemd user unit
- upgrade path
- configuration documentation
- health checks
- production build
- CI release artifacts

**Status: Complete.** Most of this slice's groundwork — `install.sh`,
`.github/workflows/release.yml` (cross-arch trimmed static builds, checksum
verification, GitHub release publish on `v*` tags) — was actually built
under ADR-019, ahead of the plan formally reaching Slice 13; this pass is
the verification/close-out, and it found and fixed two real gaps rather
than just rubber-stamping what existed (ADR-027): the generated Control
Server systemd unit had no `User=` (ran as root — now a dedicated
`devstationone` system account), and the generated `server.env` had no
`DEVSTATIONONE_DB_ENCRYPTION_KEY` (a required setting — a fresh install
would crash-loop; now generated with `openssl rand -base64 32` on first
install only). New: `scripts/test-install.sh` (`make test-install`), a
smoke test for `install.sh --server`/`--runtime` against a locally built
tarball — this class of bug (a missing `User=` line, a missing required
env var) is exactly what an actual run catches and a code-read alone
easily misses, and neither had a runnable check before this pass. Also
fixed: two stale docs (README's "Go 1.22+" vs. `go.mod`'s 1.26;
docs/TESTING.md's `/readyz` response shape) and a broken README admin-CLI
instruction (missing environment).

PostgreSQL setup, upgrade path, configuration reference, and health checks
were already documented (README.md, docs/DEPLOYMENT.md — Slice 12) and
needed no new work beyond the fixes above.

## Slice 14 — Dashboard rewrite and browser event push

**Goal**: live dashboard (runtime status + active projects), backed by a new
process-local browser event bus.

**Design** (see `/sc:design` pass, 2026-09-14):
- New `internal/events` package: `Bus` — process-local pub/sub, same
  single-process scope caveat as `runtimes.Registry`/`sessions.Hub`
  (ADR-012). `Event{Type, UserID *string, Payload}`; `UserID: nil` broadcasts
  to every connected browser (runtime status — shared infra, matches
  existing `runtime_browse.go` visibility rule); `UserID` set delivers only
  to that project's owner (project/session status — projects are
  per-user).
- New endpoint `GET /api/v1/events/stream` — same cookie-session auth and
  heartbeat pattern as `termSessionStreamHandler`; subscribes to `Bus` with
  the authenticated user's ID, forwards events as JSON text frames.
- Publish call sites: `runtime_ws.go` `handleRuntimeConnection` (online on
  `registry.Register`, offline on `registry.Unregister`); `sessions_ws.go`
  `termSessionStreamHandler` (`project.status` after `MarkRunning`/
  `MarkDetached`). Session-exit publish site (wherever `MarkExited` fires
  from a runtime-reported exit) to be located via CodeGraph at
  implementation time.
- New `sessions.ActiveCountsByProjectIDs(ctx, pool, ids []string)
  (map[string]int, error)` — one grouped query, active = status not in
  (exited, failed, closed).
- `listProjectsHandler` adds `active_sessions int` to `projectResponse`
  (additive, OpenAPI update, no breaking change). `GET /api/v1/runtimes`
  unchanged (already returns `online bool`).
- Frontend: `web/static/index.html` gets two Tabler card-list panels
  (Runtimes, Active Projects) matching `runtimes.html`/`projects.html`
  styling — no charts/stat-tiles (docs/WEB_UI_DESIGN.md: "not a
  dashboard-heavy SaaS application"). Initial paint from the two existing
  REST endpoints; live patch via `/api/v1/events/stream`, reusing the
  reconnect/backoff logic already written for the terminal WS in
  `project.html` rather than a new implementation.
- No new DB tables or migrations required.
- Notifications (bell) feature, brainstormed separately, is expected to
  reuse this same `events.Bus` rather than get its own channel.

**Success Criteria**: dashboard reflects runtime online/offline and project
session-count changes without a page reload; existing terminal/runtime/
project functionality unaffected.

**Tests**: `events.Bus` unit tests (pub/sub, per-user filtering, broadcast);
`sessions.ActiveCountsByProjectIDs` unit test; events WS handler
integration test (auth required, receives own-user + broadcast events, not
other users' project events); browser E2E for dashboard live update.

**Status: Complete.** Built as designed, with one simplification found
during implementation: the frontend does not patch individual rows on a
live event — it refetches both `GET /api/v1/runtimes` and
`GET /api/v1/projects` wholesale on any `runtime.status`/`project.status`
message. `project.status`/`runtime.status` payloads carry only an ID
(`project_id` or `runtime_id`, plus `online` for runtimes) rather than a
computed session count, specifically so the frontend has nothing "stale"
to trust and refetching is the only correct behavior — simpler than
in-place patching and correct even if several changes coalesce into one
message. The session-exit publish site was confirmed at
`internal/api/runtime_ws.go`'s `handleRuntimeEnvelope`
(`protocol.MessageSessionExit` case, alongside the existing
`sessions.MarkExited` call), looking up the session's project to publish
to its owner.

Files changed: `internal/events/bus.go` (+test), `internal/api/events_ws.go`
(new `GET /api/v1/events/stream` handler, `publishRuntimeStatus`/
`publishProjectStatus` helpers), `internal/api/runtime_ws.go` (publish calls
on register/unregister/session-exit), `internal/api/sessions_ws.go`
(publish calls on attach/detach), `internal/sessions/session.go`
(`ActiveCountsByProjectIDs` +test), `internal/api/projects.go`
(`active_sessions` field, `activeSessionCount` helper, wired into all four
`projectResponse` call sites), `internal/api/server.go` (wiring),
`web/static/index.html` (rewritten: Runtimes + Active Projects panels,
live via the new WS). New tests: `internal/events/bus_test.go`,
`TestActiveCountsByProjectIDs` (sessions), `TestEventsWS_RequiresAuth`,
`TestEventsWS_RuntimeStatusBroadcastToEveryUser`,
`TestEventsWS_ProjectStatusScopedToOwner`, `TestProjectHTTP_ActiveSessionsCount`
(api). Docs updated: `docs/openapi.yaml` (`Project.active_sessions`),
`docs/API.md` ("Dashboard events" section), `docs/ARCHITECTURE.md`
("Dashboard events" section).

Verified: full `go test -p 1 ./...` green against the shared dev Postgres;
`gofmt`/`go vet` clean; real smoke test against the `devstationone-dev`
systemd instance — logged in as `dev@local.test` in an actual browser
(Playwright), dashboard rendered live runtime/project data with no console
errors, and a manually-opened `/api/v1/events/stream` connection
authenticated and stayed open. No migrations needed.

## Slice 15 — Notifications (bell)

**Goal**: Claude-Code-style bell notification for task completion,
awaiting-input, and runtime/system events. Reuses the `events.Bus` from
Slice 14 rather than a second push channel.

**Design** (see `/sc:design` pass, 2026-09-14):
- New `notifications` table (migration), same shape as `audit_events`
  (`internal/audit`) but per-user with a read flag: `id, user_id, type,
  title, body, detail jsonb, read_at timestamptz null, created_at`;
  index `(user_id, read_at)` for unread-count, `(user_id, created_at)` for
  listing.
- New `internal/notifications` package mirrors `internal/audit`:
  `Record(ctx, pool, userID, ntype, title, body, detail)` inserts the row
  and publishes `events.Event{Type: "notification.created", UserID:
  &userID, Payload: row}` on the Slice 14 Bus; plus `ListForUser`,
  `UnreadCount`, `MarkRead`, `MarkAllRead`.
- Trigger call sites:
  - Task completion: `internal/agent/manager.go` `sessionManager.pump`
    already sends `MessageSessionExit`; the Control Server's runtime
    envelope handler already calls `sessions.MarkExited` there — add
    `notifications.Record(..., "task.completed", ...)` at that exact spot.
  - Awaiting input (idle-time heuristic): add `lastActivity
    map[sessionID]time.Time` to `sessions.Hub`, touched in `Forward`
    (the single choke point every runtime output byte already passes
    through). A ticker (~10s) scans for sessions idle past
    `DEVSTATIONONE_IDLE_NOTIFY_SECONDS` (new env var, default 120s) not
    already flagged, resolves the project owner via `sessions.Get` →
    `projects.Get`, calls `notifications.Record`. Flag resets on new
    activity; cleared in `Hub.Discard`.
  - Runtime/system events: reuses the `runtime.status` broadcast from
    Slice 14 for the live bell popup only — not persisted as a DB row,
    since a runtime is shared infrastructure with no single owner
    (existing rule, `runtime_browse.go`). Open question if persisted
    per-affected-project-owner notifications are wanted instead.
- REST additions: `GET /api/v1/notifications` (current user only,
  cursor-paginated, mirrors `audit.List` shape), `GET
  /api/v1/notifications/unread-count`, `POST
  /api/v1/notifications/{id}/read`, `POST /api/v1/notifications/read-all`.
- Frontend: bell icon already reserved in the navbar spec
  (`docs/WEB_UI_DESIGN.md`). Dropdown + unread badge, live-patched via the
  Slice 14 `/api/v1/events/stream` connection (new event type
  `notification.created`), no separate WS.

**Success Criteria**: a notification fires and appears in the bell dropdown
after reload on a second device; unread count clears on read.

**Tests**: `internal/notifications` unit tests (Record/List/UnreadCount/
MarkRead); idle-detection ticker test (idle session flagged once, reset on
activity, cleared on discard); notification REST + WS integration tests
(ownership-scoped, no cross-user leakage).

**Status**: Complete. `internal/notifications` (Record/ListForUser/
UnreadCount/MarkRead/MarkAllRead), migration `0011_notifications`, REST at
`/api/v1/notifications*`, `sessions.Hub.IdleSessions` idle heuristic,
`cmd/devstationone-server` `idleNotifyLoop`, `task.completed` wired at
`MessageSessionExit`, bell dropdown in `web/static/js/app.js` shared across
every page. Design deviation from this entry (offset/limit pagination
instead of a cursor) recorded in `docs/DECISIONS.md` ADR-029.

## Slice 16 — Git client (status/diff/stage/commit)

**Goal**: VSCode Source Control-style panel, scoped to status, diff,
stage/unstage, and commit. Branch/push/pull, history graph, and
merge/rebase/stash are separate later slices reusing the same RPC pattern
with new `Action` values — not designed in detail here to avoid building
surface ahead of need.

**Design** (see `/sc:design` pass, 2026-09-14):
- Execution model mirrors the file manager exactly
  (`internal/agent/file_manager.go` / `internal/api/files.go`): runtime-side,
  containment-checked, RPC over the existing runtime connection.
- New protocol messages `MessageGitCommand` / `MessageGitResult` /
  `MessageGitError` (error reuses the existing `ServiceErrorPayload` shape).
  New `protocol.GitCommandPayload{Action string, Root string, ...}` /
  `GitResultPayload{...}`. `Root` is always the Project's `WorkingDirectory`
  from the DB row — never client-supplied, same containment rule as
  `validateWorkingDirectory`.
- Runtime-side: new `internal/agent/git_manager.go`, same shape as
  `fileManager`. Dispatch executes the real `git` CLI via `os/exec` with
  `cmd.Dir = Root` — not a Go git library, so it reuses whatever `git`, SSH
  keys, and credential helper are already configured for that OS user, and
  needs no new dependency. `commit` passes the message via `-F -` on stdin;
  no shell is involved anywhere (argv, not shell text).
  - `status` → `git status --porcelain=v2 -z`
  - `diff` → `git diff` / `git diff --cached` (optional path param)
  - `stage` / `unstage` → `git add -- <path>` / `git restore --staged --
    <path>`
  - `commit` → `git commit -F -`
- Control-Server side: new `internal/api/git.go`, a `gitCommand` helper
  identical in shape to `fileCommand`.
- REST routes, matching the existing `/api/v1/projects/{projectID}/files/...`
  convention:
  ```
  GET  /api/v1/projects/{projectID}/git/status
  GET  /api/v1/projects/{projectID}/git/diff?path=&staged=
  POST /api/v1/projects/{projectID}/git/stage      {path}
  POST /api/v1/projects/{projectID}/git/unstage    {path}
  POST /api/v1/projects/{projectID}/git/commit     {message}
  ```
- Frontend: new "Source Control" application tab in the Project workspace —
  needs a `docs/WEB_UI_DESIGN.md` update (tab list currently reads
  `Terminal | Files | Databases | Services`).

**Security**: same containment model as Files — `Root` always
server-derived, never accepted from the client.

**Success Criteria**: opening a repo with uncommitted changes shows the
changed-file list and diffs; staging and committing from the panel produces
a real commit in the actual repository.

**Tests**: `git_manager` unit tests per action (status/diff/stage/unstage/
commit) against a real temp git repo; REST integration tests including
containment (a path outside `Root` is rejected); OpenAPI validation for the
new routes.

**Status**: Complete. See `docs/DECISIONS.md` ADR-028 for the go-git
evaluation/rejection and the git-CLI containment reasoning; `docs/API.md`
"Git" and `docs/PROTOCOL.md` "Slice 16" for the contract; `docs/WEB_UI_DESIGN.md`
"Source Control" and `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
"Source Control Components" for the UI. `stage`/`unstage` use `git add` /
`git reset --` rather than `git restore --staged`, which refuses to run in
a repo with no commits yet.

## Slice 17 — Alternate HTML terminal (forms-based rendering)

**Goal**: a second, opt-in rendering mode for a terminal session —
structured cards for prompts/diffs instead of raw ANSI — alongside the
existing xterm.js terminal, which remains the default and is unmodified.

**Design** (see `/sc:brainstorm` + `/sc:design` passes, 2026-09-14): this is
a client-side rendering mode only, not a new execution path. The PTY byte
stream is identical to today's terminal (same `pty.Session`, same
`SendFrame`/binary WS frames) — zero protocol or runtime changes beyond the
new session field below. Only the frontend gets a second mode that
line-buffers the same bytes and runs a pattern matcher before choosing card
vs. plain-text rendering. Render mode is chosen once at session creation and
is immutable for that session's lifetime (switching modes means opening a
new terminal tab) — it is not a `localStorage` preference, so it travels
correctly when a session is reattached from another device.

Card set (v1), each with a hard fallback to plain scrolling text on
non-match — detection never guesses wrong into a stuck UI:
- **yes/no** — trailing `(y/n)`/`[y/n]`/`(yes/no)` on an unterminated line →
  button card; click sends the literal keystroke + `\n`.
- **numbered menu** — 2+ consecutive `N)`/`N.` lines followed by an idle
  unterminated line → button-per-option card; click sends the digit + `\n`.
- **diff** — a `diff --git` line opens a collapsible card, collecting until
  the next blank line/shell prompt. View-only — no stage/commit actions;
  that stays in the Slice 16 Source Control tab.
- **free-text prompt** — labels the persistent input box (see below) with
  the detected prompt text instead of a separate input card.

`ponytail: dropped the progress/spinner card from v1 — no confirmed workflow
needs it yet and `\r`-redraw-as-in-place-update is the fussiest detector of
the set. Falls back to plain text (a `\r`-heavy line just re-renders each
redraw, same as today). Add it when a real cards-mode session shows spinner
noise that's actually annoying.`

`ponytail: regex/idle-timer detection, not a real parser — add a dedicated
adapter (e.g. Claude Code's --output-format stream-json) when one specific
tool's fidelity matters enough to justify it.`

**Explicit non-goal**: full-screen TUI apps that use cursor-repositioning
ANSI (vim, htop, fzf, inquirer-style arrow menus — detected via
`\x1b[...H`/`\x1b[2J`/alt-screen-enter `\x1b[?1049h`) permanently bail that
session to plain-text rendering the moment one is seen; no detection is
attempted afterward. These tools must never be misrendered as broken cards.

**Data model / API** — new migration `migrations/0016_terminal_session_render_mode.{up,down}.sql`:
`ALTER TABLE terminal_sessions ADD COLUMN render_mode text NOT NULL DEFAULT 'xterm' CHECK (render_mode IN ('xterm','cards'))`.
`internal/sessions.Session` gets `RenderMode string`; `Create(...)` takes it
as a param. `internal/api` term-session request/response structs gain
`render_mode` (omit → server defaults to `xterm`; any value outside the enum
→ 400). No new endpoint: the existing GET/attach session-info round-trip
already returns the full row, so reattach picks up `render_mode` for free.
`PATCH` stays name-only — mode is immutable after creation. OpenAPI:
`TerminalSessionFields`/`TerminalSession` schemas gain the `render_mode`
enum field.

**Frontend**:
- New-session dialog stays the existing `DS.prompt()` SweetAlert2 flow in
  `dialogs.js` — add a radio input (`input: 'radio'`, native Swal option,
  no new modal markup) for `xterm` vs `cards`, default `xterm`.
  `ponytail: reuses the existing dialog pattern instead of a new Bootstrap
  modal/segmented-control component for a single binary choice.`
- New `web/static/js/termcards.js`: per cards-mode session, a scrollback
  pane (plain `<pre>` lines and rendered cards interleaved, append-only)
  plus one persistent input box fixed at the pane bottom (always present;
  sends Enter-terminated text over the existing `sendInput(ws, str)` path).
  PTY bytes are split on `\n`; an unterminated trailing line is evaluated
  against the detectors above after a ~150ms idle period.

**Success Criteria**: a session started in "cards" mode renders at least a
yes/no-prompt card and a diff card as structured UI, for both a generic
bash test script and the Claude Code CLI; unmatched output and full-screen
TUI output still display correctly as plain text; the existing xterm
terminal type is unchanged in behavior.

**Tests**: Go — session create/API tests for `render_mode` persistence,
default, and enum rejection (400); `make openapi-lint`. No JS test harness
exists in this repo (precedent: Slices 15/16 frontend, verified by manual
smoke test only) — `termcards.js` detection is likewise verified by real
smoke test: a generic bash script exercising y/n, menu, and diff output,
plus a manual Claude Code CLI session.

**Docs to update in the same commit as implementation**: `WEB_UI_DESIGN.md`
(new-session modal), `VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
("Terminal Components" — render-mode toggle, each card state, full-screen-TUI
bail state), `API.md` "Sessions" (`render_mode`), `DECISIONS.md` new
ADR-035 (regex/idle-timer detection choice + full-screen-TUI bail-out +
single-input-box consolidation, with the ponytail upgrade path noted above).

**Status**: Complete. See `docs/DECISIONS.md` ADR-035 for the detection
approach and two real bugs (`\r`-anchored regex, `less`'s bare `ESC =`/
`ESC >`) the browser smoke test caught and fixed; `docs/API.md` "Sessions"
and `docs/WEB_UI_DESIGN.md`/`docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
"Terminal" for the contract and UI. Migration `0016`.

## Slice 18 — Project Journal

**Goal**: a Journal tab on the Project workspace — rich-text notes
(Summernote), optional git-commit association, file attachments/pasted
images stored Control-Server-side, Tabler datatable with server-side
pagination. Requirements: `docs/JOURNAL_REQUIREMENTS.md`.

**Design**:

- Migration `0012_journal`: nullable `users.display_name`; `journal_notes`
  (id, project_id, body_html, commit_sha, commit_subject, commit_author,
  commit_datetime, created_by, created_at, updated_at; project_id FK
  CASCADE) and `journal_attachments` (id, note_id FK CASCADE, filename,
  content_type, size_bytes, content bytea; created_at).
- New `internal/journal` package modeled on `internal/projects`: Create,
  Get, ListForProject (limit/offset + total, ORDER BY created_at DESC),
  Update, Delete, AddAttachment, ListAttachments, GetAttachment.
  Snippet (first ~120 chars, HTML-stripped) computed at list time in Go.
- API on `/api/v1/projects/{projectID}/journal`: GET list
  (?limit=&offset=), POST create, GET/PUT/DELETE `/notes/{noteID}`,
  POST/GET `/notes/{noteID}/attachments[/{attachmentID}]` (multipart
  upload; download with safe Content-Disposition, following the files
  API's existing attachment handling). Authorize via the existing
  project-ownership path used by every project-scoped handler.
- `Sanitization — ponytail: render-time only, using the already-vendored
  DOMPurify at every render point (row snippet is text-stripped server
  side; full body sanitized in the view/edit modals before it touches
  the DOM). No new Go sanitizer dependency; untrusted HTML never reaches
  the DOM unsanitized. Add server-side bluemonday only if a non-browser
  client ever renders note HTML.`
- Commit association: `git.command` gains a `log` action (runtime runs
  `git log --max-count=N --pretty=format:...` in Root; agent side in
  `internal/agent/git_manager.go`, payload reuses GitCommandPayload with
  a Limit field). New GET `.../git/log?limit=` endpoint feeds the picker;
  the note stores SHA/subject/author/datetime as captured metadata, no
  GitHub API.
- Creator display: `users.display_name` (nullable); journal list joins
  users, coalescing to email. Account page gains a display-name field;
  `PUT /auth/preferences` accepts it.
- UI in `web/static/project.html`: Journal tab (Tabler datatable,
  columns DateTime | Snippet | Created By | actions), XL modals for
  editor (Summernote, vendored into `web/static/vendor/summernote` —
  Bootstrap-5 build on the existing vendored jQuery) and viewer
  (DOMPurify-sanitized render, attachment list with download links,
  commit link). Timestamps via the app's existing Intl date formatter,
  "Mon Sep 14, 11:11 AM".
- Server-side pagination like the audit-log API; edit/delete open to
  any user with project access; edits set updated_at and the row shows
  an "Edited <ts>" marker; delete confirmed via the existing SweetAlert
  dialog pattern; audit events journal_created/edited/deleted/attached.
- Limits enforced server-side: 25 MB per attachment, 10 per note.
  Summernote paste callback uploads pasted images as attachments.

**Stages** (one commit at the end; docs/CodeGraph updated in this pass,
not deferred):

1. Migration 0012 + `internal/journal` package + `display_name`
   persistence (auth setter, preferences handler field) — unit + DB
   tests. → verify: `go test -p 1 ./internal/journal ./internal/auth ./internal/api`
2. API handlers + OpenAPI + git `log` protocol action + runtime
   implementation + contract tests. → verify: `make openapi-lint`,
   `go test -p 1 ./...`
3. Web UI: vendored Summernote, Journal tab, table, editor/viewer
   modals, display-name field in account page. → verify: browser smoke
   (create/edit/delete/attach/paste-image/link-commit) + E2E test.
4. Docs (API.md, ARCHITECTURE.md, WEB_UI_DESIGN.md,
   VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md, PROTOCOL.md), CodeGraph
   maintain, full `make fmt vet lint test` gate, smoke on dev instance,
   review diff, commit.

**Success Criteria**: all acceptance criteria in
`docs/JOURNAL_REQUIREMENTS.md` demonstrably pass on the dev instance —
create/edit/delete a note, paste a screenshot (stored as attachment,
not base64), attach a 25 MB-capped file, link a commit via the picker,
server-side pagination over >1 page, "Edited" marker, display_name with
email fallback, no unsanitized HTML in the DOM.

**Tests**: `internal/journal` unit + Postgres tests; API integration
tests (authz, pagination, limits, attachment download headers); git log
agent test; JS/browser E2E for create/edit/delete/attach/paste; XSS
check confirming DOMPurify sanitization of a script-bearing note.

**Status**: Complete (2026-09-14). Implemented in one pass — journal
package + migration 0012, API + OpenAPI (tag `Journal`), `log` git
action, Journal tab with Summernote editor / DOMPurify viewer /
server-side pagination, `users.display_name` preference. Attachment
removal is note-wide (FK CASCADE, no per-attachment delete endpoint);
existing attachments carry download-only actions in the editor. Note
HTML sanitization is render-time DOMPurify, not server-side (see
`docs/JOURNAL_REQUIREMENTS.md` deviation). Dev-instance smoke verified
create/edit/delete, display-name creator, attachment upload + download,
image insert via attachment URL, commit picker + association, XSS
stripping, Edited markers. Commit-picker live list requires a runtime
with the new `log` action (this machine's dev runtime updated; other
runtimes need a runtime-binary update).

## Slice 19 — Code tab (VS Code-like editor)

**Goal**: a `Code` tab on the Project workspace — project-rooted lazy file
tree with a CRUD context menu, CodeMirror 6 tabbed editor with built-in
completion, live whole-file sync over a project-scoped WebSocket (debounced
autosave, external-change push), server-persisted open-tab state, and
account-level font preferences for terminal + editor. Requirements:
`docs/CODE_TAB_REQUIREMENTS.md`.

**Design**:

- Protocol (Stage 1, complete): `code.command`/`code.result`/`code.error`
  (open/save/watch/unwatch) plus the `code.file.changed` runtime push, on
  the existing control-plane connection via a new `editManager`
  (`internal/agent/edit_manager.go`). 1-second stat poll (no inotify),
  last-write-wins saves returning pre-write content on a stale base
  fingerprint, 2 MiB editor cap (`protocol.MaxEditorFileBytes`). ADR-033.
- Control server (Stage 2): `internal/editor` (project-keyed fan-out hub +
  `project_editor_state` store), browser stream
  `GET /api/v1/projects/{projectID}/editor/stream`
  (`internal/api/editor_ws.go`), opaque editor tab-state REST pair
  (`GET/PUT .../editor/state`, 256 KiB cap), migration
  `0013_project_editor_state`, `projects.GetByRuntimeRoot` to resolve
  runtime+root to a project. The push fan-out handler runs in its own
  goroutine — it issues a correlated open RPC back to the same runtime,
  whose reply only the runtime connection's read loop can resolve.
- Font preferences (Stage 3): migration `0014_user_editor_font`
  (`users.editor_font`, `users.font_size`), validation in `internal/auth`,
  preferences PUT gains `editor_font`/`font_size`, three self-hosted woff2
  faces (Fira Code, Cascadia Code, Source Code Pro) in
  `web/static/vendor/fonts/`, shared `web/static/css/fonts.css`,
  account-UI selects.
- Frontend (Stage 4): CM6 IIFE bundle built via esbuild and committed under
  `web/static/vendor/codemirror6/`; `web/static/js/filetree.js` extracted
  from `dialogs.js`'s directory picker; `web/static/js/editor.js` pane
  (tabs, dirty dot, 300ms debounced save, conflict prompts, always-dark
  theme, tab-state restore); `Code` app tab in `project.html`; Files-tab
  deep-link. All workspace-level tabs and the Code tab's file-tab strip run
  on the real vendored Bootstrap 5.3 `bootstrap.Tab` component rather than
  `tabler.min.js`'s bundled copy (ADR-034).

**Tests**: runtime-side edit manager unit tests
(`internal/agent/edit_manager_test.go`); hub union/drop/publish tests
(`internal/editor/hub_test.go`); end-to-end editor stream integration
tests against the in-process fake runtime
(`internal/api/editor_ws_test.go`: open/watch/push/save/stale/deleted,
two-connection fan-out, binary refusal, project authorization, state
round-trip + size cap). Frontend behavior verified by a manual smoke of
the requirements doc's acceptance criteria (no JS test infra in this repo).

**Status: Complete.** Stages 1–4 done.

## Slice 20 — Help system (contextual popovers + manual page)

**Goal**: a discoverable, real (non-stub) in-app help system — a
`data-help` icon on every top-level page and project workspace tab opening
a real `bootstrap.Popover` with a one-line summary and a link into a full
searchable manual page, plus a global `?` keyboard shortcut and a sidebar
entry. Pure frontend/static-content slice: no DB table, no API endpoint,
no OpenAPI/protocol change. Requirements gathered via `/sc:brainstorm`,
designed via `/sc:design`; full rationale in ADR-036.

**Design**:

- Content: flat-frontmatter Markdown under `web/static/help/*.md` (13
  topics: dashboard, projects, runtimes, account, activity,
  project-overview, terminal, files, code, source-control, databases,
  services, journal), enumerated by `web/static/help/index.json` (id list
  only — titles/summaries stay single-sourced in each file's frontmatter).
- `web/static/js/help.js`: fetches/caches/renders content for both contextual
  popovers (every page) and the manual page. Search is a plain client-side
  substring match over title/summary/body.
- Popovers use the real Bootstrap `Popover` component. Six pages that only
  load `tabler.min.js` get a second, minimal Bootstrap 5.3.8 vendor drop
  (`web/static/vendor/bootstrap/js/popover/*`, Popover's dependency chain
  only — never `dropdown.js`) instead of the full `bootstrap.bundle.min.js`,
  to avoid re-triggering the ADR-034 dual-Bootstrap dropdown collision;
  `project.html` reuses its existing bundle. `project.html` carries one
  dynamic help icon (`#app-help-btn`) whose `data-help` follows the active
  workspace tab rather than eight static per-panel icons.
- `web/static/help.html`: two-pane manual (TOC + search | rendered
  content), reusing the `marked.parse()` + `DOMPurify.sanitize()` pattern
  already used by `project.html`'s file preview.
- Sidebar "Help" entry and global `?` shortcut (skipped while focus is in
  a text input, CodeMirror, or xterm) added to the shared `renderSidebar()`
  in `web/static/js/app.js`.

**Tests**: `TestE2E_HelpSystem` (`internal/api/e2e_test.go`, chromedp) —
dashboard help popover content, manual page default-topic render and TOC,
and search-box filtering, against a real headless browser. Manual
Playwright smoke test against the `devstationone-dev` systemd instance
covered the popover open/close, the tab-aware `project.html` icon across a
real tab switch (this is how the "Help content unavailable." cache-priming
bug fixed in ADR-036 was actually caught), and the `?` shortcut — zero
console errors throughout.

**Status: Complete.**

## Slice 21 — About modal

**Goal**: an "About" link in the sidebar's bottom cluster opening a modal
with the app name, one-line description, license/copyright, and a link
into the help manual. Pure static content, no DB/API/protocol change.

**Design**: `renderAboutModal()` in `web/static/js/app.js` injects a
single shared `#about-modal` into `<body>` on first open (one definition
for all pages, matching `renderSidebar()`/`renderBell()`); opened via
`tabler.Modal.getOrCreateInstance(...).show()` from the new "About"
nav-link, closed via a plain click handler rather than the
`data-bs-dismiss` Data API — `project.html` also loads the real
`bootstrap.bundle.min.js` for `Tab`, and avoiding the Data API for
anything shared-sidebar-rendered sidesteps ever needing to re-litigate the
ADR-034 dropdown-collision question for a new attribute.

**Tests**: `TestE2E_AboutModal` (`internal/api/e2e_test.go`, chromedp) —
opens a real project on `project.html` (the dual-Bootstrap page), expands
the mobile-collapsed sidebar, opens the modal, asserts its heading, closes
it. Debugging this test surfaced a pure test-timing gotcha worth recording
for next time: `chromedp.WaitVisible`/`WaitNotVisible` are satisfied the
instant an element's box model/visibility matches, which can be *mid*
CSS-transition (Bootstrap's Collapse expand, or a Modal's fade) rather
than settled — a click issued immediately after can land while the target
is still animating. Fixed with short explicit `chromedp.Sleep` settles,
not a product bug (this looked, at first, exactly like the kind of
Collapse-vs-Collapse double-fire ADR-034 predicts for `data-bs-toggle`
attributes elsewhere in the shared sidebar — it wasn't, but is worth
re-checking first if a similar symptom shows up again for real).

**Status: Complete.**

## Future, explicitly not MVP

- Android client
- Linux desktop client
- Windows desktop client
- additional runtime capabilities
- optional SaaS distribution
- advanced collaboration

## Slice 22 — Projects page: datatable + description + owner + last-accessed + modal create/edit

**Goal**: make a shared multi-user project list findable — description
field, owner column, last-accessed column, search/sort/filter, and the
create/edit form moved into a modal; project access becomes server-wide
(ADR-037).

**Design**: migration 0017 adds `projects.description` and
`projects.last_accessed_at` (stamped in `createTermSessionHandler` after
`session.ready`; ignored on error). `projectColumns` grows an owner-name
subquery (`COALESCE(NULLIF(display_name, ''), email)` — journal's
expression), so every SELECT/RETURNING path returns `owner_name`.
`projects.ListForUser` is deleted; the list endpoint always calls
`ListAll`. `authorizedProject` relaxes to "any authenticated user";
`writableProject` (owner/admin) gates PATCH/DELETE. UI keeps the
`activity.html` pattern — text search + owner/runtime selects +
click-to-sort headers (`aria-sort`, CSS chevrons in `app.css`), vanilla JS,
no DataTables vendor — and one Bootstrap modal shared by New project (POST)
and row Edit (PATCH). The modal class comes from a new
`web/static/vendor/bootstrap/js/modal/` drop (backdrop, focustrap,
scrollbar, component-functions, modal — Bootstrap 5.3.8 dist UMD, same
jsDelivr source as ADR-036's popover drop) because `tabler.min.js` exposes
no `window.bootstrap`; the popover chain's globals cover its other deps.

**Tests**: `TestProjectHTTP_CreateListScopedByOwner` rewritten to the
server-wide model (bob sees alice's project, can GET it, gets 404 on
PATCH/DELETE; `owner_name` asserted); ownership-enforced tests in
files/git/sessions/services updated to 200 cross-user; ActiveSessionsCount
asserts `last_accessed_at` advances on session open. Browser smoke against
the dev instance: modal create (name/description/runtime/shell/cwd), search
filter, owner sort, edit modal pre-fill + PATCH, delete — zero console
errors.

**Status: Complete.**

## Slice 23 — Admin user manager: CRUD, roles, guard rails, live last-accessed

**Goal**: an admin-only Users page with full account CRUD, role badges,
last-accessed column, and the self/last-admin/ownership guards (ADR-038).
No migration.

**Design**: four `requireAdmin` routes in `internal/api/users.go` over
`internal/auth` additions (`ListAdminUsers` with a live
`MAX(sessions.last_seen_at)` join, `AdminUpdate` as one COALESCE UPDATE,
`GetUserByID`, `CountAdmins`, `DeleteUser`). Passwords ≥8 chars at the new
admin trust boundary. UI: `users.html` (projects-page pattern: search,
`sort-th` headers, shared create/edit modal via the `bootstrap/js/modal/`
drop) plus a `Users` sidebar link injected from the existing auth-session
fetch for admins. Help: `help/users.md` + `index.json` entry.

**Tests**: `TestUserHTTP_CRUD` — non-admin 403, short-password 400,
create/list/duplicate-409, last-accessed set after login, demote-last-admin
400, promote+demote with two admins, admin password reset (new password
logs in), self-delete 400, delete 204, audit rows recorded.

**Status: Complete.**

## Slice 24 — Ownership visibility + transfer (completes ADR-038)

**Goal**: close the dead end in the user-delete guard — admins can see
what a user owns and move it to another user, per-resource or in bulk.

**Design**: `GET /api/v1/users/{userID}/resources` (one `json_agg` query)
and `POST /api/v1/users/{userID}/reassign`, both `requireAdmin`. The ID
lists are `*[]string`: absent = untouched, empty = all, non-empty = those
IDs. Runtime transfer only changes `created_by`. Audited
`ownership.transferred`. UI: fa-boxes-stacked row action on `users.html`
opens an "Owned resources" modal — per-row reassign with a target-user
picker plus "Reassign everything"; the ownership-blocked delete error
opens the panel directly.

**Tests**: `TestUserHTTP_ResourcesAndReassign` — accurate listing,
unknown/self target guards, per-resource subset transfer, bulk empty-array
transfer, delete succeeds after transfer, 2 audit events.

**Status: Complete.**

## Slice 25 — Runtime deletion (ADR-039 addendum)

**Goal**: a real delete path for runtime records — revoke's soft state left
no permanent cleanup.

**Design**: `DELETE /api/v1/runtimes/{runtimeID}` (requireAdmin):
projects-referencing guard (400 with count), row + token hash removed,
live agent connection dropped via `registry.Revoke`, audited
`runtime.deleted`. UI: trash icon on every runtimes row; confirm text
mentions the dropped connection when online.

**Tests**: `TestRuntimeHTTP_Delete` (project guard, 403 non-admin, 404
unknown, revoked-runtime delete, audit event) +
`TestRuntimeHTTP_DeleteDropsLiveConnection` (fake agent online; after
delete the metrics `runtime_connections` gauge reaches 0).

**Status: Complete.**

## Slice 26 — Docker/Compose tab (ADR-040, REQUIREMENTS_DOCKER_TAB.md)

**Goal**: manage the project's compose stack and its containers from the
workspace UI, through the runtime, via moby client + compose CLI plugin.

**Design**:

- *Protocol*: new message types `docker.command` (list/lifecycle/compose
  RPCs, mirroring `service.command`) and `docker.logs` streaming messages
  over the existing WS gateway. `Capabilities` gains `docker{available,
  version, compose}`.
- *Runtime*: `internal/docker` — moby client dial on demand, compose
  discovery (stat the four compose filenames in project root), project
  scoping (compose label + bind-mount match), `docker compose` subprocess
  with fixed argv and 10-min timeout, `logs -f` streaming. Independent
  scope re-validation (server-side containment rule).
- *Control Server*: `internal/api/docker.go` handlers —
  `GET/POST .../projects/{id}/docker/...` (list, start/stop/restart/remove,
  compose up/down/stop/restart, logs WS upgrade, images/volumes/networks
  read-only), following `runtimeConnForProject`/`serviceCommand` patterns;
  audit events for lifecycle + compose actions.
- *UI*: Docker app tab in `project.html` per VISUAL spec — state strip
  (not available / no compose file), container datatable (existing
  dense-table pattern), log pane with follow/toggle, compose action
  buttons, read-only resource lists. Tab hidden/disabled when capability
  absent.
- *OpenAPI*: all endpoints in `docs/openapi.yaml`; *PROTOCOL.md*: new
  messages.

**Tests**: runtime unit tests with a real daemon when present +
scope-containment security tests (traversal, decoy out-of-scope container
never listed); API handler tests (authz, offline runtime, bad paths); E2E
for list/lifecycle/logs.

**Smoke**: real `docker compose up` against the dev runtime, follow logs,
stop stack.

**Status: Complete.**

## Slice 27 — Backup/restore (DB + project) and runtime-level Services (ADR-041/042/043)

**Goal**: back up and restore database connections and project working
directories, with manual triggers and per-connection/per-project schedules;
relocate Services from project-scoped to runtime-level.

**Design**: see `docs/REQUIREMENTS_BACKUPS_SERVICES.md` and
`docs/DESIGN_BACKUPS_SERVICES.md` for the full requirements/design pass.

- *Protocol*: new `backup.command`/`backup.result`/`backup.error` message
  types (`docs/PROTOCOL.md` "Implementation: backup commands"). `start_db`/
  `start_project` reply with a job ID immediately and run in the
  background; `status`/`list`/`delete`/`read`/`restore_db`/
  `restore_project` complete the rest of the surface.
- *Runtime*: `internal/agent/backup_manager.go` — per-engine dump
  (`pg_dump`, `mysqldump`/`mariadb-dump`, `sqlcmd BACKUP DATABASE`,
  `sqlite3 .backup`) and restore, tar.gz+manifest project archive/extract
  with full containment re-validation on extraction, chunked file reads for
  download, fixed backups root on the runtime's own disk (ADR-041).
- *Control Server*: `internal/backups` (metadata tables + scheduler
  goroutine, `FOR UPDATE SKIP LOCKED`, retention trimming) and
  `internal/api/backups.go` (CRUD, download proxy, restore orchestration —
  session close + pre_restore snapshot before an overwrite-restore).
  Migration `0018_backups`. New audit events
  (`backup.created/restored/deleted`, `backup_schedule.changed`).
- *Services*: routes moved from `/api/v1/projects/{projectID}/services/...`
  to `/api/v1/runtimes/{runtimeID}/services/...` (`internal/api/services.go`
  rewritten to resolve by runtime, not project); project workspace's
  Services tab removed; Runtimes page gains a per-runtime Services modal
  reusing the same unit-card actions.
- *UI*: Databases tab gains a "Backups" toggle per connection (alongside
  Users/SQL Editor); project workspace gains a "Backups" tab; both share
  `web/static/js/backups.js`'s list/backup-now/restore/schedule renderer.
  Runtimes page gains a services icon-button/modal per online runtime.
- *OpenAPI*: `Backups` tag added; `Services` tag's paths moved to
  `/api/v1/runtimes/{runtimeID}/...`.

**Tests**: `internal/agent/backup_manager_test.go` (containment: path
traversal/symlink escape/absolute entries rejected, manifest format
validation, archive/extract round trip, list/read/delete); `internal/backups`
(schedule CRUD, `DueSchedules`/`AdvanceSchedule`, retention trimming, the
`kind` CHECK constraint) against the real test Postgres; `internal/api`
(`backups_test.go`, `services_test.go`) — full HTTP round trips against a
real fake runtime running the real dispatcher: PostgreSQL backup/restore via
real `pg_dump`/`pg_restore`, SQLite backup/restore via real `sqlite3`,
project backup/restore-as-new/overwrite-restore (real tar archive/extract,
real session close, real pre_restore snapshot) via a real working directory,
and runtime-scoped services against a real `systemd --user` unit.

**Smoke**: covered by the automated integration tests above (real dump/
restore/archive/extract tools, no mocks), **plus a live browser
click-through against the dev instance** (Playwright, `dev@local.test` on
`devstationone-dev.service`) — Runtimes → Services modal against the real
desktop's actual `systemd --user` instance (119 real units), and the
project Backups tab + per-connection Backups toggle against the real
"DevStation" project and its real PostgreSQL connection. Two real bugs
surfaced by this and fixed in the same pass (not left as known
limitations):
- `services.ValidUnitName` rejected systemd's own C-style-escaped unit
  names (backslash, e.g. `app-at\x2dspi\x2ddbus\x2dbus@autostart.service`)
  — ordinary desktop-generated units couldn't fetch logs. Backslash added
  to the allowed set (`internal/services/services.go`); regression test
  added.
- The Runtimes Services modal's status/enabled badges used solid Tabler
  badge classes (`bg-success`/`bg-secondary`/...), which render with
  invisible same-color-on-same-color text inside a SweetAlert2 popup
  (the CSS variable they depend on isn't in scope there). Switched to the
  `-lt` (light) badge variants, confirmed legible by screenshot.

MySQL/MariaDB and SQL Server backup/restore are implemented (native
`mysqldump`/`mysql` and `sqlcmd BACKUP`/`RESTORE DATABASE`) but **not
exercised by a passing automated test in this environment**: the dev
machine's `mysqldump`/`mysql` binaries are MariaDB's client, incompatible
with the test MySQL 8 server's `caching_sha2_password` auth plugin (a
client/server tooling mismatch, not a code defect — confirmed by the same
failure from the bare `mysql` CLI outside any DevStationOne code), and no
local `sqlcmd`/MSSQL client was available to test against the SQL Server
test container. A runtime host running its own real MySQL/MSSQL client
tooling remains the deployment prerequisite for those two engines'
backups, same pattern as ADR-040's Docker socket prerequisite; this is
not yet independently verified.

**Status: Implementation complete; PostgreSQL, SQLite, and project backup/
restore are verified end-to-end by real (non-mocked) automated tests and a
live browser click-through against the dev instance, as is the
runtime-services relocation (also click-through-verified, two real bugs
found and fixed in the process). MySQL/MariaDB and SQL Server backup/
restore code paths exist but are unverified in this environment (see Smoke
note) — a real deployment should confirm compatible client tooling before
relying on them.**

## Slice 28 — Optional Cloudflare Turnstile on login (ADR-044)

**Goal**: bot/credential-stuffing mitigation on login beyond the existing
per-IP rate limit, without imposing an external dependency on operators
who don't want one.

**Design**: `internal/config.Server` gains
`TurnstileSiteKey`/`TurnstileSecretKey` (`DEVSTATIONONE_TURNSTILE_SITE_KEY`/
`DEVSTATIONONE_TURNSTILE_SECRET_KEY`), both optional but must be set
together. New `internal/turnstile` package verifies a token against
Cloudflare's `siteverify` API, failing closed on any error. `POST
/api/v1/auth/login` gains an optional `turnstile_token` field, checked
before `auth.Authenticate` when the feature is enabled; a new
unauthenticated `GET /api/v1/auth/turnstile-config` exposes the public site
key so the login page knows whether to render the widget. New audit event
`auth.login_turnstile_failed`, distinct from `auth.login_failed`. CSP
(`securityHeaders`) widened with a `challenges.cloudflare.com` carve-out in
`script-src`/`frame-src`. `web/static/login.html`: widget loaded and
rendered only when the config endpoint reports `enabled`; submit disabled
until a token is obtained; script load/verification failure disables
submit with an inline error rather than allowing a token-less submit.

**Tests**: `internal/turnstile` unit tests (success, rejected token,
upstream failure) against an `httptest.Server` standing in for Cloudflare.
`internal/config` tests for the optional/both-or-neither validation.
`internal/api`: `TestTurnstileConfigHandler` (enabled/disabled response
shapes) and `TestLogin_TurnstileEnabled` (missing/wrong token rejected
before the password check, both audited; valid token + correct password
succeeds) — `turnstile.SiteVerifyURL` overridden to a local fake server.

**Smoke**: live browser click-through (Playwright) against a temporary
server instance pointed at the disposable test Postgres, using
Cloudflare's public always-pass Turnstile test keys
(`1x00000000000000000000AA`/`1x0000000000000000000000000000000AA`) —
confirmed the real widget script loads from `challenges.cloudflare.com`,
renders and auto-resolves ("Success!"), the submit button enables only
after resolution, and login completes end-to-end (redirect to `/`). The
disabled-by-default path (no keys configured, e.g. the persistent
`devstationone-dev.service`) is unchanged and covered by the pre-existing
`TestLoginLogoutFlow`.

**Status: Complete.**

## Slice 29 — Quick Commands tab + run history with captured output (ADR-045)

**Goal**: a **Commands** tab on the project workspace — per-project named CLI
commands ("Build" → `make build`) runnable with one click, each opening a
real PTY session running `shell -c <command>`, with a per-run history
(runner, timing, exit status, and the captured output, full up to a 100 MiB safety cap)
for self-audit. Requirements via `/sc:brainstorm`; design via `/sc:design`.

**Design**:
- *Migration* `0019_quick_commands`: `project_commands` (project FK CASCADE,
  name, command, sort_order, created_by) and `command_runs` (project FK
  CASCADE, nullable command_id FK SET NULL, name/command_text snapshots,
  session FK SET NULL, run_by, started/finished, exit_code, error,
  output_tail text).
- *Control Server*: `internal/commands` (CRUD + the pure Makefile/
  package.json/`scripts/` import parsers), `internal/commandruns` (run
  lifecycle + `Tail()` — full output capped at 100 MiB, ANSI-stripped, forced valid UTF-8), and
  `internal/api/commands.go` (CRUD, run, import scan, history list/detail).
  `startSession()` extracted from `createTermSessionHandler` so the run
  path and the plain-session path share one open dance verbatim.
- *Protocol*: `session.open` payload gains an optional `command` field —
  the runtime starts `shell -c <command>` (still a real PTY,
  `internal/pty.Options.Command`). No new protocol message for capture:
  `sessions.Hub` gains a per-session bounded capture buffer registered
  before dispatch (ordinary terminals capture zero bytes), flushed by the
  runtime's `session.exit`/`session.error` handling (and by an explicit
  session close) into the history row.
- *Import*: reuses the existing `file.command` `read`/`list` RPCs —
  Makefile targets, `package.json` `scripts`, executable files in
  `scripts/`; missing files are normal empty results. The UI offers a
  checkbox list (grouped by source); nothing saves until accepted.
- *UI*: new **Commands** app tab (`web/static/js/commands.js`, rendered
  into `#commands-panel` on first open like `backups.js`), rows with
  Run/edit/delete + per-row History expander, output modal, add/edit modal,
  import modal with a checkbox per candidate. A run switches to the
  terminal tab it opened (host-page `onSession` → `addSession`).
- *Security*: commands pass through like typed terminal input (never
  logged, never interpolated server-side); import reads are fixed
  filenames + `scripts/` inside the project root through the existing
  containment; capture is project-access-gated, bounded (full output per run up to 100 MiB, 100
  runs/project), ANSI-stripped, valid UTF-8 — ADR-045's exception to
  ADR-007, command-run sessions only. Audit events `command.created/
  updated/deleted`.

**Tests**: `internal/commands` (CRUD + project-scope guard against the test
Postgres; import parser table tests: targets vs. assignments vs. dot-
targets, malformed/missing package.json, executable-only scripts/ filter),
`internal/commandruns` (lifecycle, failure path, list scoping/filtering/
paging with tail-free rows, prune-to-100, `Tail` ANSI/UTF-8/bounds),
`sessions.Hub` capture (registered → buffered bounded → taken-once,
ordinary terminals capture nothing, Discard clears), `internal/pty`
(`shell -c` runs to exit with the command's exit code; empty Command keeps
the interactive shell).

**Status: Complete** (2026-09-15).

## Slice 30 — Git pull/push in the Source Control tab (ADR-046)

**Goal**: Pull and Push buttons in the Source Control tab, for the current
branch only. Requirements via `/sc:brainstorm`; design via `/sc:design`
(`docs/DESIGN_GIT_PULL_PUSH.md`).

**Design**:
- *Protocol*: `git.command`'s `action` gains `pull`/`push` (no new payload
  field — both always act on the current branch's upstream, never a
  client-supplied remote/branch). `git.result` gains `Branch
  *GitBranchStatus` (on `status`), `Conflicts []GitFileStatus` (on a
  conflicted `pull`), `PushedNewUpstream bool` (on `push`).
- *Runtime* (`internal/agent/git_manager.go`): `status` adds `--branch` to
  its existing `git status --porcelain=v2 -z` call and parses the new
  header records (`parseGitBranch`) plus a cheap `git remote` check
  (`hasAnyRemote`) instead of a second ahead/behind RPC. `pull` runs `git
  pull --no-rebase`; on failure, re-reads status and treats unmerged (`U`)
  entries as conflicts (`unmergedOnly`) rather than than string-matching
  stderr — a real `git.error` still covers every other failure (no remote,
  auth, network). `push` checks for an upstream first, auto-sets one
  (`push -u origin HEAD`) if absent; never `--force`. New
  `gitNetworkCommandTimeout` (2 min) for these two actions vs. 30s for the
  rest, mirroring the docker RPC/compose split.
- *Control Server*: `gitPullHandler`/`gitPushHandler` (`internal/api/
  git.go`), routes `POST git/pull`/`POST git/push`. `git/status`'s HTTP
  response shape changes from a bare `GitFileStatus[]` array to `{files,
  branch}` — a breaking change accepted without versioning since the REST
  API has no external consumers yet. Audit events `git.pulled`/
  `git.pushed`.
- *UI* (`web/static/project.html`): Pull/Push buttons plus an ahead/behind
  badge moved into the Source Control panel's always-visible header row,
  not inside `#git-body` (which fully hides on a clean tree) — the bug the
  requirements pass specifically flagged. Disabled with a tooltip when the
  repo has no remote. A pull conflict renders as an inline alert naming the
  conflicted paths and pointing at the Terminal tab; no in-browser conflict
  resolution in this slice.
- *Security*: ambient auth only (runtime host's own SSH agent/credential
  helper) — no credential storage, no new secret surface. Remote/branch
  resolved server/runtime-side, never client-supplied. No `--force` surface
  at all.

**Tests**: `internal/agent` (`TestExecuteGit_StatusReportsBranch`,
`TestExecuteGit_PushNoUpstreamSetsOne`, `TestExecuteGit_PushExistingUpstream`,
`TestExecuteGit_PullFastForward`, `TestExecuteGit_PullConflict` — real repos
and bare remotes in `t.TempDir()`, real conflicting commits, no mocked
git), `internal/api` (`TestGitHTTP_PushNewUpstreamThenExisting`,
`TestGitHTTP_PullCleanAndConflict`, plus the pre-existing status tests
updated for the `{files, branch}` shape).

**Smoke test**: real dev Control Server + runtime, a throwaway project
against a real repo with a real bare-repo remote — verified status/branch
reporting, a real push (including first-push auto-upstream on a fresh
branch), a real clean pull, and a real merge conflict end to end via the
REST API and in the browser (Playwright): header buttons stay visible and
enabled on a clean tree, ahead/behind badge renders, conflict banner and
per-file "Conflict" rows render correctly.

**Status: Complete** (2026-09-15).

**Addendum (same slice, post-smoke)**: recovery sweep for stuck "running"
history rows — a runtime that dies mid-run sends no session.exit, so
`internal/commandruns.RecoverLoop` (30s tick in `cmd/devstationone-server`)
finalizes still-running rows from recorded state: terminal session → adopt
its exit code/error; runtime offline/revoked longer than 2 minutes →
"runtime went offline while running" (blips under the window are left alone
— the runtime keeps PTY sessions alive across reconnect). The Hub's
in-memory capture buffer is flushed into the tail while still reachable.
UI: a finalized-with-no-exit row shows a "no exit" badge plus the error.

**Addendum (same slice)**: deleting a command now deletes its run history
too — migration `0020_command_runs_cascade_delete` changes
`command_runs.command_id` from nullable `ON DELETE SET NULL` to `NOT NULL
ON DELETE CASCADE` (and cleans up any pre-existing orphaned rows). Orphaned
history with no UI path back to it (the History expander lives on the
command row) didn't serve the self-audit purpose it was kept for.

## Slice 31 — Mandatory 2FA + passkeys (ADR-047)

**Goal**: every account must complete two-factor authentication; add
passkeys (WebAuthn) as both a full passwordless login path and a 2FA
method, alongside TOTP, email one-time codes, and backup-code recovery.

**Design**: see ADR-047 for the full rationale. Migration `0021_two_factor`
adds `users.two_factor_enrolled_at` (NULL = must enroll, permanently the
default for every account, not just a rollout flag) plus `user_totp`,
`webauthn_credentials`, `user_backup_codes`, `user_email_2fa`,
`user_trusted_devices`, `user_2fa_recovery_tokens`. New dependencies
`github.com/go-webauthn/webauthn` and `github.com/pquerna/otp` (no viable
stdlib equivalent for either protocol). New `internal/mail` package
(`net/smtp`, stdlib) for email-OTP/recovery mail. `internal/auth` gains
`totp.go`, `backupcodes.go`, `twofactor.go`, `webauthn.go`,
`trusteddevice.go`, `recovery.go`, `challenge.go` (the in-memory pending-
login/ceremony state). `internal/api/twofactor.go` adds 21 endpoints:
enrollment/factor management (shared between the mandatory gate and
account-settings "add a factor" via `resolveTwoFactorSubject`), login
step-up (`/2fa/verify`, WebAuthn-as-2FA, email-request-code), full
passwordless passkey login, trusted-device management, three independent
recovery paths (backup code, self-service email link, admin reset), all
converging on `auth.ClearAllTwoFactor`. `loginHandler` (`internal/api/
auth.go`) now branches on trusted-device cookie → enrollment gate → 2FA
step-up instead of ever setting a session directly. `NewRouter` gains a
`TwoFactorConfig` parameter (SMTP + WebAuthn RP settings) — updated at
every one of its ~20 call sites. New audit events: `auth.2fa_enrolled`,
`auth.2fa_factor_added/_removed`, `auth.2fa_verify_failed`,
`auth.2fa_backup_code_used`, `auth.2fa_recovery_used`,
`auth.2fa_admin_reset`, `auth.passkey_login`,
`auth.trusted_device_added/_revoked`. `web/static/login.html` rewritten
with enrollment (TOTP QR/manual-secret, email, passkey), step-up
(method picker, trust-device checkbox), and full passwordless-passkey UI,
using vanilla WebAuthn base64url/ArrayBuffer transforms (no new JS
dependency).

**Tests**: `internal/auth` (TOTP drift/seal-open, backup-code generation),
`internal/mail` (SMTP-disabled and header-injection guards),
`internal/api/twofactor_test.go` (mandatory enrollment + `/2fa/methods`,
last-factor-cannot-be-removed guard, backup-code single-use login,
trusted-device skip + revoke, admin reset, recovery-confirm) — all via the
real HTTP router, no mocked auth layer. `internal/api`'s shared `loginAs`
test helper (used by ~20 other test files) now drives every fresh account
through TOTP enrollment automatically, and through step-up verify on a
second login, so the rest of the suite kept working unmodified apart from
that one helper. The three browser E2E tests (`e2e_test.go`) gained
`e2eLoginWithEnrollment`, driving the real login page's new enrollment UI
in headless Chrome rather than assuming a bare password-only redirect.
WebAuthn ceremony crypto itself (real attestation/assertion signing) is
**not** covered by an automated test — faking a FIDO2 authenticator's ECDSA
signing and COSE key encoding is a disproportionate undertaking for this
pass; the begin/finish wiring, storage, and authorization gates around it
are tested, the cryptographic ceremony itself needs a real authenticator or
browser, exercised manually.

**Known limitation**: a recovery-confirm session is issued immediately (so
the user isn't locked out), but nothing server-side blocks other endpoints
until re-enrollment — only `GET /2fa/methods` reporting `enrolled: false`
nudges the client back into the gate. Passkey/WebAuthn UI in
`login.html` is functional but not restyled beyond the existing Tabler
card/button patterns already on that page — no QR-code image renderer was
added (the `otpauth://` URI is shown as manually-enterable text instead,
which every authenticator app also accepts); a QR library is a reasonable
later addition if the text fallback proves to be a real UX problem.
Account-settings pages for managing factors after enrollment (viewing/
removing passkeys, regenerating backup codes, revoking trusted devices
outside the login flow) expose their endpoints but have no dedicated UI in
this slice — only the login-time flows got one.

**Status: Complete** (2026-09-15).

## Slice 24: Android native client — device authentication (ADR-053), server half

**Goal**: paired phones authenticate to the Control Server with per-request
ECDSA signatures and manage their devices; the pairing QR is minted from the
admin CLI.

**Implemented**: migration 0023 (`devices`, `device_pairing_codes`);
`internal/devices` (pairing-code lifecycle with FOR UPDATE single-use
consume, device CRUD, Sig-scheme parse/verify with 60 s both-direction ts
window, single-use nonce cache, SPKI P-256 key handling, Sign helper);
`internal/api` `requireIdentity` (device or session-cookie identity, device
resolves to pairing user so `requireAdmin` composes) adopted on the
Android-facing routes (projects read, project/session endpoints incl.
stream, device management; everything else stays cookie-only);
`POST /api/v1/auth/devices/pair` (code-gated, 10/15 min IP rate limit),
`GET /api/v1/auth/devices`, `DELETE /api/v1/auth/devices/{id}`,
`GET /api/v1/users/{userID}/devices` (admin); audit events
`device.paired` / `device.pairing_code_created` / `device.revoked`;
`devstationone-admin pair --email --name --url` prints the
`devstation://pair` QR (skip2/go-qrcode) plus the code; Settings page gains
a Paired-devices table and Users manager gains a per-user devices modal;
openapi.yaml (deviceSig scheme, four paths, Device/DevicePairRequest
schemas), API.md "Device authentication", ADR-053.

**Tests**: devices package unit (header parse, signed-message
method/URI coverage, nonce cache mark/fresh/prune, per-request signature
uniqueness); API integration against the real test Postgres (pair end to
end, code reuse rejection, malformed-key rejection with code left burnable,
signed list, unsigned 401, header replay 401, stale ts 401, revoke → 204 →
401, cross-user revoke 403, admin cross-user revoke 204, admin per-user
device view, non-admin 403).

**Known limitation**: the nonce cache is per-process (ADR-012 scale note) —
a Control Server restart within the 60 s ts window is the replay bound. The
Android app itself is a follow-on slice in this pass's plan; the web UI and
API are complete for it to consume.

**Status: Complete** (2026-10-10).

## Slice 25: Android native client — the app (ADR-053)

**Goal**: DevStationOne.apk — paired-device terminal client for Android,
muxalot's proven UX running against DevStation's session model and the
Slice 24 device-signature auth.

**Implemented**: `android/` (Gradle/AGP 8.7.2, Kotlin 2.0.21, minSdk 26,
compose BOM 2024.10.01 — the exact proven pair muxalot ships): pairing
screen with zxing QR scan + `devstation://pair` deep link (idle-only
handling) and https-only enforcement; multi-server store; projects list;
terminal workspace with the full muxalot port — LazyRow session chips with
client-side per-tab colors, state dots, long-press tab menu (rename/color/
bulk close), detach-vs-kill close dialog, per-session WebSocket streams
binary both ways with Sig-authenticated upgrades, native KeyCaptureView +
InputEncoder input stack (sticky modifiers, bracketed paste w/ ESC strip,
app-cursor mode reporting), touch layer (swipe scroll incl. SGR wheel
translation in mouse-tracking apps, long-press select, tap-to-keyboard),
OSC 52 confirm + OSC 8 http(s)-only confirm, 8 ms flush batching with the
server's scrollback-replay-aware `tty.reset()`, reconnect backoff + kick on
network change, exited-session detection via REST status check,
ShortcutDock + fan groups + user macro editor with on-device shortcut
store, app lock (cold start/screen-off/60 s grace) + FLAG_SECURE.

**Deviations from the muxalot source, all deliberate**: sessions are
UUID-keyed REST resources (name = label) instead of tmux names; resize/
signal/detach ride REST, not WS JSON ctl; `clip_set`/`clip_get` (tmux
buffer) dropped — OSC 52 passthrough covers clipboard; muxalot's
tmux-new-session dialog becomes the REST session-create dialog; Files
screen dropped (web owns files by design); Pro/Edition gating removed
(themes + backup always on).

**Build verification**: `assembleDebug` compiles and links clean —
`android/app/build/outputs/apk/debug/app-debug.apk` (11.5 MB). No emulator
or device is attached to this dev machine, so the on-device E2E (pair →
open session → type → detach → reattach) is pending a physical install;
every server-side behavior it depends on is covered by the Slice 24 API
and live-smoke tests.

**Status: Complete (build); on-device verification pending** (2026-10-10).
