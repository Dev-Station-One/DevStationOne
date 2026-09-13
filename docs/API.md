# DevStation API

Base path:
```text
/api/v1
```

OpenAPI source:
```text
docs/openapi.yaml
```

## Resource groups

```text
/auth
/users
/devices
/runtimes
/projects
/sessions
/files
/databases
/services
/audit
/settings
```

## Runtimes

```text
POST   /api/v1/runtimes
GET    /api/v1/runtimes
POST   /api/v1/runtimes/{runtimeID}/revoke

WSS    /api/v1/runtimes/connect
```

`POST /api/v1/runtimes` (register) and the `revoke` action require an
authenticated administrator session. `GET /api/v1/runtimes` (list) only
requires an authenticated session, since a non-admin user needs it to select
a runtime when creating a Project (see Projects below). `POST
/api/v1/runtimes` returns the created runtime plus its raw enrollment token;
the token is shown exactly once and only its SHA-256 hash is stored.

`GET /api/v1/runtimes/connect` is where a Development Runtime opens its
outbound WebSocket connection, authenticating with `Authorization: Bearer
<enrollment token>` instead of a session cookie. See `docs/PROTOCOL.md` for
the message envelope exchanged on that connection.

## Projects

```text
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{projectID}
PATCH  /api/v1/projects/{projectID}
DELETE /api/v1/projects/{projectID}

GET    /api/v1/projects/{projectID}/sessions
POST   /api/v1/projects/{projectID}/sessions
```

CRUD requires an authenticated session; the session sub-resource routes
above are implemented in Slice 4 (see Sessions below). CRUD is scoped by
ownership (ADR-013):
administrators see and manage every project, other users only ones they
created (`created_by`). A project the caller does not own responds `404`,
the same as one that does not exist, so the response never confirms another
user's project. `runtime_id` is validated server-side against
`GET /api/v1/runtimes` and rejected (`400`) if the runtime does not exist or
is revoked.

## Sessions

```text
GET    /api/v1/projects/{projectID}/sessions
POST   /api/v1/projects/{projectID}/sessions

GET    /api/v1/sessions/{sessionID}
PATCH  /api/v1/sessions/{sessionID}
DELETE /api/v1/sessions/{sessionID}

POST   /api/v1/sessions/{sessionID}/attach
POST   /api/v1/sessions/{sessionID}/detach
POST   /api/v1/sessions/{sessionID}/resize
POST   /api/v1/sessions/{sessionID}/signal

WSS    /api/v1/sessions/{sessionID}/stream
```

A Session is a persistent, real PTY (ADR-006) running on the Project's
runtime. All routes require an authenticated session and are authorized the
same way as Projects (`authorizedTermSession` resolves the session's
Project and applies the same ownership check as `authorizedProject`,
`404` on mismatch — ADR-013).

