# DESIGN_ANDROID_ROUND4.md — Android round 4 (usability pass) and round 5 (MariaDB engine)

Implements `docs/REQUIREMENTS_ANDROID_ROUND4.md`. Status: **design draft
2026-10-11** — no code yet. Reuses the round 1-3 precedents verbatim: the
`hiddenOther` workspace pattern, `Api` extension files (`net/DbApi.kt`), the
`Loaded` fetch wrapper and `Acting` busy wrapper (`ui/DatabasesScreen.kt`,
`ui/DbAdminScreen.kt`), SharedPreferences stores (`data/Data.kt`), the
sora-editor setup in `ui/CodeScreen.kt`, and pure logic in testable helpers
(`ui/DbLogic.kt`). Round 4 changes **no server, runtime, migration or OpenAPI
surface**; round 5 is a full-stack engine addition.

## 1. Verified findings

Read from the code on 2026-10-11.

1. **Workspace tab state is a bare `mutableIntStateOf(0)`**
   (`Workspace.kt`), and `DatabasesScreen`'s stack is a `remember`ed
   `mutableStateListOf(Connections)`. Both die when the workspace leaves
   composition — hence "always Terminal".
2. **sora's completion API is open to us.** `TextMateLanguage` (0.24.6) is a
   public, non-final class; it exposes `requireAutoComplete(ContentReference,
   CharPosition, CompletionPublisher, Bundle)`, `setCompleterKeywords`, and
   `create(scope, autoComplete: Boolean)`. `CompletionPublisher.addItem(s)`,
   `SimpleCompletionItem(label, desc, prefixLength, commitText)` and
   `CompletionHelper.computePrefix` exist. Plan: subclass `TextMateLanguage`
   and override `requireAutoComplete` — no new dependency.
3. **The SQL grammar is already vendored**
   (`assets/textmate/grammars/sql.json`, scope `source.sql`,
   `languages.json:22`). `CodeScreen` already maps `.sql` to it, so
   highlighting needs only the same registry setup (`setUpTextmate`, currently
   `private` in `CodeScreen.kt`).
4. **The web UI already has schema-aware SQL completion** (CodeMirror 6,
   `dbSchemaCache {table: [columns]}`, one fetch per table, failure ⇒ editor
   still works without completion). Round 4 is parity, not new product
   behavior, and the "one call per table" cost is already accepted there.
5. **`mysql` already serves MariaDB servers today**: one `mysqlConnector`,
   web label "MySQL / MariaDB" (`project.html:798,1601`), and
   `backup_manager.go:353` already falls back to `mariadb-dump`. Round 5
   therefore adds a distinct **identity** (value, label, tooling preference,
   room to diverge), not a functional capability gap. The user chose the real
   engine; this note is recorded so the cost is a conscious one (§9).
6. **`DbRoute`s are not serializable and hold full `DbConnection` objects**;
   the Form route holds draft-free data but must never be restored (secrets).
7. **Engine strings leak into many places**: `validEngines` +
   message (`api/databases.go:88,95`), `SupportedEngines`/`For`
   (`dbconnectors/connector.go:229,238`), backup dump/restore switches
   (`agent/backup_manager.go:344,388`), the `database_connections.engine`
   CHECK (`0009`), openapi enum, web selector + dialect switch
   (`project.html:798,1601,2165`; `MariaSQL` exists in the vendored CM6),
   Android `engineLabel`/`defaultPort`/form.
8. **Config changes are absorbed**: the manifest lists `configChanges` for
   orientation/screenSize, so rotation does not recreate the Activity.

## 2. Round 4 decisions

- **D1 Persistence store**: one `UiStateStore(ctx)` in `data/Data.kt`
  (SharedPreferences `"uistate"`), ids and names only (NFR-2):
  - `tab/<serverId>/<projectId>` → Int
  - `db/<serverId>/<projectId>` → JSON string of `List<DbRef>`
  - `filter/<serverId>` → runtime id, `""` = All
  `UiStateStore.forgetServer(serverId)` deletes every key under
  `*/<serverId>/*` and `filter/<serverId>`; called from the server-removal
  path in `ServerListScreen` (`store.remove`). No size cap needed (ids only,
  bounded by projects).
