# REQUIREMENTS_ANDROID_DATABASES.md — Android Databases tab (parity round 3)

Status: **requirements draft 2026-10-10** (no design yet — next step `/sc:design`).
Produced by the `/sc:brainstorm` pass. Mock: `docs/ANDROID_CLIENT_MOCK.html` (Database tab).
Scope answers recorded from the user: full connection CRUD, full row editing,
principals/grants and backups all **in**; SQL editor runs any statement with a
warning on non-SELECT.

## Context

- The backend is complete: connection CRUD/test, schemas, tables, columns,
  indexes, constraints, triggers, rows (limit/offset/sort/sort_desc/filter),
  row commit, `/query`, principals/grants, backups + schedule + download +
  restore are all in `docs/openapi.yaml` and served for the web Database
  Manager. Connector behavior lives runtime-side (`internal/dbconnectors`).
- **Blocker**: every `/databases/*` route (and the backup routes) is
  registered with `requireSession` (cookie only). A device-signed request
  gets 401 (ADR-053). Each route the app calls must move to `requireIdentity`
  with an openapi `deviceSig` security entry; `TestDeviceFilesAuth` is the
  test pattern, as in rounds 1 and 2.
- Android has no DB client code today (`Api.kt`). Precedent to follow:
  `ScmScreen` (LazyColumn, `act()` busy/error wrapper, confirm dialogs,
  pure logic helpers with unit tests, `netClient` for slow calls).
- Authorization stays server-side: `authorizedConnection` already binds the
  connection to the authorized project and returns 404 otherwise. The client
  never supplies engine, host resolution or SQL dialect decisions the server
  does not re-check.
- Credentials: the server returns only `has_secret`; passwords are
  write-only from the client (SECURITY.md).

## Goal

On a paired device a user can inspect and operate a project's databases end to
end — connect, browse, query, edit, administer users, and back up/restore —
sized for a phone.

## Functional requirements

### FR-A — Databases tab (workspace)
1. Fifth workspace tab "Databases" (cylinder icon, drawn in the existing
   `MuxIcons` style, with contentDescription), preserved hidden-but-composed
   via the `hiddenOther` pattern. Internal navigation is a stack:
   Connections → Connection → Table → Row, with back-handler support.

### FR-B — Connections
1. List the project's connections: name, engine badge, `host:port/db`, secret
   indicator.
2. Create / edit / delete a connection. Fields per engine: name, engine
   (`postgresql|mysql|mssql|sqlite`), host, port (engine default prefilled),
   username, password (write-only; blank on edit = keep existing), database
   (SQLite: file path), SSL mode, MSSQL instance name, search path.
   Extra params stay web-only unless requested.
3. Test connection (`/test`) shows success or the runtime's error verbatim.
4. Delete requires a confirm dialog naming the connection.
5. SQLite hides host/port/user/password/SSL/instance fields.

### FR-C — Schema browser
1. Schema selector (PostgreSQL/MSSQL real schemas; MySQL single implicit
   schema; SQLite none — selector hidden).
2. Table list with kind (table/view) and row estimate if provided.
3. Table detail with four sections: Columns (name, type, nullable, default,
   PK), Indexes, Constraints, Triggers. Views/triggers shown only where the
   engine supports them.
4. Runtime offline → a clear "runtime offline" state, not a spinner.

### FR-D — Data browser
1. Paged rows (default page 50, user-selectable 25/50/100/200), next/prev,
   page indicator. Hard client cap on rendered cell length (truncate with
   tap-to-expand).
2. Sort by tapping a column header (asc → desc → none).
3. Filter field (raw SQL boolean expression, user-typed — ADR-021/023 trust
   model; the client never builds it from other input). Error from the
   server shown verbatim.
4. Wide results scroll horizontally with a sticky first column; a row tap
   opens a row detail sheet (all columns, vertical).

### FR-E — Row editing
1. Row detail sheet → Edit: per-column inputs, NULL toggle, type-aware
   keyboard hints. Save sends `commitRows` with `original` + `values`
   (parameterized server-side).
2. Insert row (FAB on table view) and Delete row (confirm dialog showing
   the identifying columns).
3. Tables with no primary key: edit/delete disabled with an explanatory
   message (the runtime refuses ambiguous `original` matches; the client
   must not guess).
4. Failures (constraint violation etc.) surface verbatim, form state kept.

### FR-F — SQL editor
1. Multi-line monospace editor per connection, Run button, results grid
   (same grid component as FR-D), row cap with "truncated" notice,
   `RowsAffected` for DML/DDL, elapsed time, error text.
