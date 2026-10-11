# DESIGN_ANDROID_ROUND5.md — Round 5: MariaDB engine

Implements the "Round 5" section of `docs/REQUIREMENTS_ANDROID_ROUND4.md`.
Status: **design draft 2026-10-11** — no code yet. Ships **after round 4**
(`docs/DESIGN_ANDROID_ROUND4.md`) as its own slices, per the user. Despite the
filename it is a full-stack round: migration, API, runtime, web and Android.

## 1. Verified findings

Read from the code on 2026-10-11.

1. **`mysql` already serves MariaDB servers today**: one `mysqlConnector`, the
   web label is "MySQL / MariaDB" (`web/static/project.html:798,1601`), and
   `internal/agent/backup_manager.go:353` already falls back to
   `mariadb-dump`. This round therefore adds a distinct **identity** (value,
   label, tooling preference, room to diverge), not a functional capability
   gap. The user chose the real engine; the cost is recorded so it is a
   conscious one. Keep the round small and boring.
2. **Engine strings leak into many places**: `validEngines` + message
   (`internal/api/databases.go:88,95`), `SupportedEngines`/`For`
   (`internal/dbconnectors/connector.go:229,238`), backup dump/restore
   switches (`internal/agent/backup_manager.go:344,388`), the
   `database_connections.engine` CHECK (`migrations/0009`), the openapi enum,
   the web selector + dialect switch (`project.html:798,1601,2165`;
   `MariaSQL` exists in the vendored CodeMirror 6), and Android
   `engineLabel`/`defaultPort`/form.
3. **Constraint precedent**: `0020` and `0022` drop constraints by their
   Postgres default name; the column-level CHECK in `0009` gets
   `database_connections_engine_check`.
4. **Capabilities**: `hello.Capabilities.Databases` is persisted with the
   runtime row (`internal/runtimes`) and shown through the runtimes API.
5. **Test infra**: MySQL integration tests use `dbtest-mysql` (`mysql:8.4`,
   port 53306, `internal/testdb/testdb.go`, `make dbtest-…`).

## 2. Decisions

- **E1 Value & compatibility**: new engine value `mariadb`. `mysql` rows are
  untouched; no auto-conversion. A MariaDB server saved as `mysql` keeps
  working; users may switch it to `mariadb` on the web (Android keeps engine
  immutable after save).
- **E2 Migration `0024_mariadb_engine`**: drop and recreate the CHECK on
  `database_connections.engine` to include `'mariadb'`. The up migration is
  `ALTER TABLE database_connections DROP CONSTRAINT
  database_connections_engine_check` + `ADD CONSTRAINT … CHECK (engine IN (…))`.
  Verify the name on the dev and prod databases (`\d database_connections`)
  before shipping; if it differs anywhere, switch to a `pg_constraint` lookup.
  Down migration: `UPDATE … SET engine = 'mysql' WHERE engine = 'mariadb'`
  first (data-preserving), then restore the old CHECK — lossy in identity
  only.
- **E3 Runtime connector**: `mariadbConnector` embeds `mysqlConnector` (same
  driver and introspection) so overrides are possible later without a fork;
  `For("mariadb")` and `SupportedEngines()` gain it. Verify during
  implementation that the `mysql.user`/`Super_priv`, `SHOW GRANTS`,
  `CREATE USER … IDENTIFIED BY` paths in `mysql.go` behave on MariaDB 10.6/11
  (expected to; covered by the integration test, not assumed). Adding an entry
  to `hello.Capabilities.Databases` is additive, but check what the web/runtime
  UI does with the list and document it in PROTOCOL.md.
- **E4 Backup tooling**: `backup_manager.go` switches add `case "mariadb"`
  alongside `mysql` using an ordered tool list: for `mariadb` prefer
  `mariadb-dump`/`mariadb`/`mariadb-admin`, fall back to `mysqldump`/`mysql`/
  `mysqladmin`; for `mysql` keep today's order (mysqldump first, mariadb
  fallback). Rationale: MariaDB 11 ships only the `mariadb-*` names by
  default. Implemented as one `mysqlFamilyTools(engine)` helper, not copied
  switches.
- **E5 API/OpenAPI**: `validEngines` gains `mariadb`; the 400 message becomes
  `engine must be one of: postgresql, mysql, mariadb, mssql, sqlite`; openapi
  enum + docs/API.md updated. `dbUserManagementSupported` unchanged (only
  sqlite is excluded).
