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

## Security

Every runtime connection is authenticated.

Every server request to a runtime is authorized before dispatch.

Runtime must validate command/resource IDs against its own known state.
