---
id: runtimes
title: Runtimes
summary: Register and monitor the Development Runtimes this server can connect to.
---
# Runtimes

A **Development Runtime** is the agent you install on a real development
machine. It runs as your normal (non-root) user, connects outbound to this
Control Server, and exposes terminals, filesystem access, `systemd --user`
services, and database connectors for the projects that use it.

## Registering a runtime

Click **Register a new runtime**, give it a name, and you'll get a one-time
enrollment token. Set `DEVSTATIONONE_SERVER_URL` and
`DEVSTATIONONE_RUNTIME_TOKEN` on the target machine and start the runtime
binary — it will enroll and appear here as **pending**, then **online**
once connected.

## Status meanings

- **online** — connected right now; projects on this runtime are usable.
- **offline** — registered but not currently connected.
- **pending enrollment** — token issued, runtime hasn't connected yet.
- **revoked** — access removed; the runtime can no longer connect, and any
  of its projects become unusable until re-registered.

Revoking a runtime does not delete the projects that reference it.

## Deleting a runtime

The trash icon removes a runtime record permanently — unlike revoke, there
is no coming back and no re-enable. The runtime's enrollment token hash is
destroyed, so its agent can never reconnect with the old token. If the
runtime is online, its live connection is dropped immediately; the
confirmation dialog warns you when that will happen. A runtime that still
has projects cannot be deleted — delete or re-point those projects first
(the error tells you).
