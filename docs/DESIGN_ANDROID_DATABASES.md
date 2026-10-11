# DESIGN_ANDROID_DATABASES.md — Android Databases tab (round 3)

Implements `docs/REQUIREMENTS_ANDROID_DATABASES.md`. Mock:
`docs/ANDROID_CLIENT_MOCK.html` (Database tab). Status: **design draft
2026-10-10** — no code yet. Reuses round 1/2 precedents verbatim: ADR-053
device-signature auth (`requireIdentity` + `deviceSig`, `TestDeviceFilesAuth`
test pattern), the `hiddenOther` workspace pattern, `Api` request/check
helpers, `ScmScreen`'s `act()` busy/error wrapper, and pure-logic helpers
with unit tests.

## 1. Verified findings that change the requirements

Read from the code on 2026-10-10; each corrects an assumption in the
requirements doc.

1. **The runtime does not guard row edits.** `buildAndExecCommit`
   (`internal/dbconnectors/connector.go:488`) builds `WHERE` from whatever
   `original` map the caller sends, in one transaction, and never checks rows
   affected. `col = NULL` silently matches nothing. So FR-E3 ("the runtime
   refuses ambiguous matches") is wrong: **the client owns row identity.**
   Decision D3 below.
2. **No row estimates, no totals.** `DBTableInfo` is `{name, kind}`;
   `DBRowsResult` has `has_more`, not a count. The UI shows no `≈ N` and no
   "of N" (the mock matches). Paging is next/prev driven by
   `has_more`.
3. **No PK flag on columns.** `DBColumnInfo` is
   `{name,type,nullable,default,position}`. Primary key comes from
   `constraints` (`type = primary_key`, `columns`) with `indexes[].primary`
   as fallback.
4. **Restore is not "overwrite this database".** Body is
   `{target_database, confirm:true}`; the runtime restores into a named
   database on the same connection, and takes a `pre_restore` backup first
   (`Backup.trigger` enum). FR-H4's typed-name confirm becomes: typed
   confirm of **target database name**.
5. **PATCH secret is all-or-nothing.** `PATCH …/databases/{id}` takes
   `secret:{password, extra_params}` and `Update` writes both
   (`dbconnections.Update`). Changing the password from the phone would
   **wipe `extra_params` set on the web**, which the API never returns.
   Decision D4.
6. **Server RPC deadline is 30 s** (`dbRPCTimeout`,
   `internal/api/databases.go:22`); a slow query returns 504. Cancel on the
   client cannot stop the server-side statement earlier than that.
7. **Backup routes**: 19 `requireSession` registrations are backup-related;
   only the 9 per-connection DB backup routes are in scope. Project-level
   backups (`/projects/{id}/backups*`) stay web-only.

## 2. Decisions

- **D1 Navigation**: the tab owns a private back stack
  (`DbRoute`: Connections → Tables → Data/Structure/Sql/Principals/Backups;
  Grants under Principals). Not workspace overlays — unlike Journal/Files the
  DB tab has deep, tab-local navigation, so overlays would hide the tab bar
  mid-flow with no benefit. `BackHandler(enabled = tab == 4 && depth > 1)`
  pops the stack; at depth 1 back falls through to the workspace.
- **D2 Grid**: Compose has no table. `ResultGrid` = one `LazyColumn` of rows;
  each row is a fixed first cell (sticky key column) plus a `Row` driven by a
  single shared `ScrollState` for horizontal scroll (same trick as
  `DiffScreen`). Column width = clamp(max(header, first 20 sampled cells) ×
  7 dp, 72, 200). Cells render ≤ 200 chars; full value in the row sheet. One
  component serves Data browser and SQL results.
- **D3 Row identity**: `original` = primary-key columns only, taken from the
  fetched row. No PK (or a PK column of float/real/double type, which does
  not round-trip) ⇒ Edit/Delete disabled with a message; Insert still allowed.
  After every commit the screen re-fetches the page and, for update/delete,
  checks the row actually changed or vanished — if not, shows "No row
  matched" (compensates for the runtime not reporting rows affected).
- **D4 Password change vs `extra_params`**: recommended **server change in
  slice 1**: make `secret.extra_params` optional (pointer) in the PATCH body;
  absent ⇒ keep the stored value. Keeps `extra_params` web-only without
  data loss. Decided 2026-10-10 (§9).
- **D5 Typed values**: JSON from the API is decoded to `JsonElement`.
  Editing converts per `DBColumnInfo.type`: int/serial/bigint → number,
  numeric/float/real/double → number, bool/bit → boolean, everything else
  (text, dates, uuid, json) → string. Only changed columns go in `values`.
  Insert uses a per-column tri-state: *default* (omit) / *value* / *NULL*.
- **D6 Query execution**: all SQL, filter, and row calls use `netClient`
  (150 s) although the server caps at 30 s; query keeps a handle to the
  OkHttp `Call` so Cancel aborts the HTTP request. Server-side cancellation
  stays out of scope (OQ-5 default).
- **D7 Statement classifier** (UX only, never a security control): strip
  leading whitespace/`--`/`/* */` comments; read-only iff first keyword ∈
  {SELECT, WITH, EXPLAIN, SHOW, PRAGMA, DESCRIBE, VALUES, TABLE} **and** no
  `;` followed by further non-comment text (blocks `SELECT 1; DROP …`).
  Everything else shows the confirm dialog quoting the statement.
  `WITH … DELETE` (data-modifying CTE) is a known false negative; the dialog
  copy says "may modify data" accordingly.
- **D8 SQL editor**: plain monospace `BasicTextField` for v1 (OQ-2); sora
  editor reuse is a later swap behind the same composable signature.
- **D9 Phone-only layout** (OQ-3). **ExtraParams web-only** (OQ-4, via D4).
- **D10 Secrets**: password lives in a `remember` (not `rememberSaveable`)
  holder, cleared on dismiss/save; never in drafts or logs. SQL text and
  filters are never logged. Screens honor the existing screenshot flag.

## 3. Component map

New (all under `android/app/src/main/java/dev/devstationone/`):

| File | Contents |
|---|---|
| `net/DbApi.kt` | `Api.db*` methods as extension functions on `Api` (keeps `Api.kt`, already 16 callers, from growing); DTOs (`DbConnection`, `DbTable`, `DbColumn`, `DbIndex`, `DbConstraint`, `DbTrigger`, `DbRows`, `DbRowEdit`, `DbPrincipal`, `DbGrant`, `DbBackup`, `DbSchedule`) |
| `ui/DatabasesScreen.kt` | nav host + back stack, tab entry |
| `ui/DbConnectionsScreen.kt` | list, form, delete confirm |
| `ui/DbTablesScreen.kt` | schema picker, table list, Structure (4 segments) |
| `ui/DbDataScreen.kt` | filter, sort, pager, row sheet, row edit/insert/delete |
| `ui/DbSqlScreen.kt` | editor, run, results, history, warning dialog |
| `ui/DbAdminScreen.kt` | principals, grants, SQLite-unsupported state |
| `ui/DbBackupsScreen.kt` | list, create, schedule, download, restore |
| `ui/DbGrid.kt` | `ResultGrid` (D2) |
| `ui/DbLogic.kt` | pure: classifier (D7), sort cycle, page math, PK detection, value coercion (D5), form validation per engine, edit-enabled derivation |

Changed: `ui/Workspace.kt` (5th `Tab(MuxIcons.Database)` + `hiddenOther`
box; new cylinder icon in `MuxIcons`), `docs/openapi.yaml`,
`internal/api/server.go`, `docs/API.md`, `docs/ANDROID_CLIENT.md`,
`docs/IMPLEMENTATION_PLAN.md`.

## 4. HTTP surface (all via `Api`, signed per request)

All under `/api/v1/projects/{p}/databases`. Server side every route below
moves `requireSession` → `requireIdentity`; web path unchanged.

| Method + path | Android method | Notes |
|---|---|---|
| `GET ""`, `GET /{c}` | `dbConnections`, `dbConnection` | has_secret only |
| `POST ""`, `PATCH /{c}`, `DELETE /{c}` | `dbCreate`, `dbUpdate`, `dbDelete` | POST flat `password`; PATCH nested `secret` (D4) |
| `POST /{c}/test` | `dbTest` | error text verbatim |
| `GET /{c}/schemas`, `/tables`, `/tables/{t}/{columns,indexes,constraints,triggers}` | `dbSchemas`, `dbTables`, `dbStructure(kind)` | `schema` query param |
| `GET /{c}/tables/{t}/rows` | `dbRows(limit,offset,sort,sortDesc,filter)` | `has_more` paging |
| `PATCH /{c}/tables/{t}/rows` | `dbCommit(edits)` | body `{edits:[{op,original,values}]}`, one transaction (verified against `commitRowsHandler`) |
| `POST /{c}/query?limit=` | `dbQuery(sql,limit)` | `is_exec`, `rows_affected` |
| `GET/POST /{c}/principals`, `DELETE`, `PUT password`, `GET/POST grants`, `POST grants/revoke` | `dbPrincipals`… | sqlite → 400 "no server-side users" mapped to the unsupported state, not an error |
| `GET/POST /{c}/backups`, `GET/DELETE /{c}/backups/{b}` | `dbBackups`… | |
| `GET/PUT/DELETE /{c}/backups/schedule` | `dbSchedule*` | cadence hourly/daily/weekly, retention 1–100 |
| `GET /{c}/backups/{b}/download` | `dbDownloadBackup(out)` | streamed `copyTo(OutputStream)` like `downloadAttachment`; destination from `ActivityResultContracts.CreateDocument` |
| `POST /{c}/backups/{b}/restore` | `dbRestore(target, confirm=true)` | |

No new protocol messages and no migrations: the runtime commands already
exist; this round only changes auth, one PATCH field (D4), and openapi
security entries.

## 5. Screens (see the mock for pixels)

- **Connections**: cards (engine badge, `host:port/db`, key icon when
  `has_secret`). Offline runtime ⇒ banner + Retry (distinguish 502/504 from
  empty). Empty state with Add.
- **Form**: engine segment drives visible fields (SQLite: name + file path
  only). Port defaults per engine (5432/3306/1433). Test button runs
  `/test` on the *saved* record — so Save first, or Test posts to a draft?
  **Decision**: Test is enabled only after Save (the API has no draft-test
  route); the mock shows it disabled on a new connection.
- **Tables**: header chip row (Tables / Users / Backups), schema picker
  (hidden for SQLite), SQL button, overflow (Edit / Test / Refresh).
- **Structure**: Columns / Indexes / Constraints / Triggers segments; PK key
  glyph from D3 detection.
- **Data**: filter box (raw expression, user-typed) + Apply, header-tap sort
  (asc→desc→none), pager `prev · Rows a–b · size ▾ · next`, FAB insert.
  Row tap ⇒ bottom sheet (all columns, Edit/Delete).
- **SQL**: editor, Run, history sheet (20/connection, app-private store),
  result grid with `has_more` ⇒ "truncated at N rows", rows-affected banner,
  Cancel while running.
- **Principals/Grants/Backups/Restore**: as mocked; restore dialog asks
  for `target_database` (prefilled with the connection's database) and a
  typed confirm of that name.

## 6. Danger rules

- Destructive actions (delete connection, delete row, drop principal, revoke,
  delete backup, restore, non-read-only SQL) always go through a confirm
  dialog naming the object; none auto-repeat on refresh.
- Never send a client-computed WHERE; `filter` is the only free-text SQL
  fragment besides the SQL editor, both user-typed.
- Authorization is server-side per request; the app never caches
  connection secrets or ids across servers.
- Offline runtime, 504, 409, and 400 responses render the server message
  verbatim (≤ 200 chars, as in `ScmScreen`).

## 7. Testing

- **Server**: `TestDeviceDatabasesAuth` (table-driven over every switched
  route: signed device → 2xx/expected, revoked → 401, no auth → 401),
  mirroring `TestDeviceFilesAuth`; PATCH `extra_params` merge test (D4).
- **Android unit** (`DbLogicTest`): classifier incl. `SELECT 1; DROP`,
  comments, CTE; sort cycle; page math with `has_more`; PK detection
  (constraint, index fallback, none, float PK); value coercion round-trips;
  form validation per engine; edit-enabled derivation.
- **DTO decode tests** with fixtures captured from the dev instance
  (`ds1.premadev.com`), including `null` cells and `raw` grants.
- **Real engines**: dev Postgres (55432) plus a throwaway SQLite file on the
  runtime host; MySQL/MSSQL covered by the existing connector integration
  tests, spot-checked on device if available.
- **On-device E2E** (Pixel 8a via uiautomator dump, per the project
  runbook): add connection → test → browse → sort/filter/page → edit row →
  SQL (SELECT and UPDATE with warning) → principals (PG and SQLite) →
  backup/download/restore. Recorded in `IMPLEMENTATION_PLAN.md` as
  Slice 28.

## 8. Slices (each vertical: code + tests + openapi + docs + CodeGraph + commit)

1. **Server auth switch** — all in-scope routes to `requireIdentity`,
   openapi `deviceSig`, `TestDeviceDatabasesAuth`, D4 merge fix, API.md.
   Deploy to prod (`scripts/deploy-to-prod.sh`) before device E2E.
2. **Shell + browse** — `DbApi.kt` read methods, Workspace tab + icon,
   Connections list, Tables, Structure, `DbLogic` basics.
3. **Data browser** — `ResultGrid`, filter/sort/paging, row sheet (read).
4. **SQL editor** — classifier, warning, history, cancel.
5. **Row edit** — edit/insert/delete, PK identity, re-fetch verification.
6. **Connection CRUD** — form, test, delete, secret handling.
7. **Principals & grants.**
8. **Backups** — list/create/delete, schedule, download, restore.

## 9. Resolved decisions (2026-10-10)

- **D4**: server change — `secret.extra_params` becomes optional on PATCH;
  omitted keeps the stored value (slice 1).
- **Test-before-save**: Test works only after Save, using the existing
  `/test` route; no new server surface. The mock reflects this.
- **OQ-6** (SQLite backup restore with the file open): unresolved by
  design; verify runtime behavior at the start of slice 8 and block SQLite
  restore on device if unsafe.

## 10. Risks

- Phone-sized data grids stay awkward; the row sheet is the real reading
  surface. If testing shows the grid is not worth its cost, slice 3 can ship
  list-of-cards rows instead without changing the API layer.
- The runtime's silent zero-row updates mean D3's re-fetch check is the only
  safety net; a PK-less table must never get an Edit affordance.
- Raw-expression `filter` and SQL editor are deliberate arbitrary-SQL
  surfaces (ADR-021/023); the device key is the only gate, so a lost,
  unlocked phone can run SQL. App lock (existing) should be the documented
  mitigation in `docs/SECURITY.md` when this ships.
- 30 s server deadline vs long restores/backups: those RPCs may need the
  async job pattern the web UI already uses (check `Backup.state running`
  polling in slice 8, not a blocking call).

## 11. Status

Design approved 2026-10-10. Slice 1 (server auth switch) complete — see
`IMPLEMENTATION_PLAN.md` Slice 28; slices 2-8 not started.