- **E6 Web UI**: selector option `mariadb` "MariaDB"; `ENGINE_LABELS` (`mysql`
  label changes from "MySQL / MariaDB" to "MySQL"); `dbDialectFor` maps
  `mariadb → D.MariaSQL` (present in the vendored CodeMirror 6); default port
  3306 prefill.
- **E7 Android**: `ENGINE_OPTIONS` gains `mariadb "MariaDB"` right after
  MySQL; `defaultPort("mariadb") = 3306`; `engineLabel`; `insertText` backtick
  quoting and the keyword extras treat `mariadb` as `mysql` (a single
  `isMysqlFamily(engine)` helper in `DbLogic`).
- **E8 Rollout order** (each step independently safe): migrate + deploy server
  → update runtimes → web → Android release. A `mariadb` connection created
  before its runtime is updated fails with "unsupported database engine" —
  surfaced as the normal server error text. An older Android build shows the
  raw `mariadb` string and can open/browse it (server-side behavior only). A
  newer Android against an unmigrated server gets a clean 400 on save.
- **E9 ADR**: new `ADR-054` in `docs/DECISIONS.md` (engine identity, MySQL-
  family helper, migration approach).

## 3. Component map

| Area | Files |
|---|---|
| DB | `migrations/0024_mariadb_engine.{up,down}.sql` |
| Server | `internal/api/databases.go` (`validEngines`, message) |
| Runtime | `internal/dbconnectors/connector.go` (+ `mariadb.go`), `internal/agent/backup_manager.go` |
| Protocol docs | `docs/PROTOCOL.md` (capability list), `internal/protocol/protocol.go` (comment) |
| Web | `web/static/project.html` (selector, labels, dialect, port) |
| Contract/docs | `docs/openapi.yaml`, `docs/API.md`, `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, `docs/SECURITY.md` (engine list), `docs/IMPLEMENTATION_PLAN.md`, `docs/ANDROID_CLIENT.md` |
| Android | `ui/DbLogic.kt`, `ui/DbCompletion.kt` (keywords/quoting), tests |
| Test infra | `docker-compose.yml` `dbtest-mariadb` (`mariadb:11`, port 53307), `internal/testdb/testdb.go`, `Makefile` target |

## 4. Testing

- Migration up/down on a populated table (rows with `mysql` and `mariadb`).
- API: create/patch with `mariadb`; invalid engine message.
- **Connector integration** against a real MariaDB container: test, schemas,
  tables, structure (columns/indexes/constraints/triggers), rows, commit,
  query, principals/grants, backup + restore — table-driven with the existing
  MySQL cases so parity is asserted, not assumed.
- Backup tool selection unit test (which binary is tried first per engine).
- `TestDeviceDatabasesAuth` unchanged (same routes).
- Android: `ENGINE_OPTIONS`, label, port, quoting, keyword set tests;
  on-device E2E: create a MariaDB connection, browse, SQL completion with
  backticks, backup/restore.

## 5. Slices

1. **Migration + API validation + openapi/API.md** (deployable on its own).
2. **Runtime connector + backup tooling + integration tests + compose service.**
3. **Web UI engine** (selector, label, dialect, port).
4. **Android engine option** (`ENGINE_OPTIONS`, helpers, tests) + docs/ADR + E2E.

Each slice is vertical: code + tests + docs + CodeGraph + commit. Slices 1-3
need a server deploy (`scripts/deploy-to-prod.sh`) and a runtime update before
any `mariadb` connection is created; slice 4 ships as a release APK.

## 6. Risks

- MariaDB grant/role differences could surface in `Principals`/`Grants`; the
  integration test is the gate, fixes stay inside the connector.
- A changed `mysql` label on the web may surprise users with MariaDB servers
  saved as `mysql`; the web help text (`web/static/help/backups.md` mentions
  MySQL) gets a one-line note that either engine value works for MariaDB
  servers saved earlier.
- The CHECK-constraint default name could differ if an environment's schema
  was hand-edited; E2 verifies it on dev and prod first and has a
  `pg_constraint` lookup as the fallback.

## 7. Status

Design draft 2026-10-11 for review. Not started; ordered after round 4.
