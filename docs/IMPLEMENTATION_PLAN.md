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

**Design** (see `/sc:design` pass, 2026-09-14): this is a client-side
rendering mode only, not a new execution path. The PTY byte stream is
identical to today's terminal (same `pty.Session`, same `SendFrame`/binary
WS frames) — zero protocol or runtime changes. Only `web/static/project.html`
gets a second mode that line-buffers the same bytes and runs a pattern
matcher before choosing card vs. plain-text rendering.

- Detection is regex-based over plain text (trailing `? `/`(y/n)`/numbered-menu
  lines → button card; a `diff --git` block → collapsible diff card).
  Anything unmatched falls back to plain scrolling text — never breaks,
  never guesses wrong into a stuck UI.
  `ponytail: regex-based generic detection, not a real parser — add a
  dedicated adapter (e.g. Claude Code's --output-format stream-json) when
  one specific tool's fidelity matters enough to justify it.`
- Card buttons (yes/no, menu choice) send the equivalent literal keystrokes
  back over the existing binary WS frame path — no backend input change.
- New nullable `terminal_sessions.render_mode` column (migration; default
  `'xterm'`, opt-in `'cards'`), set at session creation, so the mode travels
  with a session that's reattached from another device — not stored in
  `localStorage`.

**Success Criteria**: a session started in "cards" mode renders at least a
yes/no-prompt card and a diff card as structured UI for a tool with a
conventional prompt format; unmatched output still displays correctly as
plain text; the existing xterm terminal type is unchanged in behavior.

**Tests**: JS unit tests for the pattern matcher (matched → card, unmatched
→ plain text, no false crash on arbitrary bytes); migration test for the new
column and its default; browser E2E confirming input round-trips correctly
from a card button.

**Status**: Not Started

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

## Future, explicitly not MVP

- Android client
- Linux desktop client
- Windows desktop client
- additional runtime capabilities
- optional SaaS distribution
- advanced collaboration