2. Any statement runs. Non-SELECT-shaped statements (client heuristic:
   first keyword not in SELECT/WITH/EXPLAIN/SHOW/PRAGMA/DESCRIBE) trigger a
   confirm dialog quoting the statement. The heuristic is UX only — the
   server is not weakened or relied on.
3. Query history (last 20) kept on-device per connection; no query text or
   results are logged (SECURITY.md). History lives in app-private storage
   and is cleared on app-lock reset/unpair.

### FR-G — Principals and grants
1. List principals (users/logins/roles) with flags (can login, superuser).
2. Create principal (name, password, can-login, superuser), set password,
   drop (confirm).
3. Grants view per principal: table-level grants list; grant and revoke a
   privilege on a table from the fixed privilege whitelist. MySQL shows the
   engine's `raw` grant text.
4. SQLite: the section renders an honest "SQLite has no server-side users or
   roles" state (server already returns 400).

### FR-H — Backups
1. Per connection: list backups (time, size, status), create backup now,
   delete (confirm).
2. Schedule: view, set (cadence + retention per existing schema), remove.
3. Download a backup to device storage via the system document-create
   picker (streamed, not buffered in memory).
4. Restore from a backup: two-step confirm (type the connection name),
   progress, result.

### FR-I — Server auth prerequisite
1. Switch all routes used by FR-B…FR-H to `requireIdentity` one route at a
   time; web cookie path unchanged; openapi `deviceSig` entries added; one
   device-signed integration test per route group. Prod binary must carry the
   switch before E2E (`scripts/deploy-to-prod.sh`).

## Non-functional requirements

- NFR-1 Security: never log SQL, row data, passwords, or filters; DTOs for
  secrets are write-only and zeroed from UI state on dismiss; screens set
  FLAG_SECURE when the app's screenshot setting is on.
- NFR-2 Memory: rows decoded page-at-a-time; backup download streamed.
- NFR-3 Resilience: all calls on `netClient` (150 s) for query/restore/backup;
  UI stays responsive, with a cancel affordance for a running query
  (cancels the HTTP call; server-side cancellation is out of scope).
- NFR-4 Draft safety: unsent SQL editor text and row-edit forms survive
  rotation/process death (draft pattern from round 1).
- NFR-5 Tests: pure-logic unit tests (statement classifier, sort cycle,
  pagination bounds, edit-enabled derivation, form validation per engine);
  `Api` DTO decode tests against captured fixtures; server device-sig tests
  per route group; on-device E2E recorded in `IMPLEMENTATION_PLAN.md`.
- NFR-6 Visual: Material 3 theme already in app; mock palette matches the
  existing workspace mock.

## User stories / acceptance

- US-D1 As a user I add a PostgreSQL connection, test it, and see its tables.
  *Accept*: test shows OK/error; table list loads; password never appears in
  any response, log line, or screen after save.
- US-D2 I browse a table, sort by a column, filter, and page.
  *Accept*: request carries `sort`, `sort_desc`, `filter`, `limit`, `offset`.
- US-D3 I edit a row and see the change persisted.
  *Accept*: `commitRows` update; re-fetch shows the value; a PK-less table
  disables edit with a message.
- US-D4 I run `DELETE FROM t` in the SQL editor.
  *Accept*: confirm dialog appears first; rows-affected reported.
- US-D5 I open Principals on a SQLite connection.
  *Accept*: honest unsupported state, no 400 surfaced as a crash.
- US-D6 I create, download, and restore a backup.
  *Accept*: file lands via picker; restore needs typed-name confirm.
- US-D7 A device-signed request to every route above returns 2xx; the same
  request with a revoked device returns 401.

## Open questions

- OQ-1 Staging order. Proposed: (1) server auth switch, (2) browse
  read-only, (3) SQL editor, (4) row edit, (5) connection CRUD, (6)
  principals/grants, (7) backups. Confirm in `/sc:design`.
- OQ-2 SQL editor syntax highlighting: reuse the sora-editor SQL TextMate
  grammar from the Code tab, or plain monospace for v1?
- OQ-3 Tablet/landscape: grid-first layout, or phone-only for this round?
- OQ-4 MSSQL/MySQL extra params (`ExtraParams`) editable on device, or
  web-only?
- OQ-5 Query cancel: HTTP-call cancel only, or add a server/runtime cancel
  route (new protocol message)?
- OQ-6 Backup restore of a SQLite file backup while the file is open —
  runtime behavior to verify before design.

## Explicitly out of scope (round 3)

Docker tab, Services, Commands, ER diagrams, import/export of CSV, saved
named queries synced to the server, multi-statement transaction control,
server-side query cancellation (OQ-5 default), cross-database browsing
beyond a connection's pinned database.
