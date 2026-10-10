# CLAUDE.md — DevStationOne

You are the primary implementation agent for DevStationOne.

Read these files before implementing anything:
- `docs/PRD.md`
- `docs/ARCHITECTURE.md`
- `docs/API.md`
- `docs/PROTOCOL.md`
- `docs/IMPLEMENTATION_PLAN.md`
- `docs/SECURITY.md`
- `docs/WEB_UI_DESIGN.md`
- `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`

The two UI documents are mandatory for all frontend work. They are authoritative for
visual hierarchy, workspace layout, component anatomy, responsive behavior, and UI
state presentation. Do not invent a competing visual system or silently simplify a
required state. If implementation constraints require a deviation, document the
deviation and preserve the product intent.

## Mission

Build a production-quality self-hosted remote development environment platform.

DevStationOne has two server-side applications:

1. **Control Server** — Internet-facing Go application containing the REST/OpenAPI API, authentication, authorization, PostgreSQL persistence, WebSocket gateway, runtime connection manager, and Tabler web UI.
2. **Development Runtime** — Go application installed on a development machine and running as the normal development user. It establishes an outbound authenticated connection and provides PTYs, filesystem access, systemd user services, and database connectors.

The MVP client is the browser.

## Absolute implementation rules

### No stubs

Never introduce:
- TODO implementations
- placeholder handlers
- fake success responses
- hard-coded demo data
- empty methods
- panic("not implemented")
- "coming soon" behavior for required completed functionality
- interfaces whose production implementation does not exist
- mocked production paths

Tests may use mocks/fakes where appropriate. Production functionality must be real.

### One-pass vertical implementation

"One pass" means one complete vertical application/slice at a time.

For every slice:
1. Read requirements.
2. Inspect repository.
3. Inspect CodeGraph.
4. Identify impacted packages/symbols.
5. Design the smallest complete vertical slice.
6. Implement all required layers.
7. Add migrations.
8. Add API/OpenAPI definitions.
9. Add protocol definitions if applicable.
10. Implement runtime behavior.
11. Implement UI behavior if applicable.
12. Add automated tests.
13. Run formatters.
14. Run static analysis.
15. Run unit tests.
16. Run integration tests.
17. Run frontend tests/build.
18. Run API contract validation.
19. Run a real smoke test.
20. Inspect logs.
21. Update documentation now, in this pass — API.md, ARCHITECTURE.md, DECISIONS.md (new ADRs), IMPLEMENTATION_PLAN.md status, any doc the slice touched. Do not defer this to a follow-up turn after CodeGraph or the commit.
22. Update CodeGraph only after step 21 is done — CodeGraph must index docs that are already current, not stale ones.
23. Review git diff.
24. Search for accidental stubs/TODOs.
25. Verify acceptance criteria.
26. Commit the completed slice — documentation changes are part of the slice's commit, never a separate later one.

Never claim a slice is complete because it merely compiles. A slice is also not complete while its documentation update is still pending.

### CodeGraph is mandatory

Use the CodeGraph tooling available in the development environment.

First inspect the installed implementation instead of guessing commands:

```bash
codegraph --help
codegraph version
```

Use the commands supported by the installed version.

Maintain a repository graph continuously. Before substantial changes, inspect the graph and use impact/dependency queries when available.

At the end of each completed slice:
```bash
./scripts/codegraph-maintain.sh
```

The script must detect the installed CodeGraph CLI and run the appropriate status/index/synchronization workflow. Do not invent unsupported CodeGraph commands.

The CodeGraph index is part of repository maintenance and must never be intentionally left stale after a completed slice.

### Testing

