# DevStationOne Product Requirements

## 1. Vision

DevStationOne is a self-hosted remote development environment platform.

A user installs the Control Server on an Internet-accessible server and installs Development Runtime on one or more development machines. The user opens the Control Server in a browser and can operate development environments remotely.

## 2. Components

### Control Server

Internet-facing:
- Go
- PostgreSQL
- REST API
- OpenAPI
- authentication
- authorization
- WebSocket gateway
- runtime connection manager
- Tabler web UI

### Development Runtime

Separate application:
- Go
- runs as normal development user
- outbound secure connection
- PTY manager
- filesystem manager
- systemd user-service manager
- database manager

### Browser

Only MVP client.

Future:
- Android
- Linux desktop
- Windows desktop

## 3. Projects

A Project is the primary workspace.

Required fields:
- ID
- name
- runtime ID
- user
- working directory
- shell
- environment configuration
- created/updated timestamps

A Project may have unlimited simultaneous Sessions.

Project session creation must use the stored Project working directory.

Example:
```text
Project: My Application
Runtime: dev01
User: brian
Working directory: /home/brian/src/my-application

Sessions:
  Terminal
  Claude Code
  Dev Server
```

This naturally supports project-local:
- `CLAUDE.md`
- `.claude/`
- skills
- MCP configuration
- source code
- package managers
- virtual environments

## 4. Sessions

A Session is a persistent interactive PTY.

Requirements:
- create
- rename
- attach
- detach
- reconnect
- close
- resize
- signals
- process lifecycle
- status
- timestamps
- multiple concurrent sessions per Project

Browser disconnection must not terminate a session.

## 5. File Manager

Webmin/Filemin-inspired.

Capabilities:
- directory tree/list
- breadcrumbs
- stat
- create file/directory
- edit text
- syntax highlighting
- rename
- copy
- move
- delete
- upload
- download
- search
- archive/extract where supported
- Markdown preview
- image preview

The UI is a tab within the Project workspace.

## 6. Database Manager

CloudBeaver-inspired.

Supported:
- PostgreSQL
- MySQL/MariaDB
- Microsoft SQL Server
- SQLite

Capabilities:
- connection management
- test connection
- catalogs/databases
- schemas
- tables
- columns
- indexes
- constraints
- views
- triggers where supported
- data browser
- filters
- sorting
- pagination
- insert/edit/delete
- SQL editor
- results
- export
- user/role management where supported
- permissions where supported

Database connections execute from the Development Runtime so private/localhost databases do not need Internet exposure.

## 7. Services

Use `systemd --user` where available.

Capabilities:
- list
- status
- start
- stop
- restart
- enable
- disable
- logs

No root required for normal operation.

## 8. Runtime capability map

Runtime reports:
- hostname
- OS
- architecture
- runtime version
- runtime user
- capabilities
- database drivers
- systemd user availability

Example:
```json
{
  "terminal": true,
  "filesystem": true,
  "services": true,
  "databases": ["postgresql", "mysql", "mssql", "sqlite"]
}
```

UI adapts to capabilities.

## 9. UI

Use Tabler folded sidebar:
https://preview.tabler.io/layout-folded.html

Desktop:
- top navbar
- foldable/pinnable left navigation
- large workspace
- tabbed project interface

Mobile:
- offcanvas navigation
- touch-friendly controls
- horizontally scrollable tabs
- terminal uses maximum viewport
- no desktop-only critical function

Top navigation:
- Dashboard
- Projects
- Runtimes
- Activity
- Settings
- User menu

Sidebar/tool navigation:
- Projects
- Files
- Databases
- Services
- Audit

## 10. API

Everything the browser can do is available through OpenAPI.

Domains:
- authentication
- users
- devices
- runtimes
- projects
- sessions
- files
- databases
- services
- audit
- settings

## 11. Privacy

The customer owns and operates the Control Server, database, and development machines.

No vendor-hosted relay is required.

The Control Server is self-hosted and contains customer data.

## 12. Security

- strong authentication
- secure browser sessions
- runtime cryptographic identity
- runtime revocation
- resource authorization
- project scoping
- filesystem containment
- safe process execution
- protected database credentials
- audit logging
- rate limiting
- CSRF protection as applicable
- secure WebSocket origin/session validation
- no sensitive values in logs

## 13. MVP non-goals

Do not implement native clients yet.
Do not implement SaaS hosting.
Do not implement Claude-specific APIs.
Do not implement Kubernetes/container management.
Do not make database access depend on direct Internet connectivity.
