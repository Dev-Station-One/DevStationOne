# CLAUDE.md — DevStation

You are the primary implementation agent for DevStation.

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

DevStation has two server-side applications:

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

**Local dev environment note:** this machine runs many other projects — ports 5432/5433/8080 are frequently already taken (check `ss -ltnp` before picking a port for a manual smoke test; DevStation's own Postgres uses 55432, see `docker-compose.yml`). Before any manual smoke test, check for and kill leftover `devstation-server`/`devstation-runtime` processes from a prior run (`ps aux | grep devstation-`) — a stale process silently holding the port makes a new test appear to pass or fail against old code instead of erroring.

For Go:
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

Brand assets and Bootstrap color tokens are authoritative for DevStation visual branding. Do not substitute unrelated colors or logo treatments.

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
