# Requirements: Docker / Compose tab on project workspace

Status: requirements agreed via brainstorm 2026-09-15. Architecture (ADR) and
implementation slices follow separately.

## Goal

Add a **Docker** app tab to `web/static/project.html` for managing the
project's containers and compose stack through the Development Runtime.
The runtime talks to the local Docker daemon via
`github.com/moby/moby/client` over the local socket.

## Decisions made

| Question | Decision |
|---|---|
| Scope | Containers + compose core; images/volumes/networks read-only |
| Compose engine | `docker compose` CLI plugin subprocess (moby client has no compose API) |
| Daemon access | moby client over local docker socket on runtime host |
| Visibility | Project-scoped only (compose project containers + the project's own containers) |
| Compose file | Auto-detect in project working directory |
| Container logs | Stream (`docker logs -f`) over WS; not polling |
| Compose up output | Bounded RPC (extended timeout), no live build output |
| Container exec | Excluded — `docker exec -it` in the Terminal tab already covers it |
| Stopped container logs | Viewable — same endpoint; follow ends at retained-log end |

## Functional requirements

### FR1 — Compose stack view
- Runtime auto-detects `compose.yaml`, `compose.yml`, `docker-compose.yml`,
  `docker-compose.yaml` in the project working directory (containment-checked).
- No compose file → tab shows an empty state ("no compose file in this
  project"); container list still available.
- Compose operations: `up -d` (build), `down`, `stop`, `restart`, `ps`/status.
  Output of `up`/`down` shown to the user (pull/build progress or final result).

### FR2 — Container list (project-scoped)
- Show containers belonging to the detected compose project
  (`com.docker.compose.project` label) plus any containers whose mounts bind
  the project working directory. Anything else on the host is not shown.
- Columns: name, image, state (running/stopped/paused/exited/restarting),
  ports, health, compose service name. Sortable datatable, mobile-responsive
  (existing `.files-table`/`.db-data-table` dense-table pattern).

### FR3 — Container lifecycle
- Start / stop / restart / delete per container. Delete offers remove-volumes
  choice. Destructive actions require confirmation (existing modal pattern),
  state the container name in the dialog.

### FR4 — Container logs
- Streamed with follow (`docker logs -f`) over WebSocket while the tab is
  open; stops streaming when the tab is closed or hidden.
- Tail last N lines on connect before following. Timestamps toggle.
- No terminal I/O is ever logged server-side (existing rule applies to
  container stdout).

### FR5 — No built-in exec (deliberately excluded)
- The Terminal tab is a real PTY in the project working directory where
  `docker` is on PATH and the runtime user holds socket access, so
  `docker exec -it <container> bash` already works there with the existing
  session-survival machinery. A built-in exec session type would add a
  protocol session type, moby ExecAttach plumbing, and container-ID scope
  validation for the price of one saved keystroke. Excluded; container
  rows may show the exec command as a copyable hint.

### FR6 — Images / volumes / networks (read-only)
- Lists only: image (tag, size, created), volume (name, driver, size where
  supported), network (name, driver) — filtered to resources referenced by
  the project-scoped containers. No create/delete/pull operations.

### FR7 — Capability reporting
- Runtime reports docker availability in `protocol.Capabilities` (socket
  reachable, daemon version, compose plugin present). Control Server exposes
  it; tab renders "Docker not available" state when absent.

## Non-functional requirements

- **Security**
  - Runtime user requires docker socket access (docker group or rootless
    docker). This is effectively host-root-equivalent; document in
    SECURITY.md as an explicit deployment trust boundary.
  - Container ID / service name / file path from the client are never
    trusted: project scope is resolved server-side; runtime re-validates
    containment independently (compose file must resolve inside the project
    working directory).
  - Compose subprocess: fixed argv, no shell interpolation of client input;
    project name derived from the compose file, not accepted from the client.
- **Transport**: same outbound runtime connection + WebSocket gateway as
  existing features; no direct browser-to-docker.
- **Timeouts**: compose lifecycle RPCs get a longer timeout than the 15 s
  service RPC (builds are slow); streaming endpoints are WS, not polled RPC.
- **Audit**: lifecycle actions (compose up/down, container start/stop/rm)
  recorded in the audit log like service actions.

## User stories

1. As a developer, I open a project with a compose file and see its services
   and containers with live state, without leaving the workspace.
2. As a developer, I start/stop/restart the whole stack with one click and
   watch `docker compose up -d` output.
3. As a developer, I follow a container's logs live while starting a dev server.
4. As a developer, I click a running container and can copy its `docker exec`
   command, then paste it into the project Terminal tab to get a shell in it.
5. As a developer, I see an honest empty state when the runtime host has no
   Docker or the project has no compose file.

## Acceptance criteria

- With Docker + compose plugin on the runtime host: list, lifecycle, logs,
  with a real daemon (integration tests +
  manual smoke).
- Without Docker: tab shows the not-available state; no other tab regresses.
- Compose file outside the project working directory or absent: empty state;
  no traversal escape (filesystem security test).
- Non-project containers never appear in the list (test with a decoy
  container running on the host).
- `make fmt vet lint test`, openapi lint, frontend build, browser E2E for
  list/lifecycle/logs pass.

## Resolved questions

1. **Compose up progress** — bounded RPC with an extended timeout (builds
   are slow); result + final output at completion. Live streaming is a
   later addition only if slow builds actually annoy.
2. **Exec session placement** — no built-in exec at all. The Terminal tab
   already provides `docker exec -it` as an ordinary PTY; the Docker tab
   shows the command as a copyable hint.
3. **Stopped container logs** — viewable via the same endpoint; follow
   ends when retained log ends.