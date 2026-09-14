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

## Dashboard events

```text
Browser dashboard
      |
      | WSS (JSON events, one connection per browser tab)
      v
Control Server   -- internal/events.Bus fans out to every subscribed
      |              browser, scoped by user ID (nil = broadcast); an
      |              in-process pub/sub, same single-process scope as
      |              runtimes.Registry and sessions.Hub (ADR-012)
      |
      +-- runtime online/offline (registry.Register/Unregister) -> broadcast
      +-- session attach/detach/exit -> scoped to the project's owner
```

Not a general event bus for arbitrary application state — just the two
triggers above (Slice 14) plus `notification.created` (Slice 15, below).
Payloads are intentionally thin (an ID, not a full resource) for the first
two — a receiving browser is expected to refetch the relevant REST resource
rather than trust a payload that may represent several coalesced changes.
`notification.created` is the one exception: it carries the full
notification, since a notification's fields are immutable after creation
(other than `read_at`), so there is nothing to coalesce. See `docs/API.md`
"Dashboard events" for the wire format.

## Notifications

```text
                          sessions.Hub.Forward (every runtime output byte)
                                |
                                v
                    lastActivity[id] = now(); idleNotified[id] cleared
                                |
      cmd/devstationone-server idleNotifyLoop (10s tick)
                                |
                    Hub.IdleSessions(threshold) -- idle, not yet flagged
                                |
                                v
              notifications.Record(pool, bus, ownerID, type, ...)
                     |                                |
                     v                                v
              INSERT notifications                events.Bus.Publish
                                                  ("notification.created")
```

`internal/agent/manager.go`'s `MessageSessionExit` handling
(`internal/api/runtime_ws.go`) calls the same `notifications.Record` for
`task.completed`, outside the idle loop — a session exit is an immediate
event, not something to poll for.

The idle heuristic's state (`lastActivity`, `idleNotified`) lives on
`sessions.Hub` itself, not a separate tracker, because `Forward` is already
the one place every byte of runtime output passes through. It shares the
same single-process, in-memory scope as the rest of `Hub` and
`runtimes.Registry` (ADR-012). See `docs/DECISIONS.md` ADR-029.

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

## Code tab (live editing)

The Code tab edits files live over a project-scoped WebSocket
(`internal/api/editor_ws.go`). Browser saves and runtime change pushes both
carry whole files (ADR-033): the runtime's edit manager
(`internal/agent/edit_manager.go`) polls mtime+size on the project's open
file set once per second — no inotify — and pushes `code.file.changed`;
the Control Server resolves the project from the runtime+root pair, fetches
the content once via a `code.command` open RPC, and fans it out through
`internal/editor.Hub` to every subscribed editor stream of that project
(`internal/api/runtime_ws.go`). The fan-out handler runs in its own
goroutine because it issues a correlated RPC back to the same runtime,
whose reply only the runtime connection's read loop can resolve.

Saves are last-write-wins: the runtime compares the client's base
mtime/size fingerprint against the disk, and on a mismatch returns the
pre-write content with `stale: true` after writing anyway. Editor file
sizes are capped at 2 MiB (`protocol.MaxEditorFileBytes`), far below the
Files REST path's 24 MB, because autosave puts the whole buffer on the
wire per debounce tick. Open-tab state persists per user+project in
`project_editor_state` (one opaque `jsonb` row, `internal/editor`).

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
      | db.command   decrypting credentials only to build each RPC payload;
      | (RPC)         dispatches test/schemas/tables/columns/indexes/
      v              constraints/triggers/rows/commit/query
Development Runtime  (runtimes.Conn.SendCommand, Envelope.ID-correlated,
      |              same mechanism as service.command), and invalidate
      v              (fire-and-forget, SendEnvelope) after an edit/delete.
      |            -- internal/agent/db_manager.go caches one *sql.DB per
      |              connection_id (dbManager.pools), opened lazily via
      |              internal/dbconnectors and reused across RPCs; an idle
      v              entry closes after 5 minutes, invalidate closes it now.
PostgreSQL / MySQL / MSSQL / SQLite
```

Slice 7 delivers connection storage, encrypted credentials, and connection
test across all four engines; Slice 8 adds schema discovery (schemas,
tables/views, columns, indexes, constraints, triggers) via each engine's own
catalog metadata, plus the runtime-side connection pool cache the repeated
schema-browsing RPCs need (ADR-021/022) — a plain `map[connectionID]*sql.DB`
kept by `db_manager.go`, not a second session-tracking layer on top of what
Slice 4's terminal sessions already have. Slice 9 adds the data browser
(paginated/sorted/filtered rows, staged insert/edit/delete committed as
parameterized statements) and the SQL Editor (arbitrary SQL, the same
trust model as the terminal — `docs/SECURITY.md`, ADR-021). A `filter`
expression and the SQL Editor's own text are the two places raw,
unparameterized SQL is deliberately accepted from the browser; every other
value in this slice — row edits included — is bound, never interpolated.
Export (CSV/JSON/SQL) happens client-side from whatever page of rows is
already loaded, not a separate server-side streaming endpoint. Credentials
are AES-256-GCM sealed at rest under `DEVSTATIONONE_DB_ENCRYPTION_KEY` and
only ever decrypted server-side, immediately before dispatch — never
logged, never returned from an API response (see `docs/API.md` "Databases"
and `docs/SECURITY.md`).

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
- journal notes (`journal_notes`) and their attachments
  (`journal_attachments`, content in `bytea`) — `internal/journal`
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
