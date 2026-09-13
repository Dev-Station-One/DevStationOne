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
