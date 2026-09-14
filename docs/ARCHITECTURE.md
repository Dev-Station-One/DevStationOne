# DevStationOne Architecture

## High-level

```text
                         INTERNET
                            |
                            | HTTPS / WSS
                            v
                 +------------------------+
                 |    DEVSTATION CONTROL  |
                 |                        |
                 | Go API                 |
                 | OpenAPI                |
                 | Auth/AuthZ             |
                 | Web UI                 |
                 | WebSocket Gateway      |
                 | PostgreSQL             |
                 +-----------+------------+
                             |
                       Runtime Protocol
                       outbound TLS/WSS
                             |
                             v
                 +------------------------+
                 |   DEVSTATION RUNTIME   |
                 |                        |
                 | PTYs                   |
                 | Files                  |
                 | systemd --user         |
                 | Databases              |
                 +-----------+------------+
                             |
                  +----------+----------+
                  |          |          |
                 bash      Claude      npm
```

## Control Server

Suggested Go packages:
```text
cmd/devstationone-server
internal/
  api/
  auth/
  authorization/
  config/
  database/
  projects/
  runtimes/
  sessions/
  websocket/
  runtime/
  files/
  databases/
  services/
  audit/
  users/
web/
migrations/
```

The exact structure may change if a stronger Go architecture is justified.

## Runtime

Suggested:
```text
cmd/devstationone-runtime
internal/
  agent/
  connection/
  protocol/
  pty/
  filesystem/
  services/
  databases/
  security/
```

## Runtime connection

Runtime initiates an outbound authenticated TLS connection.

The runtime does not need an Internet-facing port.

The Control Server maintains:
- runtime registration
- heartbeat
- capabilities
- connection state
- revocation

## Terminal

Browser:
```text
Browser xterm.js
      |
      | WSS (binary terminal bytes, one session per socket)
      v
Control Server   -- internal/sessions.Hub routes bytes <-> the runtime's
      |              multiplexed connection, and replays a bounded
      | runtime      scrollback buffer to a (re)attaching browser
      | protocol     (Slice 4; see docs/PROTOCOL.md "Implementation:
      | (multiplexed terminal sessions" for the binary frame format)
      | binary
      | frames)
      v
Development Runtime  -- internal/pty (github.com/creack/pty), one real
      |                 OS PTY per session, output pumped back per-session
      v
PTY
```

PTY bytes are not persisted to PostgreSQL by default.

Persist session metadata only (`terminal_sessions` table: status,
dimensions, exit code — never terminal content).

## Project execution

Client:
```text
POST /api/v1/projects/{projectID}/sessions
```

Control Server resolves:
- runtime
- user
- cwd
- shell
- environment policy

Runtime creates:
- PTY
- process
- correct cwd
- correct user context
- terminal size

The client cannot replace the authoritative cwd.

## Files

Filesystem operations are performed on the runtime (`internal/filesystem`,
Slice 6), executed through `os.Root` opened at the Project's working
directory: all paths are root-relative, the Control Server validates them
for an early `400`, and the runtime re-checks containment independently
(ADR-018). Transfers are base64-encoded inside `file.command`/`file.result`
envelopes, capped at 24 MB per file (PROTOCOL.md).

Both Control Server authorization and runtime containment checks are required.

## Databases

Database connections execute on the runtime.

This supports:
- localhost
- Unix sockets
- private subnets
- databases inaccessible from the Internet

The Control Server brokers requests and results.

```text
Control Server  -- REST (internal/api/databases.go) does CRUD against the
      |             database_connections table (internal/dbconnections),
      | db.command   decrypting credentials only to build the RPC payload;
      | (RPC)         dispatches one db.command per test request
      v              (runtimes.Conn.SendCommand, Envelope.ID-correlated,
Development Runtime  same mechanism as service.command).
      |            -- internal/dbconnectors opens+pings a database/sql
      v              connection for the requested engine, closes it.
PostgreSQL / MySQL / MSSQL / SQLite
```

Slice 7 delivers connection storage, encrypted credentials, and connection
test across all four engines (ADR-021); the runtime never keeps a database
connection open between RPCs in this slice — schema/data browsing and the
SQL editor (Slices 8/9) are a later pass. Credentials are AES-256-GCM
sealed at rest under `DEVSTATIONONE_DB_ENCRYPTION_KEY` and only ever
decrypted server-side, immediately before dispatch — never logged, never
returned from an API response (see `docs/API.md` "Databases" and
`docs/SECURITY.md`).

## Services

Use systemd user APIs/commands where supported.

```text
Control Server  -- REST (internal/api/services.go) resolves the Project's
      |             runtime connection, dispatches one service.command RPC
      | service      per request (runtimes.Conn.SendCommand, correlated by
      | command      Envelope.ID — Slice 5's first user of that field),
      | (RPC)        and waits for the matching service.result/error.
      v
Development Runtime  -- internal/services runs real systemctl --user /
      |                 journalctl --user commands (os/exec, no shell,
      v                 never root — SECURITY.md).
systemd --user
```

Services are not a stored resource: there is no database table, and every
request reflects the runtime's live state at call time (see
`docs/API.md` "Services" and `docs/PROTOCOL.md` "Implementation: service
commands").

## Persistence

PostgreSQL stores:
- users
- devices
- runtimes
- runtime capabilities
- projects
- terminal sessions (`terminal_sessions`; the browser-auth `sessions` table
  is a distinct, pre-existing concept — see ADR-015)
- database connections (`database_connections`, credentials encrypted —
  see ADR-021)
- database connection metadata
- audit events
- settings

Sensitive credentials require protected storage.

## Web UI

Tabler 1.5.x folded layout.

Use a mature terminal emulator such as xterm.js.

The UI must consume the public API rather than reaching directly into application internals.

## Deployment

Control Server:
- Go binary
- PostgreSQL
- static assets
- reverse-proxy/TLS friendly

Runtime:
- Go binary
- systemd --user service
- outbound network only

## Observability

- structured logs
- request IDs
- runtime IDs
- session IDs
- health/readiness
- metrics where appropriate
- audit events

Never log secrets, terminal streams, file contents, or database credentials.
