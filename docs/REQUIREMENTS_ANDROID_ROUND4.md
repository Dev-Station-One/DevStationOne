# REQUIREMENTS_ANDROID_ROUND4.md — Android round 4 (usability pass) and round 5 (MariaDB)

Status: **requirements draft 2026-10-11** (no design yet — next step `/sc:design`).
Produced by the `/sc:brainstorm` pass. Builds on round 3
(`docs/REQUIREMENTS_ANDROID_DATABASES.md`, `docs/DESIGN_ANDROID_DATABASES.md`,
shipped). Scope answers recorded from the user:

- Rounds: **Android-only changes first (round 4), MariaDB as its own later
  round (round 5)**. MariaDB is a real engine, not a label.
- SQL editor: swap to the **sora editor** with SQL highlighting and a
  completion popup. **No LSP.**
- Tab memory: per project, survives app restart, and also restores the exact
  Databases sub-screen.
- Project list runtime filter: remembered **per server**.
- Remembered state uses plain storage (no special app-lock handling).
- Autocomplete columns: load **all tables' columns when the editor opens**
  (capped, remainder lazy).

## Context

- Round 4 needs **no server, runtime, migration or OpenAPI change**: every
  call it makes already exists and already accepts the device signature
  (ADR-053).
- The sora editor, its TextMate engine, and an SQL grammar
  (`assets/textmate/grammars/sql.json`, scope `source.sql`) are already in
  the app (`CodeScreen`). Today the SQL editor is a plain `OutlinedTextField`
  (design decision D8 deferred the swap "behind the same composable
  signature").
- `WorkspaceScreen` holds the selected tab in a plain `mutableIntStateOf(0)`,
  so every project opens on Terminal. `DatabasesScreen` keeps its private back
  stack in `remember`, so it resets whenever the workspace leaves composition.
- `ProjectsScreen` shows projects in server order and already fetches
  `runtimes()` (names + online flag) for the subtitle.
- The connection form's engine and the Structure view's segment are chip rows
  (`MuxChip`); `DropdownMenu` is already used in the app (schema picker, SQL
  history).
- The server has four engines (`postgresql`, `mysql`, `mssql`, `sqlite`);
  `database_connections.engine` is CHECK-constrained. MariaDB talks the MySQL
  protocol but has no engine value of its own.

## Goal

Make the app faster to resume and easier to operate one-handed: reopen where
you left off, find projects quickly, pick engines and structure views from
dropdowns, and write SQL with highlighting and schema-aware completion.

## Functional requirements — round 4 (Android only)

### FR-1 — Remember the open tab (per project, persisted)
1. Leaving a project (back to the project list, switching server or project)
   and returning to it restores the last selected workspace tab for that
   project, including after the app process was killed and restarted.
2. Keyed by server id + project id; two projects (or the same project on two
   servers) never share a value.
3. First open of a project, an unknown/corrupt stored value, or an index
   outside the current tab count → Terminal (tab 0).
4. The Terminal tab's own session selection is unchanged (already persisted
   separately); this requirement covers only the workspace tab.
5. Stored with the existing SharedPreferences store pattern; contains no
   secrets.

### FR-2 — Remember the Databases sub-screen
1. The Databases tab restores its navigation to the deepest **restorable**
   route: Connections, Tables (connection), Data or Structure (connection,
   schema, table), SQL (connection), Users, Grants (connection, user),
   Backups (connection).
2. The back stack is restored, not just the top route, so Back from a
   restored Data screen returns to Tables, then Connections.
3. Routes are stored by id/name only (connection id, schema, table, user
   name). Connection details are re-resolved from the connection list on
   restore.
4. If a stored connection no longer exists, or a stored table/schema fails to
   load, fall back to the nearest valid ancestor (Connections list at worst)
   — never an error screen.
5. **Never restored**: the connection form (new/edit), any dialog or sheet
   (row sheet, edit/insert form, restore, password, delete confirms), SQL
   editor text, filter text, sort, and page offset. Passwords and typed values
   are never stored.
6. Keyed like FR-1. Plain storage; restoring is **not** gated or cleared by
   the app lock (user decision).

### FR-3 — Project list: alphabetical + runtime filter
1. Projects are listed alphabetically by name, case-insensitive, stable for
   ties.
