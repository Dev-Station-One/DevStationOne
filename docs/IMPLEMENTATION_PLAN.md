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

**Status: Complete.**

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

## Future, explicitly not MVP

- Android client
- Linux desktop client
- Windows desktop client
- additional runtime capabilities
- optional SaaS distribution
- advanced collaboration