`POST /api/v1/projects/{projectID}/sessions` creates the row, then
synchronously sends `session.open` to the Project's runtime over its
existing control-plane connection (Slice 2) and waits up to 10s for
`session.ready`/`session.error`. Responses:
- `201` — the session started; `status` is `detached` (alive, unattached).
- `409` — the Project's runtime is not currently connected.
- `502` — the runtime rejected session.open (e.g. `os_user` does not match
  the runtime's actual OS user — see ADR-014) or the command could not be
  dispatched.
- `504` — the runtime did not respond within the timeout.

Status values (`docs/PROTOCOL.md` lifecycle, plus `failed`):
`requested`, `starting`, `running` (attached), `detached` (alive,
unattached), `exited`, `failed`, `closed`.

`PATCH` renames a session (`{"name": "..."}`) — it is the only mutable
field; working directory/shell/environment come from the Project and
cannot be overridden per-session.

`DELETE` sends `session.close` to the runtime (best-effort — it also
succeeds if the runtime is offline) and marks the session `closed`. This
is a terminal state: a closed session's row is kept for history but can no
longer be attached to.

`POST .../resize` `{"cols": int, "rows": int}` and `POST .../signal`
`{"signal": "SIGINT"|"SIGTERM"|"SIGKILL"|"SIGHUP"|"SIGQUIT"|"SIGTSTP"|"SIGCONT"}`
both require the runtime to be online (`409` if not) and forward directly
to it; resize additionally persists the new size. Signal is for explicit
process control (e.g. a "Force stop" UI action) — normal input, including
Ctrl+C/Ctrl+D as bytes, goes through the WSS stream instead.

`POST .../attach` and `POST .../detach` are control-plane conveniences,
not the attach mechanism itself: opening the WSS stream is what attaches a
browser (and closing it detaches). `attach` just confirms the session is
still live before a client opens the stream (`409` if `exited`/`failed`/
`closed`); `detach` forcibly disconnects whichever browser is currently
attached (e.g. so another device can take over) without closing the
session.

`WSS .../stream` is the terminal byte stream. Opening it attaches the
caller (marking the session `running`) and immediately replays up to 256KB
of recent output (Hub scrollback) so a reattach isn't a blank screen;
closing it marks the session `detached` if the process is still alive.
Messages are **binary-only** WebSocket frames carrying raw terminal
bytes in both directions — no JSON envelope, matching `docs/PROTOCOL.md`'s
binary-safety requirement. `409` (not upgraded) if the session is not in a
live status or the runtime is offline.

## Files

The final OpenAPI contract must define safe operations for:
- list
- stat
- read
- write
- create
- rename
- copy
- move
- delete
- upload
- download
- search

## Databases

The final OpenAPI contract must define:
- connection CRUD
- test
- catalog discovery
- schema discovery
- table metadata
- data browsing
- query execution
- users/roles
- permissions

## Services

```text
GET  /api/v1/projects/{projectID}/services
GET  /api/v1/projects/{projectID}/services/{unit}
GET  /api/v1/projects/{projectID}/services/{unit}/logs
POST /api/v1/projects/{projectID}/services/{unit}/start
POST /api/v1/projects/{projectID}/services/{unit}/stop
POST /api/v1/projects/{projectID}/services/{unit}/restart
POST /api/v1/projects/{projectID}/services/{unit}/enable
POST /api/v1/projects/{projectID}/services/{unit}/disable
```

Services are `systemd --user` units on the Project's runtime (PRD §7). They
are not a stored resource — every request is a live RPC to the runtime
(`internal/services`, real `systemctl`/`journalctl` invocations, no root)
over its existing control-plane connection (see `docs/PROTOCOL.md`
"Implementation: service commands"), so there is no service table or
create/delete route. Authorization reuses `authorizedProject` (ADR-013):
`404` on a project the caller does not own, `409` if the Project's runtime
is not currently connected.

`{unit}` must match `^[A-Za-z0-9:_.@-]+\.service$` (`services.ValidUnitName`)
— checked server-side before ever dispatching to the runtime; `400` if it
doesn't.

`GET .../services` lists every unit the runtime's systemd --user instance
knows about: both currently-loaded units and installed-but-not-loaded ones
merged together, since systemd unloads a stopped unit from its live table
soon after nothing references it (confirmed against a real systemd --user
instance — a project's own service would otherwise disappear from the list
the moment it's stopped). A bare template unit (`app@.service`, no
instance) is excluded — it isn't independently startable.

`GET .../services/{unit}` returns one unit's current status; `404`-shaped
as `502` (bad gateway) if the runtime reports it not found, since "unit
doesn't exist" is the runtime's answer, not the Control Server's.

`.../logs?lines=N` returns the unit's most recent journal lines (`journalctl
--user -u {unit} -n N`, default 200, capped at 2000). An empty array is a
normal result, not an error — it also covers a machine with no per-user
journal namespace at all, which `journalctl --user` reports as "No journal
files were found" rather than failing.

Response shape for every unit-returning endpoint:
```json
{
  "unit": "my-app.service",
  "description": "My App",
  "load_state": "loaded",
  "active_state": "active",
  "sub_state": "running",
  "enabled": "enabled"
}
```
`enabled` is `"enabled"`, `"disabled"`, `"static"` (has no `[Install]`
section — enable/disable are meaningless for it), or `"unknown"`.

## Errors

Use consistent machine-readable errors with:
- HTTP status
- stable error code
- human message
- request/correlation ID where appropriate

Never return secrets in errors.

## Authentication

Browser authentication: server-side session, identified by a random 256-bit token stored in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. The server stores only a SHA-256 hash of the token, never the raw value. Sessions expire 7 days after creation and are individually revocable (`/api/v1/auth/sessions`). See `docs/openapi.yaml` for the concrete contract.

Runtime device authentication: a bearer token issued at enrollment (`POST
/api/v1/runtimes`), presented as `Authorization: Bearer <token>` on the
`/api/v1/runtimes/connect` WebSocket upgrade. The server stores only a
SHA-256 hash of the token, the same pattern as browser sessions.