2. A dropdown (Material3 `DropdownMenu`) at the top of the list filters by
   runtime. First entry **All**, then the runtimes, each with its online/
   offline state; the button shows the current choice.
3. The choice is remembered **per server** across app restarts. First use, or
   a stored runtime that no longer exists, resets to All.
4. A filter that matches no projects shows an explicit empty state with a
   one-tap "Show all".
5. The filter is client-side over the already-fetched project and runtime
   lists; no new request.

### FR-4 — Engine dropdown in the connection form
1. The engine chip row becomes a dropdown selector (Material3 `DropdownMenu`)
   listing the engines the server supports: MySQL, PostgreSQL, SQL Server,
   SQLite.
2. Selecting an engine keeps today's behavior: field set per engine, default
   port prefill unless the user changed it, SQLite = name + file path only.
3. On an existing connection the selector is shown disabled (engine is fixed
   once saved), as the chips are today.
4. Engine labels come from the single `engineLabel` mapping used by the list
   and form. **MariaDB is not offered in round 4**; it joins the list in
   round 5 (see below). An engine value the app does not know renders as its
   raw string in the list and must not crash the form.

### FR-5 — Structure selector as a dropdown
1. The Columns / Indexes / Constraints / Triggers segment chips become a
   dropdown selector (Material3 `DropdownMenu`); default Columns.
2. Each entry shows its item count once the structure loaded.
3. The selection is not persisted (resets to Columns per visit).
4. Content, primary-key marker, and the four underlying calls are unchanged.

### FR-6 — SQL editor on sora (highlighting + completion)
1. The SQL text area is the sora `CodeEditor` with the vendored SQL TextMate
   grammar, using the same theme/instance setup as `CodeScreen`. Run, Cancel,
   History, the non-read-only confirm dialog, the 500-row limit, and row
   sheet behave exactly as in round 3 (`isReadOnlySql` and history are
   untouched).
2. **Completion popup** offers, case-insensitively by prefix:
   - SQL keywords/functions (a fixed list covering the four engines' common
     syntax);
   - table and view names of the connection's schema;
   - column names — after `table.` (or `alias.` when resolvable simply) only
     that table's columns; otherwise columns of all loaded tables, ranked
     below keywords/tables.
3. **Schema data**: when the editor opens it loads the connection's table
   list and then each table's columns using the existing `tables` and
   `columns` calls. Cap: up to N tables (N set in design, default proposal
   50) are loaded up front with bounded concurrency; remaining tables load on
   demand when their name is typed or after `table.`. Loading never blocks
   typing or Run.
4. Schema used: the schema last browsed for this connection, else
   `defaultSchema(engine, schemas)`; SQLite has none.
5. Failure to load schema data (offline runtime, 504, permissions) degrades
   to keywords-only completion with a small, dismissible notice; it is never
   a blocking error.
6. Editor behavior: monospace, soft-wrap off, no auto-correct/auto-capitalize,
   works with the soft keyboard and `imePadding`; text survives rotation and
   the tab being hidden (hidden-but-composed pattern); the entered text is
   still not persisted or logged beyond the existing 20-item history.
7. **No LSP, no diagnostics, no formatter** in this round (explicit non-goal).

## Non-functional requirements

- NFR-1 No new permissions and no new server routes; no behavior change when
  the server is an older build (round 4 only uses routes that exist today).
- NFR-2 Remembered state holds ids/names only — no passwords, SQL text, row
  values, or filter text; stored in app-private storage.
- NFR-3 Restore/fallback paths never crash on missing, renamed, or
  permission-changed objects.
- NFR-4 Completion latency: popup within ~100 ms of a keystroke once schema
  data is loaded; schema loading cannot push the editor's first paint beyond
  what `CodeScreen` takes today.
- NFR-5 Schema loading respects the server's 30 s per-call deadline and the
  runtime's load: bounded concurrency (design sets the number), cancel on
  leaving the screen, no retry storms.
- NFR-6 Accessibility: dropdown buttons carry content descriptions and expose
  expanded/collapsed state; touch targets ≥ 48 dp; selected item is announced.
