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

`POST`/`GET`/`revoke` require an authenticated administrator session (cookie
auth). `POST /api/v1/runtimes` returns the created runtime plus its raw
enrollment token; the token is shown exactly once and only its SHA-256 hash
is stored.

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

## Sessions

```text
GET    /api/v1/sessions/{sessionID}
PATCH  /api/v1/sessions/{sessionID}
DELETE /api/v1/sessions/{sessionID}

POST   /api/v1/sessions/{sessionID}/attach
POST   /api/v1/sessions/{sessionID}/detach
POST   /api/v1/sessions/{sessionID}/resize
POST   /api/v1/sessions/{sessionID}/signal

WSS    /api/v1/sessions/{sessionID}/stream
```

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

The final OpenAPI contract must define:
- list
- status
- start
- stop
- restart
- enable
- disable
- logs

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