- **D2 Tab memory**: `WorkspaceScreen` initializes
  `tab = state.tab(key).takeIf { it in 0..4 } ?: 0` and writes on selection
  change (`LaunchedEffect(tab)`; cheap `apply()`). The tab count is a named
  constant `WORKSPACE_TABS = 5` next to the tab row so FR-1.3's range check
  and the row cannot drift.
- **D3 Databases restore** — snapshot + validate-then-push:
  ```kotlin
  @Serializable data class DbRef(val kind: String, val conn: String = "",
      val schema: String = "", val table: String = "", val user: String = "")
  ```
  `kind ∈ tables|data|structure|sql|users|grants|backups`. Pure functions in
  `DbLogic`: `refsOf(stack): List<DbRef>` (skips `Form`, `Connections`
  root, keeps order) and `restoreStack(refs, resolve): List<DbRoute>` where
  `resolve` is a suspend lambda that returns the route or null.
  Algorithm on `DatabasesScreen` start, when stored refs are non-empty: show
  Connections at once (list spinner) → fetch `dbConnections` once → for each
  ref in order, validate **one cheap call** and push, stopping at the first
  failure:
  - `tables|sql|users|backups` → connection id exists in the list;
  - `data|structure` → `dbTables(conn, schema)` contains `table`;
  - `grants` → `dbPrincipals(conn)` contains `user`.
  Any exception ⇒ stop (the validated ancestor stays on top), show a small
  dismissible notice "Couldn't reopen <name>" — never an error screen
  (FR-2.4). Persist on every stack change via
  `LaunchedEffect(stack.toList())` writing `refsOf`. The restore job is
  cancelled with the composition and runs once per `projectID`.
  Not stored (FR-2.5): Form, dialogs, sheets, SQL text, filter, sort, offset.
