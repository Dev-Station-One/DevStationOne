# Deploying DevStationOne

DevStationOne is self-hosted (docs/PRD.md "Privacy"): the customer runs the
Control Server, its PostgreSQL database, and every Development Runtime on
their own infrastructure. There is no vendor-hosted relay.

## Control Server

### Build

```bash
go build -o devstationone-server ./cmd/devstationone-server
go build -o devstationone-admin ./cmd/devstationone-admin
```

Static assets (`web/static/`, `assets/`) and SQL migrations
(`migrations/*.sql`) are embedded in the binary — nothing else needs to
ship alongside it except PostgreSQL itself.

### Configuration (environment variables)

| Variable | Required | Purpose |
|---|---|---|
| `DEVSTATIONONE_DATABASE_URL` | yes | PostgreSQL connection string |
| `DEVSTATIONONE_DB_ENCRYPTION_KEY` | yes | Base64, 32 raw bytes (AES-256-GCM) — encrypts stored database connection credentials (ADR-021). Generate with `openssl rand -base64 32`. Losing this key makes every stored credential unrecoverable; rotating it requires re-entering every connection's credentials. |
| `DEVSTATIONONE_SERVER_ADDR` | no (default `:8080`) | Listen address |
| `DEVSTATIONONE_LOG_LEVEL` | no (default `info`) | `debug`\|`info`\|`warn`\|`error` |
| `DEVSTATIONONE_ENV` | no (default `development`) | `development`\|`production` (informational, logged at startup) |

See `internal/config` for the authoritative list.

### Run

```bash
DEVSTATIONONE_DATABASE_URL='postgres://...' \
DEVSTATIONONE_DB_ENCRYPTION_KEY='<base64 32 bytes>' \
./devstationone-server
```

The server runs its own PostgreSQL migrations at startup
(`database.Migrate`, `cmd/devstationone-server/main.go`) — there is no
separate migration step. `GET /healthz` (liveness) and `GET /readyz`
(readiness, checks the database) are unauthenticated.

Seed the first administrator (one-time, does not restart the server):

```bash
DEVSTATIONONE_DATABASE_URL='postgres://...' \
DEVSTATIONONE_DB_ENCRYPTION_KEY='<same key>' \
echo '<password>' | ./devstationone-admin -email admin@example.com
```

### systemd (recommended)

Two supported paths:

- **`install.sh` (packaged, root-invoked)** — `curl ... | sudo bash -s -- --server`
  installs a system-wide unit running as a dedicated, unprivileged
  `devstationone` system account (`useradd --system --no-create-home`) and
  auto-generates `DEVSTATIONONE_DB_ENCRYPTION_KEY` on first install. See
  README.md "Installing the Control Server" — the recommended path for a
  real deployment.
- **Manual, user-level (below)** — for building from source and running
  under your own account (e.g. a personal homelab instance), no root
  required at all:

Run as a normal user-level service (no root required):

```ini
# ~/.config/systemd/user/devstationone-server.service
[Unit]
Description=DevStationOne Control Server
After=network.target

[Service]
WorkingDirectory=/opt/devstationone
ExecStart=/opt/devstationone/devstationone-server
Environment=DEVSTATIONONE_SERVER_ADDR=:8080
Environment=DEVSTATIONONE_DATABASE_URL=postgres://...
Environment=DEVSTATIONONE_DB_ENCRYPTION_KEY=...
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
```

```bash
loginctl enable-linger $USER   # survive logout/reboot
systemctl --user enable --now devstationone-server.service
```

### Reverse proxy / TLS

The Control Server speaks plain HTTP; terminate TLS at a reverse proxy
(nginx, Caddy, Traefik) in front of it. The proxy must:

- forward `Upgrade`/`Connection` headers unmodified — both `/api/v1/runtimes/connect`
  (runtime WebSocket) and `/api/v1/sessions/{id}/stream` (terminal WebSocket)
  require this
- set `X-Forwarded-Proto: https` — the session cookie's `Secure` attribute
  follows this header (`requestIsSecure`, `internal/api/auth.go`), since the
  Control Server itself never sees TLS directly
- allow long-lived connections (raise/disable the proxy's idle-timeout for
  the WebSocket paths above — a session can sit open for hours)

### Rate limits, security headers, CSRF (Slice 12)

Built into the server, not the proxy: per-IP rate limits on
authentication/mutating requests, `Content-Security-Policy`/`X-Frame-Options`/
etc. on every response, and an `Origin` check on state-changing requests
alongside the session cookie's `SameSite=Lax` attribute. See
docs/SECURITY.md and docs/DECISIONS.md ADR-026 for the specifics and
rationale — nothing to configure.

### Metrics

`GET /api/v1/metrics` (administrator session required) exposes Go runtime
stats plus `http_requests_total`, `http_requests_5xx_total`,
`runtime_connections`, and `terminal_sessions_attached` as JSON (Go's
`expvar` format) — see docs/API.md "Metrics". There is no Prometheus
exposition-format endpoint in this MVP (ADR-026); scrape it with a script
that hits the URL with an authenticated session if you need it in a
dashboard.

### Upgrading

1. Back up PostgreSQL first — see docs/BACKUPS.md.
2. Stop the running server.
3. Replace the binary with the new build.
4. Start it — migrations run automatically before it starts serving.
5. Confirm `/healthz` and `/readyz` both answer `ok`.

Migrations are additive/forward-only in normal operation
(`database.Migrate`, `internal/database/database.go`, only ever runs "up").
A `*.down.sql` exists per migration (`migrations/`) for manual rollback if
an upgrade must be reverted; run it with the separately-installed
[`golang-migrate` CLI](https://github.com/golang-migrate/migrate) against a
checkout of this repo (not the built binary, which only embeds the "up"
path in its own runner):

```bash
migrate -path migrations -database "$DEVSTATIONONE_DATABASE_URL" down 1
```

## Development Runtime

### Build and enroll

```bash
go build -o devstationone-runtime ./cmd/devstationone-runtime
```

Register a runtime from the web UI (**Runtimes** → **Register**, or
`POST /api/v1/runtimes` as an administrator) to get its one-time enrollment
token, then:

```bash
DEVSTATIONONE_SERVER_URL='https://control.example.com' \
DEVSTATIONONE_RUNTIME_TOKEN='<enrollment token>' \
./devstationone-runtime
```

The runtime runs as the normal development user it's installed for — never
root — and only ever makes an outbound connection to the Control Server; no
inbound port needs to be opened on the development machine.

### systemd (recommended)

Same pattern as the Control Server: a `~/.config/systemd/user/` unit
running as the development user, with `DEVSTATIONONE_SERVER_URL`/
`DEVSTATIONONE_RUNTIME_TOKEN` in `Environment=`, `loginctl enable-linger`
so it survives logout/reboot.

### Revocation

Revoking a runtime (**Runtimes** → revoke icon, or
`POST /api/v1/runtimes/{id}/revoke`) immediately closes its live connection
if it's online, and its stored token hash can never authenticate a new
connection again — the running `devstationone-runtime` process is left in
place (it just can't reconnect) and should be stopped/uninstalled
separately if the machine itself is being decommissioned.