**Local dev environment note:** this machine runs many other projects — ports 5432/5433/8080 are frequently already taken (check `ss -ltnp` before picking a port for a manual smoke test; DevStationOne's own Postgres uses 55432, see `docker-compose.yml`). Before any manual smoke test, check for and kill leftover `devstationone-server`/`devstationone-runtime` processes from a prior run (`ps aux | grep devstation-`) — a stale process silently holding the port makes a new test appear to pass or fail against old code instead of erroring.

For Go:
- `make fmt vet lint test` runs the full local Go gate; `make openapi-lint` validates docs/openapi.yaml
- `gofmt`
- `go vet`
- project-appropriate lint/static analysis
- unit tests
- integration tests
- integration tests share one dev Postgres (`make db-up`) — always run `go test -p 1 ./...` (already the `make test` target), never bare `go test ./...`, or cross-package `TRUNCATE`s race
- race tests for concurrency-sensitive packages where practical

For web:
- formatter/linter
- typecheck
- unit/component tests
- production build
- browser E2E tests for important user workflows

For API:
- OpenAPI validation
- endpoint integration tests
- authentication/authorization tests

For runtime:
- real PTY tests
- reconnect tests
- resize tests
- process lifecycle tests
- filesystem security tests
- database connector tests
- systemd user integration tests when available

### Android client builds

- System gradle is 4.4.1 (unusable). Build with the cached dist:
  `cd android && ANDROID_HOME=$ANDROID_SDK_ROOT ~/.gradle/wrapper/dists/gradle-8.14.3-bin/*/gradle-8.14.3/bin/gradle assembleDebug`
- Only one SDK location may be set: use `ANDROID_SDK_ROOT=/home/btafoya/android_sdk` —
  setting ANDROID_HOME to `/home/btafoya/Android/Sdk` at the same time fails the build.

### Android release signing

- `android/keystore.properties` + `android/keystore/release.jks` (gitignored) sign release builds;
  `gradle :app:assembleRelease`, verify with `apksigner verify --print-certs`.
- Switching an installed debug build to release needs `adb uninstall` (signature mismatch), which
  wipes the Android-Keystore device key — the phone must re-pair afterward (`devstationone-admin
  pair`). Back up the keystore; it is permanent.

### Phone E2E

- Pixel 8a attached over TLS adb (`adb devices`). `adb exec-out screencap` returns black
  (FLAG_SECURE) — drive UI with `adb shell uiautomator dump /sdcard/ui.xml` (extract text/bounds,
  then `adb shell input tap`) and confirm behavior via `adb logcat`, not screenshots.

### Compose note

- Compose: `Modifier.size(0)` / `clipToBounds` does not reliably hide composed content (children
  keep real bounds; drawing/semantics leak). Hide composed-but-alive subtrees by measuring
  full-size and never placing them — `Modifier.layout`, see `ui/Workspace.kt hiddenOther`.

### Security

Treat terminal, process, filesystem, database, and service operations as high-impact capabilities.

Never trust client-provided:
- filesystem paths
- runtime IDs
- project IDs
- session IDs
- database IDs
- shell paths
- environment variables
- service names

Authorize resources server-side.

The runtime must enforce filesystem containment independently of the Control Server.

Never log:
- terminal input/output
- file contents
- passwords
- database credentials
- authentication tokens
- private keys
- sensitive environment values

### Terminal

Terminal sessions must be real PTYs.

A Project can have multiple simultaneous sessions.

Each session:
- has a stable ID
- belongs to exactly one Project
- starts in the Project working directory
- runs with the Project runtime user
- supports terminal dimensions
- supports signals
- survives browser disconnects
- can be reattached
- reports lifecycle state

Do not implement terminal streaming through database polling.

Claude Code is not a special integration. It is an ordinary process. If the Project directory and environment are correct, Claude Code should see its project-local files naturally.

### Projects

A Project defines execution context:
- name
- runtime
- OS user
- working directory
- shell
- environment policy
- optional service configuration
- metadata

The client requests a Project ID; it must not be able to override the authoritative working directory during session creation.

### Development Runtime

The runtime:
- runs as a normal user
- connects outbound to Control Server
- supports reconnect
- reports capabilities
- manages PTYs
- performs filesystem operations
- manages `systemd --user`
- manages database connections
- never requires root for ordinary operation

### Services

Use user-level systemd where available:
```bash
systemctl --user
```

Do not require root.

### Files

Implement real filesystem functionality:
- list
- stat
- create file
- create directory
- read
- write
- edit
- rename
- copy
- move
- delete
- upload
- download
- search where specified

Prevent path traversal and symlink-based root escapes.

### Databases

MVP:
- PostgreSQL
- MySQL/MariaDB
- Microsoft SQL Server
- SQLite

Implement real:
- connection management
- connection test
- database/catalog discovery
- schema discovery
- table discovery
- columns
- indexes
- constraints
- views/triggers where supported
- data browser
- filtering
- sorting
- pagination
- editing
- SQL editor
- query results
- user/role/permission management where supported

SQLite does not have server-side users/roles; represent that honestly.

### UI

Before any frontend implementation or modification, read:
- `docs/WEB_UI_DESIGN.md`
- `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
- `docs/BRAND_ASSETS.md`
- `docs/BOOTSTRAP_COLORS.md`

Brand assets and Bootstrap color tokens are authoritative for DevStationOne visual branding. Do not substitute unrelated colors or logo treatments.

Use current Tabler 1.5.x and Bootstrap 5.3-compatible patterns.

Reference:
https://preview.tabler.io/layout-folded.html

The UI must be fully responsive and mobile-first.

The terminal must use a mature browser terminal emulator such as xterm.js rather than a custom terminal renderer.

Project workspace concept:
```text
Project
 ├── Terminal tabs
 │    ├── bash
 │    ├── Claude Code
 │    └── dev server
 ├── Files
 ├── Databases
 └── Services
```

Do not confuse application tabs with terminal sessions.

Routes the Android client calls must be registered with `requireIdentity` (device Sig header,
cookie fallback) — `requireSession` is cookie-only and 401s every device-signed request
(ADR-053). Check `internal/api/server.go` when adding client-facing endpoints.

### API-first

Every web capability must be exposed through the OpenAPI-described API.

`docs/openapi.yaml` is the canonical HTTP contract.

WebSocket terminal transport is documented in `docs/PROTOCOL.md`.

Future native clients must not require backend rewrites.

### Git

Before each commit:
```bash
git status
git diff --check
```

Keep commits coherent. Do not mix unrelated refactors into feature commits.

## Dev instance

A persistent development Control Server runs on this machine as a systemd
user service (`devstationone-dev.service`, enabled with linger — survives
logout and reboot):

- Upstream: `http://192.168.25.165:18091` (LAN)
- Public: `https://ds1.premadev.com` (external TLS-terminating proxy pointed at the upstream)
- Binary: `bin/devstationone-server` (gitignored). The service does not rebuild itself — after changing server code run `go build -o bin/devstationone-server ./cmd/devstationone-server && systemctl --user restart devstationone-dev.service`
- Logs: `journalctl --user -u devstationone-dev.service -f`
- Seeded dev user: `dev@local.test` — re-create with `bin/devstationone-admin --email dev@local.test` if the `users` table is ever empty (the `devstationone-admin` CLI, ADR-019; there is no env-var bootstrap anymore). It needs the same `DEVSTATIONONE_DATABASE_URL`/`DEVSTATIONONE_DB_ENCRYPTION_KEY` as the server, but those live only in `devstationone-dev.service`'s `Environment=` lines (systemd scopes them to that unit, not your shell) — pull them out first: `set -a; . <(systemctl --user cat devstationone-dev.service | grep '^Environment=' | sed 's/^Environment=//'); set +a` — then `echo '<password>' | bin/devstationone-admin --email dev@local.test`. Same pattern applies to any other devstationone instance deployed this way (docs/DEPLOYMENT.md "systemd (recommended)").
- Integration tests (`make test`) use a separate test-only Postgres (`make dbtest-postgres-up`, port 55433, ADR-031) and `TRUNCATE ... CASCADE` freely there — they no longer touch the dev instance's database on 55432, so this seeded user survives test runs

## Prod deploy (dev.brian.one)

- `ssh dev.brian.one` / `scp ... dev.brian.one:~/devstationone/` — plain commands; the ssh config
  carries user `btafoya`, port 22283. Do not invent remote usernames or add ssh flags.
- Never use `rsync` to this host: it is shell-aliased with `--password-file` and errors on ssh targets.
- Order matters: copy ALL files (`migrations/ internal/ cmd/ web/ go.mod go.sum`) BEFORE
  `go build` + `systemctl --user restart devstationone.service` — the binary embeds
  migrations and web (go:embed); a build from a stale tree dies at startup ("no migration
  found for version N") and systemd lands in start-limit-hit (`systemctl --user reset-failed`
  to recover).
- Unit: `devstationone.service` (drop-ins live in `devstationone.service.d/` — never touch).
  Health: `curl http://172.30.0.1:18091/healthz` on the host (not 127.0.0.1), then
  `https://ds.premadev.com`, then a behavior check of the actual change.

## Definition of Done

A slice is complete only when:
- production logic exists
- all required layers are integrated
- tests pass
- API/protocol docs are current
- security checks pass
- UI works if applicable
- real smoke test passes
- CodeGraph is current
- documentation is current
- no accidental stubs exist
- acceptance criteria are demonstrably satisfied

## End-of-slice report

Report:
- completed slice
- implementation summary
- files changed
- migrations
- API/OpenAPI changes
- protocol changes
- tests run
- smoke test
- CodeGraph status
- security considerations
- known limitations that are outside the slice
- next slice

Do not call incomplete required functionality a "known limitation."
