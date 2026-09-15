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

### Runtime → Docker daemon (ADR-040)

The Docker tab's runtime-side operations reach the daemon only through a
local unix socket (standard paths — `/var/run/docker.sock`, `/run/docker.sock`,
or the rootless `~/.docker/run/docker.sock`); never TCP, never `DOCKER_HOST`
in any other scheme. Granting the runtime user access to that socket (docker
group membership or rootless docker) is **host-root-equivalent** on that
machine — it is an explicit deployment prerequisite, chosen by whoever
installs the runtime, not something the code grants. All container IDs,
compose actions, and the compose file location are resolved or re-validated
by the runtime against the project scope (compose project label or
bind-mount of the Project working directory); a client-supplied container ID
can only ever select among containers the scoped list already showed. The
`docker compose` subprocess is invoked with fixed argv and the project
directory as cwd — no shell interpolation, no client-provided paths or
project names.

### Runtime → backup files (ADR-041)

Backup files (database dumps and project archives) live only on the
runtime host, under a fixed root
(`~/.local/share/devstationone/backups/`) — never on Control Server disk.
Dump/restore commands (`pg_dump`/`pg_restore`, `mysqldump`/`mysql`,
`sqlcmd`, `sqlite3`) are invoked with fixed argv, credentials passed via
environment variables (`PGPASSWORD`/`MYSQL_PWD`) rather than command-line
arguments where the engine's tooling supports it, and dump output is never
logged. A compatible client for each database engine the runtime backs up
must be installed on the runtime host — this is a deployment prerequisite
(same pattern as the Docker socket above), not something the code
provisions; in particular, a MySQL server using `caching_sha2_password`
needs a client build that supports it (a MariaDB-flavored `mysqldump`/
`mysql` does not). Project-archive extraction re-validates every entry's
containment (rejects `..`, absolute paths, and symlink targets escaping the
destination) independently of anything the Control Server already checked,
the same discipline as `internal/filesystem`.

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

Both WebSocket upgrade points (`GET /api/v1/runtimes/connect`,
`GET /api/v1/sessions/{sessionID}/stream`) share `wsAcceptOptions`
(`internal/api/middleware.go`): compression disabled (a per-message-deflate
side channel is a real, if narrow, risk on a connection carrying any
attacker-influenced content, and terminal I/O isn't bandwidth-constrained
enough to need it) and origin validation via `coder/websocket`'s own
default (an upgrade request with a cross-origin `Origin` header is
rejected before the handler ever runs).

## CSRF

The session cookie is `SameSite=Lax` (ADR-009/ADR-026) — a browser already
withholds it on a cross-site `POST`/`fetch`, which is the classic CSRF
vector. `csrfProtect` (`internal/api/middleware.go`) adds defense in depth:
any state-changing request (`POST`/`PUT`/`PATCH`/`DELETE`) carrying an
`Origin` header that disagrees with the request's own `Host` is rejected
with `403`, regardless of cookie/session validity. A request with no
`Origin` header at all is not blocked by this check (see ADR-026 for why).

## Rate limits

Per-client-IP token buckets (`internal/api/middleware.go` `ipLimiter`),
one instance per Control Server process:
- `POST /api/v1/auth/login` specifically: ~10/minute after a small burst,
  to blunt password guessing.
- Every other mutating (`POST`/`PUT`/`PATCH`/`DELETE`) `/api/v1/...`
  request: a much larger general budget. Read (`GET`) requests are
  exempt — the UI polls read endpoints by design (runtime/session/service
  status), and the actual abuse surface is state-changing requests.

Exceeding a bucket returns `429`.

Optional, additional to the rate limit above: Cloudflare Turnstile on
login (docs/DECISIONS.md ADR-044), configured via
`DEVSTATIONONE_TURNSTILE_SITE_KEY`/`DEVSTATIONONE_TURNSTILE_SECRET_KEY`
(`internal/config`). Disabled by default — both unset leaves `POST
/api/v1/auth/login` exactly as it was before this feature existed; setting
only one is a startup config error. When enabled, `internal/turnstile`
verifies the submitted token against Cloudflare's `siteverify` API before
`auth.Authenticate` runs, failing closed on any verification error
(network failure, timeout, non-`success` response) rather than letting the
login through. See docs/API.md "Authentication" for the endpoint contract.

## Security headers

Every response carries `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: same-origin`,
`Permissions-Policy` (denying geolocation/camera/microphone), and a
`Content-Security-Policy` restricting everything to same-origin
(`securityHeaders`, `internal/api/middleware.go`) — plus
`Strict-Transport-Security` when the request arrived over TLS (directly or
via `X-Forwarded-Proto: https`, matching the session cookie's own `Secure`
logic). The CSP keeps `'unsafe-inline'` for `script-src`/`style-src`: every
page under `web/static/` uses inline `<script>`/`<style>` by design (no
build step), so a stricter policy would break the app while adding little
— everything is otherwise same-origin, with one explicit carve-out in
`script-src`/`frame-src` for `https://challenges.cloudflare.com` (optional
Turnstile login challenge, docs/DECISIONS.md ADR-044) — present even when
Turnstile is unconfigured, since the allowance is inert without the widget
script being loaded.

## Observability

Every request is logged as one structured line (method, path, status,
duration, a random per-request correlation ID, client IP —
`withRequestLogging`, `internal/api/middleware.go`) and the same
correlation ID is echoed back as an `X-Request-Id` response header. It is
a debugging aid, not a secret or a security boundary.

`GET /api/v1/metrics` (administrator session required) exposes basic
counters — total requests, 5xx count, connected runtimes, attached
terminal sessions — as JSON (Go's `expvar` format; see ADR-026 for why not
Prometheus exposition format in this MVP).

## Audit

Audit security-relevant events:
- authentication
- runtime registration/revocation
- project changes
- session creation/termination
- database connection changes
- database principal/grant changes
- service changes
- privileged filesystem operations

Do not store raw terminal streams in audit logs.

Retention is configurable (`internal/audit` `Settings.RetentionDays`,
`GET`/`PUT /api/v1/audit/settings`, admin-only); a daily background job
purges events older than the policy. Recording is best-effort and never
blocks the action it describes (ADR-025).

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

## Command run output capture (ADR-045)

The Quick Commands feature persists the **last 64 KiB of a command-run
session's PTY output** into `command_runs.output_tail` — the one deliberate
exception to "do not store raw terminal streams" above, scoped strictly to
sessions opened by a Quick Command click (ordinary terminals capture zero
bytes). Risks and mitigations:

- A command's output can contain anything echoed through it (environment
  values, tokens). Anyone able to view the run history already has full
  interactive terminal access to the same project — capture adds
  *persistence* of a bounded tail, not new visibility.
- The tail is stored in the project-scoped row, authorized exactly like a
  terminal session attach (404 for everyone without project access),
  never logged server-side, never included in list responses (only the
  single-run GET).
- Bounded (64 KiB per run, newest 100 runs per project, pruned on insert),
  ANSI-stripped, forced to valid UTF-8.
- The command string itself is treated like typed terminal input: never
  logged, never interpolated server-side, executed by the runtime as
  `shell -c <command>` under the project's own shell/working directory.
