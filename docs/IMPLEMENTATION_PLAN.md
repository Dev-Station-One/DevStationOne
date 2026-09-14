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

## Slice 10 — Database users/permissions

Deliver database-specific management for:
- PostgreSQL roles
- MySQL/MariaDB users/grants
- SQL Server logins/users/roles/permissions

SQLite must clearly report that server-style users/roles are not supported.

## Slice 11 — Activity/audit

Deliver:
- audit model
- event capture
- filtering
- UI
- retention policy/configuration
- authorization

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

## Future, explicitly not MVP

- Android client
- Linux desktop client
- Windows desktop client
- additional runtime capabilities
- optional SaaS distribution
- advanced collaboration