- **D4 Reusable dropdown**: `MuxDropdown` in `ui/kit/Kit.kt`:
  ```kotlin
  @Composable fun <T> MuxDropdown(label: String, selected: T?, options: List<DropdownOption<T>>,
      onSelect: (T) -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true)
  data class DropdownOption<T>(val value: T, val text: String, val detail: String? = null)
  ```
  A `TextButton`-style anchor showing `"$label: ${selected.text} ▾"` inside a
  `Box` with a Material3 `DropdownMenu`; ≥ 48 dp; semantics:
  `contentDescription = "$label, $selectedText"`, `stateDescription` =
  expanded/collapsed, `Role.DropdownList`; disabled ⇒ anchor shown, menu not
  openable. It replaces the three chip groups (engine, structure kind) and
  the schema picker/Runtime filter use it too — one component, one behavior.
  The plain `DropdownMenu` is used (as the user's links), not
  `ExposedDropdownMenuBox`, because the anchor is a button not a text field.
- **D5 Engine options**: `ENGINE_OPTIONS: List<DropdownOption<String>>` in
  `DbLogic` — round 4: `mysql "MySQL"`, `postgresql "PostgreSQL"`,
  `mssql "SQL Server"`, `sqlite "SQLite"` (user's order, MySQL first).
  `engineLabel` stays the single label source (list cards + dropdown).
  An engine value outside `ENGINE_OPTIONS` (e.g. a `mariadb` connection seen
  by this build) renders as its raw string and the dropdown is disabled for
  existing connections anyway, so edit/save round-trips it unchanged.
  `selected` for an unknown engine is shown via an extra synthetic option, not
  dropped.
- **D6 Structure selector**: `StructureKind` enum (Columns, Indexes,
  Constraints, Triggers); `MuxDropdown` options carry `detail = count` once
  loaded (`"Columns (7)"`). The bundle fetch is unchanged (four calls at
  once), so counts are free. Not persisted.
- **D7 Project list**: pure helpers in `DbLogic`-style new file
  `ui/ProjectListLogic.kt`:
  - `sortedProjects(list)`: `sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER){ it.name }.thenBy { it.id })`;
  - `runtimeOptions(projects, runtimes)`: runtimes with ≥ 1 project (open
    question 3 resolved: **only runtimes that have projects**), sorted by name,
    each with online state; `All` first;
  - `filterProjects(list, runtimeId)`.
  `ProjectsScreen` keeps one fetch (`projects()` + `runtimes()`), stores
  the selection in `UiStateStore.filter(serverId)`, and treats a stored id
  that is not in `runtimeOptions` as All **without** overwriting it until the
  user picks (so a runtime coming back online reapplies). Empty result ⇒ text
  + `TextButton("Show all")`. A project whose runtime is unknown to
  `runtimes()` appears under All only.
- **D8 SQL editor swap**: new `ui/DbSqlEditor.kt` exposing
  ```kotlin
  class SqlEditorHandle { var text: String; fun setText(s: String); val hasText: State<Boolean> }
  @Composable fun SqlEditor(handle: SqlEditorHandle, complete: SqlCompletionSource, modifier: Modifier)
  ```
  `SqlView` keeps Run/Cancel/History/confirm/limit logic and `isReadOnlySql`
  unchanged; it reads `handle.text` at Run time and calls
  `handle.setText(h)` for a history pick, so the 500-row path and tests are
  untouched. Setup mirrors `CodeScreen` (`CodeEditor` in `AndroidView`,
  `TextMateColorScheme`, `isWordwrap = false`, fixed `180.dp` height so
  `AndroidView` has a definite size; `onRelease { it.release() }`;
  `ContentChangeEvent` → `hasText`). `setUpTextmate` becomes `internal`
  (moved to `ui/TextmateSetup.kt`). Soft-keyboard flags: no suggestions/
  auto-correct (`TYPE_TEXT_FLAG_NO_SUGGESTIONS | MULTI_LINE`). Dark TextMate
  theme as in `CodeScreen` (one theme for both editors).
- **D9 Completion engine** (pure, `ui/DbCompletion.kt`, unit-tested):
  - `completionAt(text: CharSequence, cursor: Int): Ctx(prefix, qualifier?)` —
    scan back over `[A-Za-z0-9_$]`; if the char before is `.`, `qualifier` is
    the identifier before it (quotes/backticks stripped); no popup when the
    prefix is empty and there is no qualifier.
  - `aliasMap(text): Map<String,String>` — regex
    `\b(?:from|join)\s+([\w."`]+)(?:\s+(?:as\s+)?(\w+))?` skipping reserved
    words as aliases; resolves `FROM orders o` ⇒ `o → orders` (open question
    2: **yes, simple aliases only**; subqueries/CTEs are not resolved and then
    fall through to "all columns").
  - `suggest(ctx, schema, engine, aliases): List<Suggestion(text, kind, detail)>` —
    `kind ∈ keyword|table|column`; case-insensitive prefix match; with a
    qualifier: only that table's columns (alias or table name,
    case-insensitive); otherwise keywords + tables first, then columns of all
    loaded tables (detail = owning table), max 50 items, alphabetical within a
    kind.
  - `insertText(engine, name)`: quote only when needed (`[a-z_][a-z0-9_]*`
    fails or reserved) — `"x"` for postgresql/sqlite, `` `x` `` for mysql
    (and mariadb in round 5), `[x]` for mssql.
  - `SqlLanguage(scope, source): TextMateLanguage` overrides
    `requireAutoComplete`: reads the current `SqlSchema` snapshot, builds
    `SimpleCompletionItem`s, adds to the publisher; kind icons by
    `CompletionItemKind`.
- **D10 Schema loader**: `SqlSchema(tables: List<DbTable>, columns: Map<String,List<DbColumn>>)`
  is an immutable snapshot in a `@Volatile` field (sora calls
  `requireAutoComplete` on a background thread). `SqlSchemaLoader` (scope =
  composition) does: `dbTables(schema)` → publish tables → load columns for
  the first **N = 50** tables with `Semaphore(4)` concurrency (`async` per
  table, each publishes a new snapshot) → remaining tables load **lazily**:
  when `completionAt` yields a qualifier/prefix equal to an unloaded table
  name, `ensureColumns(name)` fires once (de-duplicated set) and the next
  keystroke sees the result. Views count as tables. Cancelled on dispose. A
  failed table call ⇒ keywords-only + a dismissible notice (FR-6.5); a failed
  single `columns` call ⇒ that table is skipped silently.
- **D11 Which schema**: `DbRoute.Sql(conn, schema)`; `TablesView` hoists the
  schema picker state so the SQL button passes it (and `DbRef.sql` stores it).
  If the route has none (restore from older state, or SQLite) the loader uses
  `defaultSchema(engine, dbSchemas())` (SQLite: ""). Queries typed against
  another schema are still valid SQL; completion just does not know that
  schema (documented limitation).
- **D12 Keyword list**: constant lists in `DbCompletion.kt`: one shared ANSI
  set plus small per-engine extras (`ILIKE`, `RETURNING` for postgresql;
  `TOP`, `NOLOCK` for mssql; `PRAGMA`, `AUTOINCREMENT` for sqlite;
  `LIMIT`, `SHOW`, `DESCRIBE` for mysql/mariadb). Uppercase insertion.

## 3. Round 4 component map

New (all under `android/app/src/main/java/dev/devstationone/`):

| File | Contents |
|---|---|
| `ui/DbCompletion.kt` | `completionAt`, `aliasMap`, `suggest`, `insertText`, keyword sets, `SqlSchema`, `SqlSchemaLoader`, `SqlLanguage` |
| `ui/DbSqlEditor.kt` | `SqlEditorHandle`, `SqlEditor` composable (sora in `AndroidView`) |
| `ui/TextmateSetup.kt` | `setUpTextmate` moved out of `CodeScreen.kt` (now `internal`) |
| `ui/ProjectListLogic.kt` | `sortedProjects`, `runtimeOptions`, `filterProjects` |

Changed: `data/Data.kt` (`UiStateStore`), `ui/kit/Kit.kt` (`MuxDropdown`,
`DropdownOption`), `ui/Workspace.kt` (tab init/persist, `WORKSPACE_TABS`),
`ui/DatabasesScreen.kt` (restore, `DbRef` persist, hoisted schema, structure
dropdown, schema picker → `MuxDropdown`), `ui/DbConnectionForm.kt` (engine
dropdown), `ui/DbSqlScreen.kt` (uses `SqlEditor`), `ui/DbLogic.kt`
(`DbRoute.Sql.schema`, `DbRef`, `refsOf`, `restoreStack`, `ENGINE_OPTIONS`),
`ui/Screens.kt` (`ProjectsScreen` filter + sort, server-removal cleanup),
`ui/CodeScreen.kt` (import moved `setUpTextmate`), docs.

Unchanged: all `Api`/`DbApi` calls, server, runtime, openapi.

## 4. Round 4 flows

```
Open project ─► Workspace(tab = store.tab ?: 0) ─► tab 4? ─► DatabasesScreen
                                                      │
                          refs = store.db(key) ───────┤ empty → Connections
                                                      ▼
                     Connections (spinner) ─ fetch conns ─ for ref in refs:
                       validate(one call) ─ ok → push route
                                           └ fail → stop, notice, keep ancestor
