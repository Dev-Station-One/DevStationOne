# Architecture Decisions

## ADR-001 — Go Control Server

Go is used for the Control Server because the product requires concurrent HTTP/WebSocket/runtime connections, PTY/process integration, portability, and a small self-contained deployment.

## ADR-002 — PostgreSQL without PostgREST

The application has a dedicated Go API, so PostgREST is not used.

PostgreSQL is the persistent datastore. Go owns business logic, authorization, API contracts, and runtime orchestration.

## ADR-003 — Separate Control Server and Development Runtime

The Internet-facing Control Server is separated from the machine where development code executes.

The Runtime makes an outbound secure connection, avoiding the need for inbound ports on development machines.

## ADR-004 — Browser as MVP client

The browser is the first client. All product functions are API-accessible so native clients can be built later without redesigning the backend.

## ADR-005 — Projects own execution context

Working directory, user, shell, and environment are properties of a Project. Sessions reference Projects and cannot override the authoritative working directory.

## ADR-006 — Real PTYs

Interactive sessions use operating-system PTYs rather than command/request simulation. This is required for Claude Code, editors, tmux, interactive shells, and arbitrary terminal applications.

## ADR-007 — WebSocket for terminal transport

Terminal byte streams are latency-sensitive and binary. WebSocket is used for live transport. PostgreSQL is not used for byte-by-byte terminal streaming.

## ADR-008 — systemd --user

User services are managed through the normal user's systemd instance where available. Root is not required for normal development-service operations.

## ADR-009 — Server-side browser sessions, not JWT

Browser authentication uses an opaque, high-entropy session token in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. The server stores only a SHA-256 hash of the token, keyed to a `sessions` row (user, expiry, last-seen, user agent, IP).

JWTs were rejected for this slice: DevStation is a single self-hosted Control Server, not a distributed set of services needing stateless verification, so JWT's main advantage does not apply here. A server-side session gives immediate, per-session revocation (required by PRD §4/§5 "account/session management" and by runtime/device revocation elsewhere in the product) without token-blocklist workarounds, and keeps the WebSocket terminal transport (ADR-007) authenticating the same way as the REST API.

## ADR-010 — Runtime identity reuses the bearer-token/hash pattern

Runtime enrollment (Slice 2) authenticates the same way as browser sessions
(ADR-009): a high-entropy random token is generated at registration, the
runtime presents it as `Authorization: Bearer <token>` on connect, and the
server stores/looks up only its SHA-256 hash.

mTLS or per-runtime client certificates were considered and rejected for
this slice: they add a certificate issuance/rotation/revocation subsystem
the product does not otherwise need, whereas the bearer-token pattern is
already implemented, tested, and immediately revocable (`UPDATE ... SET
status = 'revoked'` is checked on every connect attempt). If a future
threat model requires mutual TLS between Runtime and Control Server, it can
be layered underneath this application-level credential without changing
the protocol envelope.

## ADR-011 — `coder/websocket` for the runtime protocol transport

The Control Server <-> Development Runtime connection (ADR-003, ADR-007)
needs a WebSocket implementation on both ends. `coder/websocket` (the
maintained successor to `nhooyr.io/websocket`) was chosen over
`gorilla/websocket` because it is actively maintained, has a smaller
context-aware API, and its concurrency-safe `Read`/`Write` model fits the
heartbeat-plus-read-deadline pattern used for liveness detection. The same
library will carry Slice 4's binary terminal byte stream.

## ADR-012 — Single-process, in-memory runtime connection registry

The Control Server tracks live runtime connections in an in-memory map
(`internal/runtimes.Registry`), scoped to one server process. This is
sufficient for the current single-instance deployment model (ADR-001/PRD
§11: self-hosted, customer-operated). A future horizontally-scaled Control
Server would need a shared broker (e.g. Postgres `LISTEN/NOTIFY` or a
message bus) so any instance can reach a runtime connected to another
instance; that is out of scope until multi-instance deployment is a
requirement.

## ADR-013 — Project ownership: `created_by`, not a shared workspace