- NFR-7 Pure logic (sort + filter, route (de)serialization and ancestor
  fallback, tab-index validation, completion ranking and prefix/qualifier
  parsing, schema-load capping) lives in testable helpers with unit tests, as
  in `DbLogic`.

## User stories / acceptance

- **US-1** As a user I reopen a project and land on the tab I used last.
  *AC*: switch to Files, go back to the list, reopen the project, and kill
  the app and reopen — Files is selected both times; a different project is
  unaffected.
- **US-2** As a user I come back to the table I was browsing. *AC*: open
  Data for `orders`, leave the project, return — Data for `orders` shows with
  Back going Tables → Connections; delete the connection on the web first —
  returning shows the Connections list, no error.
- **US-3** As a user with many projects I find mine fast. *AC*: list is A→Z
  ignoring case; choosing runtime "bee" shows only its projects; closing the
  app and reopening that server still shows "bee"; a runtime with no matching
  projects shows "Show all".
- **US-4** As a user adding a connection I pick the engine from a dropdown.
  *AC*: dropdown lists MySQL, PostgreSQL, SQL Server, SQLite; picking SQLite
  hides host/port/user/password; an existing connection's selector is
  disabled.
- **US-5** As a user inspecting a table I choose Columns/Indexes/Constraints/
  Triggers from a dropdown showing counts.
- **US-6** As a user writing SQL I get highlighted SQL and completions.
  *AC*: typing `sel` offers `SELECT`; `FROM or` offers table `orders`;
  `orders.` offers only its columns; with the runtime offline the editor
  still highlights and offers keywords, with a notice.

## Open questions (for design)

1. **Bulk columns**: there is no single "all columns of a schema" route — one
   call per table. Is the cap + lazy load enough, or should design propose a
   small server route (this would end "no server change" for round 4)?
2. **Alias resolution**: how much simple `FROM t a` alias handling is worth
   building (requirement says "when resolvable simply"); default is
   table-name qualifier only.
3. **Filter scope**: list only runtimes that have projects, or all runtimes
   the server returns? Proposal: runtimes with at least one project.
4. **Storage shape** for FR-2: one JSON string per project in
   SharedPreferences vs several keys; design decides.
5. **Dropdown vs chips on very wide/foldable screens**: out of scope (phone
   only), noted for later.

## Explicitly out of scope (round 4)

- A real SQL language server, query diagnostics, formatting, snippets.
- MariaDB (round 5).
- Remembering SQL text, filters, sort, paging, or any dialog state.
- Persisting the Structure selection.
- Tablet/foldable layouts.
- Any change to the web UI or server.

---

## Round 5 — MariaDB engine (requirements outline, ships after round 4)

Separate round, full stack, because the engine value exists in the database
schema and the runtime, not just the UI. Detailed requirements and design come
with their own `/sc:design` pass; the constraints below are fixed now.

1. **New engine value `mariadb`** end to end: `database_connections.engine`
   CHECK constraint (migration), API validation (`validEngines`), OpenAPI enum
   and docs, runtime connector registration, web UI engine selector, Android
   engine dropdown (MariaDB added to the FR-4 list).
2. **Driver**: reuse the MySQL wire driver; the connector must handle the
   places where MariaDB differs (default port 3306; dump/restore tooling —
   `mariadb-dump`/`mariadb` vs `mysqldump`/`mysql`; version-specific grant and
   role syntax; `SHOW GRANTS` raw output like MySQL).
3. **Feature parity** with the MySQL connector: test, schemas, tables,
   structure (columns/indexes/constraints/triggers), rows, row commit, query,
   principals/grants, backup/restore, schedules.
4. **Existing data**: current `mysql` connections are untouched; no
   auto-conversion. Users can create a new `mariadb` connection.
5. **Old clients**: an Android build that predates round 5 must tolerate a
   `mariadb` connection in the list (raw engine string, no crash); it simply
   cannot create or re-pick that engine.
6. **Tests**: connector integration tests against a real MariaDB (alongside
   the existing MySQL ones), migration up/down, API validation, device-auth
   test table unchanged (same routes).
7. **Docs**: API.md, ARCHITECTURE.md, DECISIONS.md (new ADR), SECURITY.md
   database section, IMPLEMENTATION_PLAN.md, openapi.yaml.
