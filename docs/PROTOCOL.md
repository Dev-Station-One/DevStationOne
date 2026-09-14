# DevStationOne Runtime and Terminal Protocol

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
- `os_username`, `home_dir` — the OS user the runtime process runs as and
  its home directory (a runtime always runs as one fixed user, ADR-014).
  Replaces the old per-Project `os_user`: the Control Server reads it live
  from the runtime instead of trusting a client-typed value.
- `shells` — login shells parsed from `/etc/shells` on the runtime host, so
  the Project setup UI can offer a real dropdown instead of a free-text
  path. Empty if unreadable; no fallback list.

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

## Implementation (Slice 4): terminal sessions

Terminal sessions multiplex over the same control-plane connection used for
`hello`/`heartbeat`/`revoked` (no second connection to the runtime). Two
kinds of messages travel on it:

**Control messages** — JSON `Envelope`s on WebSocket text frames, same
shape as Slice 2:

```text
session.open    server -> runtime   {session_id, shell, dir, os_user, env, cols, rows}
session.ready    runtime -> server  {session_id}
session.error    runtime -> server  {session_id, error}
session.resize   server -> runtime  {session_id, cols, rows}
session.signal   server -> runtime  {session_id, signal}
session.close    server -> runtime  {session_id}
session.closed    runtime -> server {session_id}
session.exit      runtime -> server {session_id, exit_code}
```

`session.open` correlates its `session.ready`/`session.error` response by
`session_id` alone (the payload's own field) rather than the envelope's
`id` — one is already unique per session, so a second correlation
identifier would be redundant.

**Terminal bytes** — binary WebSocket frames, never JSON-wrapped:

```text
[36-byte session ID][raw payload bytes]
```

The session ID is a Postgres `uuid` in its canonical 36-character string
form, so the prefix is fixed-width — no length delimiter needed
(`protocol.EncodeFrame`/`DecodeFrame`). Direction is implicit: a frame the
server sends is input, a frame the runtime sends is output. This satisfies
the "binary-safe, must not JSON-escape" requirement above while reusing the
single connection from Slice 2.

The Control Server's WSS `/api/v1/sessions/{sessionID}/stream` (browser
side, see `docs/API.md`) is a separate, simpler connection: one session
per socket, so it carries binary terminal bytes with no framing at all —
the Control Server's `internal/sessions.Hub` is what translates between
that per-session socket and the runtime's multiplexed frames.

Runtime-side, a real OS PTY per session (`internal/pty`, wrapping
`github.com/creack/pty` — ADR-006) is started for `session.open` and its
output is streamed back via a `pump` goroutine per session until the
process exits. A `session.open` whose `os_user` does not match the
runtime's actual OS user is rejected with `session.error` (ADR-014).

## Implementation (Slice 5): service commands

Unlike terminal sessions, `systemd --user` operations are simple
request/response — list/status/start/stop/restart/enable/disable/logs each
produce one answer, with no ongoing byte stream. They reuse the
control-plane connection with a single message pair:

```text
service.command   server -> runtime   {action, unit, lines}
service.result     runtime -> server  {services} | {service} | {logs}
service.error       runtime -> server {error}
```

`action` is one of `list`, `status`, `start`, `stop`, `restart`, `enable`,
`disable`, `logs`; `unit` is required for every action except `list`;
`lines` is the tail length for `logs`.

This is the first use of `Envelope.ID` for its originally-reserved purpose
(see "Implementation: control-plane envelope" above): several service
commands can be in flight on one runtime connection at once, with no other
natural per-request identifier the way session messages have `session_id`,
so the server generates a random correlation ID per command and the
runtime's `service.result`/`service.error` reply carries the same ID back.
`runtimes.Conn.SendCommand` on the Control Server implements the
generic wait-for-matching-ID half of this exchange; `connection.Sender`'s
`SendReply(id, ...)` implements the runtime side. Both are intentionally
generic (not service-specific), for reuse by future request/response RPCs
(file operations, database queries) — see ADR-016.

Runtime-side, `internal/services` runs real `systemctl --user`/`journalctl
--user` commands (direct process execution, no shell — SECURITY.md) via
`os/exec`, never as root. `hello.Capabilities.Services` reflects whether a
`systemd --user` instance is actually reachable at connect time, not merely
whether this runtime build supports the capability.

## Implementation (Slice 6): file commands

Slice 6 adds three message types, reusing the Slice 5 correlated
request/response machinery unchanged:

- `file.command` (server→runtime): `{action, root, path, to, data, query}`.
  `action` is one of `list`, `stat`, `read`, `write`, `create`, `mkdir`,
  `rename`, `copy`, `move`, `delete`, `search`.
- `file.result` (runtime→server): exactly one of `{entries}`, `{info}`,
  `{data}`, `{matches}` populated, matching the action.
- `file.error` (runtime→server): `{error}` — the same payload shape as
  `service.error`; the correlation resolver (`wsConn.tryResolve`) treats
  both types as failures.

Containment: the Control Server fills `root` from the Project record (its
working directory) — `root` is never accepted from a client. `path`/`to`
are root-relative and validated with the same `filesystem.Clean` rule on
both sides (reject empty/absolute/`..`), then the runtime performs every
operation through an `os.Root` opened at `root`, which also refuses
symlinks escaping the root (SECURITY.md, ADR-018).

The Project setup UI's directory picker (ADR-020) reuses `file.command`
unchanged for the `list`/`mkdir`/`stat` actions, with one difference: `root`
is the runtime's reported home directory (`hello.Capabilities.HomeDir`)
instead of a Project's working directory, since there is no Project yet at
that point in the flow. No new message type exists for this — same
envelope, same runtime-side containment, different source for `root`.

Transfer encoding: `data` (write payloads and read results) is the file's
bytes base64-encoded inside the JSON envelope by `encoding/json`. A file is
capped at `protocol.MaxFileBytes` (24 MB); both ends of the control
connection enforce a read limit of `protocol.MaxMessageBytes` (48 MB),
which comfortably bounds the base64-encoded worst case (~33 MB).
If the 24 MB cap proves too small for real projects, the
upgrade path is chunked streaming messages, not raising the envelope
further.

## Security

Every runtime connection is authenticated.

Every server request to a runtime is authorized before dispatch.

Runtime must validate command/resource IDs against its own known state.
