# DevStation Runtime and Terminal Protocol

This document defines the protocol boundary between Control Server and Development Runtime.

The implementation must choose a versioned, authenticated, binary-safe protocol.

## Requirements

- outbound runtime connection
- mutual/runtime identity
- heartbeat
- reconnect
- capability advertisement
- request/response correlation IDs
- server-to-runtime commands
- runtime-to-server events
- terminal byte streaming
- terminal resize
- signals
- session lifecycle
- file operations
- service operations
- database operations

## Terminal stream

A terminal stream must preserve raw terminal bytes.

Recommended logical messages:
```text
session.open
session.input
session.output
session.resize
session.signal
session.exit
session.close
```

The protocol must be binary-safe and must not JSON-escape arbitrary terminal bytes.

## Session lifecycle

```text
requested
starting
running
detached
exited
closed
```

A detached browser does not imply an exited process.

## Runtime capabilities

Runtime registration advertises:
- terminal
- filesystem
- services
- database drivers
- systemd availability

## Versioning

Protocol messages must include a protocol version.

Backward compatibility rules must be documented once the first implementation exists.

## Implementation (Slice 2): control-plane envelope

The control-plane connection is a single WebSocket. Every message is a JSON
text frame with this envelope:

```json
{"v": "1", "type": "hello", "id": "", "payload": { ... }}
```

- `v` — protocol version, currently `"1"`.
- `type` — one of `hello`, `heartbeat`, `revoked` (Slice 2). Later slices add
  session/file/service/database message types.
- `id` — optional correlation ID, reserved for future server-to-runtime
  request/response exchanges. Unused by Slice 2 messages.
- `payload` — message-specific body.

Terminal byte streams (Slice 4) are a separate concern from this envelope:
they must remain binary-safe and are not JSON-wrapped.

### Connection lifecycle

1. Runtime dials `GET /api/v1/runtimes/connect` with
   `Authorization: Bearer <enrollment token>`.
2. Server authenticates the token (hash lookup), rejecting unknown tokens
   with `401` and revoked tokens with `403` before upgrading.
3. Runtime sends `hello` immediately after the WebSocket upgrade:
   ```json
   {"v": "1", "type": "hello", "payload": {
     "hostname": "dev01",
     "os": "linux",
     "arch": "amd64",
     "runtime_version": "0.1.0",
     "capabilities": {"terminal": false, "filesystem": false, "services": false}
   }}
   ```
   Capability fields are only `true` once the slice implementing that
   capability exists; Slice 2 advertises none.
4. Server marks the runtime `online`, records `enrolled_at` (first time
   only) and the hello fields, and registers the live connection.
5. Runtime sends `heartbeat` every 15s. The server updates `last_seen_at`
   and treats a 45s silence (3 missed heartbeats) as a dead connection.
6. On disconnect, the server marks the runtime `offline` (unless it has been
   revoked) and the runtime reconnects with exponential backoff (1s, capped
   at 30s), resetting the backoff after each successful handshake.
7. If an administrator revokes the runtime while connected, the server
   sends `revoked` and closes the connection. Any subsequent connection
   attempt with that token is rejected with `403` at step 2.

## Security

Every runtime connection is authenticated.

Every server request to a runtime is authorized before dispatch.

Runtime must validate command/resource IDs against its own known state.
