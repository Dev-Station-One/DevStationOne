# DevStationOne Security Model

## Threat model

Assume:
- the Control Server is exposed to the public Internet
- clients may be compromised
- browser requests may be maliciously modified
- runtimes may disconnect/reconnect
- users may attempt cross-project access
- filesystem paths may contain traversal or symlink attacks
- database credentials are sensitive
- terminal sessions provide arbitrary code execution by design

## Security boundaries

### Browser → Control Server

Authentication and authorization.

### Control Server → Runtime

Cryptographically authenticated runtime identity.

### Runtime → Operating System

Normal user privileges. OS permissions remain a primary defense.

## Authorization

Every request must establish:
- authenticated principal
- resource ownership/access
- operation permission
- runtime association

Never rely solely on IDs supplied by the browser.

## Filesystem containment

All filesystem paths must be resolved and checked against authorized roots.

Reject:
- `..` traversal
- absolute-path escapes
- symlink escapes
- ambiguous path normalization

Perform containment checks at the runtime even if the Control Server already authorized the path.

## Process execution

Never construct shell commands by concatenating untrusted strings.

Use direct process execution APIs.

For a shell session, intentionally execute the configured shell through a PTY, but validate the configured shell as a server-side Project setting.

## Database security

- Encrypt/protect stored credentials.
- Never return passwords through normal API responses.
- Never log passwords.
- Use least privilege where possible.
- Execute database operations on the runtime.

## WebSocket security

Validate:
- authenticated user/session
- target session ownership
- origin policy
- session state
- runtime association

Prevent session hijacking.

## Audit

Audit security-relevant events:
- authentication
- runtime registration/revocation
- project changes
- session creation/termination
- database connection changes
- service changes
- privileged filesystem operations

Do not store raw terminal streams in audit logs.

## Secrets

Never commit secrets.

Provide environment/config mechanisms for:
- database encryption keys
- authentication secrets
- runtime bootstrap credentials
- TLS material

## Defense in depth

Security must be enforced at:
1. HTTP/API layer
2. application authorization layer
3. runtime protocol layer
4. runtime filesystem/process layer
5. operating-system user boundary
