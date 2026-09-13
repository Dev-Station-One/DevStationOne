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

The implementation must use a secure browser authentication mechanism and runtime device authentication.

The exact mechanism is selected during the authentication slice and documented in OpenAPI.