Projects are scoped by `created_by` (the DevStation account that created
them), separate from `os_user` (the OS account the runtime executes the
Project's sessions as, e.g. "brian" in PRD §3). A non-admin user's project
list/get/patch/delete only ever sees rows they created; an unauthorized
lookup returns 404, not 403, so a non-owner cannot use the response code to
confirm another user's project exists (SECURITY.md "never rely solely on
IDs supplied by the browser"). Administrators can see and manage every
project, mirroring the existing `is_admin` gate already used for runtime
management (Slice 2), which is useful for a self-hosted operator managing
projects on behalf of other accounts on the same installation.

Runtimes themselves remain unscoped by owner (Slice 2 already allows any
admin to see all runtimes); a Project may reference any existing,
non-revoked runtime regardless of who registered it.

## ADR-014 — Session `os_user` must match the runtime's actual OS user

A Project's `os_user` (Slice 3) names the OS account its sessions should
run as. The Development Runtime process itself always runs as one normal
user and never as root (PRD §8, SECURITY.md "never requires root for
ordinary operation") — it has no `setuid`/`sudo`/privilege-escalation path
to actually become a different OS user. Slice 4's `session.open` therefore
compares the requested `os_user` against the runtime's own
`os/user.Current()` and rejects the session with `session.error` on
mismatch (`internal/pty.Start`), rather than silently running as whichever
user the runtime process happens to be, or pretending to support
multi-user impersonation it cannot deliver. This is a real containment
check enforced at the runtime (SECURITY.md "Runtime must validate
command/resource IDs against its own known state"), not a stub: a Project
misconfigured for a different machine's user fails loudly at session
creation instead of silently running as the wrong (or coincidentally
matching) user.

A future multi-user runtime host would need a real privilege boundary
(e.g. a small root-owned setuid helper, or per-user runtime processes) to
lift this restriction; that is out of scope for the MVP's single-dev-user
deployment model.

## ADR-015 — Terminal sessions: table name, binary framing, and scrollback

Three related Slice 4 design choices:

**Table name.** The PRD and UI call PTY sessions "Sessions," but a
`sessions` table already exists for browser authentication (ADR-009,
`0002_users_sessions.up.sql`). The new table is named `terminal_sessions`
to avoid colliding with that unrelated, pre-existing concept; the Go
package is still `internal/sessions`, matching ARCHITECTURE.md's suggested
package layout — only the SQL table name differs from the product-level
term.

**Binary framing over the existing runtime connection.** Rather than open
a second connection per session between Control Server and Runtime, all
sessions multiplex over the one control-plane WebSocket from Slice 2
(ADR-011/ADR-012). Terminal bytes travel as binary WebSocket frames
prefixed with a fixed-36-byte session ID (`protocol.EncodeFrame`); this
keeps ADR-007's "no JSON-escaping" requirement without inventing a new
transport, and reuses `runtimes.Registry`'s existing per-runtime
connection lookup to route commands. The browser-facing
`/api/v1/sessions/{id}/stream` socket, by contrast, is already scoped to
one session per connection, so it carries raw bytes with no framing at
all — the frame prefix only exists where multiplexing is actually needed.

**Bounded server-side scrollback.** `internal/sessions.Hub` keeps the last
256KB of each session's output in memory (per Control Server process,
matching ADR-012's single-instance scope) and replays it when a browser
(re)attaches, so "browser refresh/reconnect preserves process" (PRD §4)
also means the reattaching terminal isn't blank. This is an in-memory
convenience, not a durability guarantee: output produced while the Control
Server is restarted, or while the runtime's own connection is down, is
lost — PTY bytes are still deliberately not persisted to PostgreSQL
(ADR-007).

## ADR-016 — Generic correlated request/response over the runtime connection

Slice 5 (services) needed a server-to-runtime request/response exchange
with no natural per-request key the way Slice 4's session messages have
`session_id` — several `service.command`s (e.g. two browser tabs both
listing services) can be in flight on one runtime connection at once. This
is exactly what `protocol.Envelope.ID` was reserved for since Slice 2
("optional correlation identifier for future request/response exchanges")
but had gone unused through Slice 4.

Rather than build service-specific correlation bookkeeping,
`runtimes.Conn` gained a generic `SendCommand(ctx, msgType, payload)
(json.RawMessage, error)`: it generates a random ID (reusing
`auth.NewSessionToken`, already exactly "a high-entropy random token" —
overkill length for a short-lived in-memory key, but a one-line reuse beats
a second RNG helper), registers a pending channel keyed by it, sends the
envelope, and blocks until `wsConn.tryResolve` — called from the runtime
connection's read loop for every inbound envelope carrying a non-empty
`ID` — delivers the matching reply or `ctx` is done. The runtime side is
symmetric: `connection.Sender.SendReply(id, msgType, payload)` sends a
reply carrying a given correlation ID, alongside the existing
`SendEnvelope` (id `""`) for fire-and-forget messages like heartbeats and
session lifecycle events, which don't need correlation and shouldn't be
forced to carry an unused ID field.

This machinery is deliberately not services-specific: `service.error` is
the only message type `tryResolve` currently special-cases as a failure
(vs. treating the payload as a success), but the pattern — one request
type, one result type, one error type, correlated by ID — is exactly what
file operations and database queries (Slices 6+) will need too. Building
it generically now, while there are two real call sites to keep it honest
(services today, sessions' existing non-correlated flow as the contrast
case), is judged cheaper than two more rounds of "session_id-shaped"
one-off correlation later.

## ADR-017 — Services: direct systemctl/journalctl exec, and a live-state union for List

`internal/services` shells out to `systemctl --user` and `journalctl
--user` via `os/exec` (argv arrays, no shell — SECURITY.md "never
construct shell commands by concatenating untrusted strings") rather than
talking to systemd's D-Bus API directly. systemd ships no official Go D-Bus
binding in this project's dependency set, and the CLI's plain-text/`show`
output is already a stable, documented interface; parsing it is a smaller,
more auditable surface than vendoring a D-Bus client for this MVP. Unit
names are validated (`services.ValidUnitName`, checked both server-side for
an early `400` and runtime-side before exec) against
`^[A-Za-z0-9:_.@-]+\.service$` as defense-in-depth — `os/exec` already
makes shell injection impossible by construction, so this instead rejects
nonsensical input with a clean error instead of a raw systemctl failure.

**`List` merges two systemctl sources, discovered necessary by testing
against a real systemd --user instance, not anticipated up front.**
`systemctl --user list-units --all` only shows units currently loaded into
the manager's live table — a unit systemd has garbage-collected after it
stopped (observed happening within about half a second on this project's
own dev machine, systemd 255) silently disappears from `--all` even though
`systemctl --user show`/`start` on it still work fine. For a service
manager whose core workflow is "stop a project's dev service, come back
later and start it again," a list that drops exactly the units a user just
stopped is a real defect, not an edge case. `List` therefore unions
`list-units --all` with `list-unit-files` (which lists every installed
unit file regardless of live-table membership) and fills in status for the
difference with one additional bulk `systemctl show unit1 unit2 ...` call
— not one `show` call per missing unit, which would mean dozens of
subprocess spawns on a typical desktop (119 installed unit files vs. 24
loaded, measured on this same dev machine). A bare template unit
(`app@.service`, no instance) is excluded from that union: `systemctl show`
and `start` both reject it outright ("is missing the instance name"), so
listing it would offer Start/Stop actions that error every time. If the
bulk `show` call itself fails for an unexpected unit name, `List` degrades
to the `list-units`-only result rather than failing the whole listing over
one bad entry — found necessary the same way: a real unit
(`gnome-session-manager@.service`) on this dev machine's own systemd
instance broke a naive "error on any exec failure" implementation.

## ADR-018 — Files: os.Root containment, root-relative paths, whole-file base64 transfers (Slice 6)

Date: 2026-09-13

**Decision.** The runtime's filesystem capability (Slice 6) is built on
Go's `os.Root` (`internal/filesystem`). Every file.command is executed
against a Root opened at the Project's working directory; paths travel as
root-relative strings, never absolute, and are validated by
`filesystem.Clean` on the Control Server (early 400) *and* re-checked by
the runtime before the Root call (SECURITY.md's independent containment).
`os.Root` supplies the actual guarantee: it resolves every path under the
root fd and refuses both `..` traversal and symlinks that escape the root —
including symlinks planted inside the root pointing outside it, which
plain `filepath`-based checks miss. Transfers (read results, write
payloads) carry the file's bytes base64-encoded inside the JSON envelope,
capped at `protocol.MaxFileBytes` (24 MB), with the control connection's
WebSocket read limit raised to `protocol.MaxMessageBytes` (48 MB) on both
ends to bound the encoded worst case.

**Why not alternatives.** Validating paths with `filepath.Clean` +
prefix checks is the classic approach but is a known source of
containment CVEs (symlink races, mixed separators); `os.Root` is the
stdlib answer to exactly this. A separate upload/download HTTP channel
(e.g. the runtime exposing a local port) would break the outbound-only,
single-connection transport model. Chunked transfer messages would triple
the protocol surface before any project needed >24 MB files over this
path; the cap is documented in PROTOCOL.md and the upgrade path is noted
there.

**Consequence.** Copy/move are confined to one filesystem (the root) —
acceptable, since a Project's tree lives on one device in practice. HTML
and SVG are served with an attachment disposition so stored project files
can never render as documents on the Control Server origin.