SqlView opens ─► SqlEditor(handle) + SqlSchemaLoader(conn, schema)
                    tables ─► publish ─► columns(50, ×4) ─► publish…
keystroke ─► sora ─► SqlLanguage.requireAutoComplete(bg thread)
                    completionAt → aliasMap → suggest(snapshot) → publisher
```

## 5. Round 4 error handling and security

- Restore/validation failures fall back one level; never throw to the UI.
- Corrupt stored JSON ⇒ treated as empty (`runCatching`, like `ShortcutStore`).
- Stored data = ids and names; no secrets, SQL, values, filters (NFR-2). A
  schema/table name is not sensitive beyond what the lock already hides
  (user decision: plain storage, not cleared on lock).
- Completion data (table/column names) lives in memory only, is never logged,
  and is dropped when the SQL screen leaves composition.
- Schema loading uses existing authorized routes; concurrency is bounded (4)
  and cancelled on leave, so it cannot flood the runtime (NFR-5). Total worst
  case: 1 + 50 calls per SQL-screen open.

## 6. Round 4 testing

- **Unit** (`DbLogicTest` + new `DbCompletionTest`, `ProjectListLogicTest`):
  `refsOf`/`restoreStack` (missing connection, missing table, grants missing
  user, Form skipped, order preserved); tab range clamp; `sortedProjects`
  (case, ties); `runtimeOptions` (only runtimes with projects, online flag),
  `filterProjects`, unknown stored runtime ⇒ All; `completionAt` (prefix,
  qualifier, quoted qualifier, empty); `aliasMap` (`FROM t a`, `JOIN t AS a`,
  reserved-word guard, schema-qualified); `suggest` ordering/cap/case;
  `insertText` quoting per engine; `ENGINE_OPTIONS` ⊆ known labels; unknown
  engine label.
- **No instrumented UI tests exist**; the new Compose pieces get the project's
  usual on-device E2E (below).
- **On-device E2E** (Pixel 8a, uiautomator dump + logcat): open Files, back,
  reopen ⇒ Files; kill app, reopen ⇒ Files; Data screen restore incl. Back
  chain; restore with a renamed table (fallback + notice); project list order
  and runtime filter incl. restart and "Show all"; engine dropdown (SQLite
  hides fields; existing connection disabled); Structure dropdown counts; SQL
  editor highlighting, `sel`→`SELECT`, `FROM or`→`orders`, `orders.`→columns,
  alias, runtime offline ⇒ keywords-only + notice.

## 7. Round 4 slices (each vertical: code + tests + docs + CodeGraph + commit; E2E then closes it)

1. **`MuxDropdown` + engine and structure dropdowns** (FR-4, FR-5).
2. **Project list sort + runtime filter** (`ProjectListLogic`, `UiStateStore.filter`, server-removal cleanup) (FR-3).
3. **Tab memory** (`UiStateStore.tab`, `WORKSPACE_TABS`) (FR-1).
4. **Databases route restore** (`DbRef`, validate-then-push, persist) (FR-2).
5. **SQL editor on sora, no completion** (`SqlEditor`, `TextmateSetup`, highlighting) (FR-6.1, 6.6).
6. **SQL completion** (`DbCompletion`, schema loader, schema hoisting, ranking, notice) (FR-6.2-6.5).

No server deploy is needed for any slice; each ships as a release APK
(`assembleRelease`, in-place update, no re-pair).

## 8. Round 4 risks

- **sora completion on the background thread**: reading the `@Volatile`
  immutable snapshot avoids locking; the popup is rebuilt on each keystroke
  and cheap at ≤ 50 items. Keep `suggest` allocation-light.
- **`AndroidView` height/IME**: fixed `180.dp` editor + existing
  `imePadding` at the workspace; verify with the keyboard up on a small
  display (CodeScreen precedent works full-height only).
- **Restore race**: user taps before restore finishes — restore only pushes if
  the stack is still the initial `[Connections]`; any user navigation cancels
  it (no surprise screen jumps).
- **Alias regex** will misparse some SQL (subqueries, CTEs, quoted aliases):
  failure mode is "offers all columns", never a wrong insert; documented.
- **50-table cap with 4× concurrency** can still be slow on a remote
  database; the editor stays usable (keywords + names first) and the cap is a
  single constant.
- **Dropdown a11y**: confirm TalkBack reads state; if `Role.DropdownList` is
  unsatisfactory use `Role.Button` + explicit state text.

## 9. Round 5 — MariaDB engine (ships after round 4, own slices)

Moved to its own document: `docs/DESIGN_ANDROID_ROUND5.md` (decisions E1-E9,
component map, testing, slices, risks). Round 4 only touches MariaDB where
round 5 will later add it (engine options, keyword/quoting helpers); round 4
code must not assume the engine list is closed (finding 7 above, D5).

## 10. Open items for implementation (none block starting)

- Exact keyword lists and per-engine extras (D12) — written during slice 6.
- N = 50 / concurrency 4 are defaults; revisit after on-device timing on a
  large schema.
- Whether to also move the schema picker to `MuxDropdown` in slice 1 (design
  says yes, for one dropdown behavior); drop it if slice 1 grows.

## 11. Status

Round 4: slices 1-6 **implemented 2026-10-11** (unit tests green, release APK
smoke-tested on the Pixel 8a: project sort + runtime filter incl. restart, tab
memory incl. restart, Databases route restore, engine dropdown, SQL editor
screen). Deviations: the SQL **Run** button is a play icon (`MuxIcons.Play`,
a11y label "Run") per user request; the "no projects for this runtime" empty
state of FR-3.4 is unreachable because D7 lists only runtimes that have
projects, so it was not built. The completion popup is canvas-drawn and
not visible to uiautomator; its logic is covered by `DbCompletionTest`.
Round 5: tabled by the user for a later date (see DESIGN_ANDROID_ROUND5.md §7).
