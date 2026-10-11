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

SQL migrations (`migrations/*.sql`) are embedded in the binary
(`migrations/embed.go`, `go:embed`) — no separate migration step.

**The web UI is embedded too** (ADR-048): `web/static/` (`web/embed.go`)
and `assets/` (`assets/embed.go`) are compiled into the binary via
`go:embed`, so `devstationone-server` is self-contained — no separate
`web/static/`/`assets/` directory needs to ship or stay in sync with it.
`web/static/vendor/codemirror6/node_modules` (build tooling, ~102MB, never
served) is deliberately excluded from the embed; only `cm6.bundle.js`,
`chunks/`, `entry.js` are included.

For local development, `DEVSTATIONONE_DEV_STATIC=1` switches
`internal/api/server.go`'s `NewRouter` back to serving `web/static/` and
`assets/` straight from disk (`http.Dir`, resolved relative to the process's
working directory — run from the repo root), so an edited file is visible
on refresh with no rebuild. Unset (the production default), it serves the
embedded copy — which only changes when the binary is rebuilt.

### Configuration (environment variables)

| Variable | Required | Purpose |
|---|---|---|
| `DEVSTATIONONE_DATABASE_URL` | yes | PostgreSQL connection string |
| `DEVSTATIONONE_DB_ENCRYPTION_KEY` | yes | Base64, 32 raw bytes (AES-256-GCM) — encrypts stored database connection credentials (ADR-021). Generate with `openssl rand -base64 32`. Losing this key makes every stored credential unrecoverable; rotating it requires re-entering every connection's credentials. |
| `DEVSTATIONONE_SERVER_ADDR` | no (default `:8080`) | Listen address |
| `DEVSTATIONONE_LOG_LEVEL` | no (default `info`) | `debug`\|`info`\|`warn`\|`error` |
| `DEVSTATIONONE_ENV` | no (default `development`) | `development`\|`production` (informational, logged at startup) |
| `DEVSTATIONONE_TURNSTILE_SITE_KEY` | no (default unset — disabled) | Cloudflare Turnstile bot-check on login (docs/DECISIONS.md ADR-044). From the Cloudflare dashboard: Turnstile → Add widget → this server's domain → copy the Site Key. Must be set together with `..._SECRET_KEY`, or not at all — one without the other is a startup config error. |
| `DEVSTATIONONE_TURNSTILE_SECRET_KEY` | no (default unset — disabled) | Paired secret key for the site key above. |
| `DEVSTATIONONE_DEV_STATIC` | no (default unset — serves the embedded web UI) | Any non-empty value serves `web/static/`/`assets/` from disk instead (dev only — see "Build" above). |
| `DEVSTATIONONE_SMTP_HOST` | no (default unset — email disabled) | SMTP relay for 2FA email codes and account-recovery mail (ADR-047). Setting it plus any of the other `SMTP_*` values enables email; with no relay configured, email factors are enrollable but codes can't be sent (login then falls back to other factors). |
| `DEVSTATIONONE_SMTP_PORT` | no (default `587`) | SMTP relay port; STARTTLS used automatically when the server advertises it. |
| `DEVSTATIONONE_SMTP_USERNAME` / `DEVSTATIONONE_SMTP_PASSWORD` | no | Relay credentials (e.g. a Postmark Server Token). Never logged. |
| `DEVSTATIONONE_SMTP_FROM` | no | Sender address — for Postmark it must be a verified sender signature for the account's server token. |
| `DEVSTATIONONE_SMTP_STREAM` | no (default unset) | Provider message stream, sent as `X-PM-Message-Stream` (Postmark). |
| `DEVSTATIONONE_TRUSTED_PROXIES` | no (default unset — RemoteAddr only) | Comma-separated IPs/CIDR ranges of proxies whose `X-Forwarded-For` headers are trusted for rate limiting, audit `client_ip`, and session records. The header is honored only when the TCP peer itself is trusted, and then the client is the rightmost entry that is not itself a trusted proxy (entries to its left are client-supplied and ignored); a direct client cannot spoof it. Set this on the public deployment (TLS-terminating proxy in front of the server) so external clients don't share one rate-limit bucket. |

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
  real deployment. The release tarball (`.github/workflows/release.yml`)
  bundles just the three binaries — the web UI is embedded in
  `devstationone-server` (see "Build" above), so there's nothing else to
  install or keep in sync.
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

Once running this way, `devstationone-admin` (seeding a new admin, or
resetting a password per "Upgrading"/troubleshooting below) needs the same
`DEVSTATIONONE_DATABASE_URL`/`DEVSTATIONONE_DB_ENCRYPTION_KEY` as the
server — but those now live only inside the unit's `Environment=` lines,
not in your shell (unlike the `install.sh` path, which writes a
sourceable `server.env` you `source` first — see README.md "Resetting a
password"). Systemd scopes `Environment=` to that unit's own process tree;
an interactive SSH/login shell never inherits it. Pull the values out of
the unit before running the CLI:

```bash
set -a
. <(systemctl --user cat devstationone-server.service | grep '^Environment=' | sed 's/^Environment=//')
set +a
echo '<password>' | ./devstationone-admin -email admin@example.com
```

**Changing config later** (e.g. enabling Turnstile above, or any other
variable in the table) on a manual unit: there's no `server.env` to edit —
add/change the `Environment=` line directly in the unit file, then reload
and restart:

```bash
systemctl --user edit --full devstationone-server.service
# add: Environment=DEVSTATIONONE_TURNSTILE_SITE_KEY=...
# add: Environment=DEVSTATIONONE_TURNSTILE_SECRET_KEY=...
systemctl --user daemon-reload
systemctl --user restart devstationone-server.service
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
3. Replace the binary with the new build — the web UI is embedded in it
   (ADR-048), so there's nothing else to sync.
4. Start it — migrations run automatically before it starts serving.
5. Confirm `/healthz` and `/readyz` both answer `ok`, and that a changed
   page (e.g. one with new frontend markup) actually reflects it.

Any change under `web/static/` or `assets/` — including static-only
HTML/CSS/JS/JSON edits — now requires a rebuild and restart to take
effect, since it's compiled into the binary rather than read from disk.
`DEVSTATIONONE_DEV_STATIC=1` opts back into disk-serving for local
development (see "Build" above) where a restart isn't wanted.

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

## Installing with install.sh

`install.sh` installs prebuilt binaries and systemd units.

```bash
# Control Server (root; creates the unprivileged `devstationone` system user)
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | sudo bash -s -- --server
sudoedit /etc/devstationone/server.env
sudo systemctl enable --now devstationone-server

# Development Runtime (normal user; installs to ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | bash -s -- --runtime
```

Create the database first:

```bash
sudo -u postgres psql -c "CREATE ROLE devstation LOGIN PASSWORD 'choose-a-strong-password';"
sudo -u postgres psql -c "CREATE DATABASE devstation OWNER devstation;"
```

`server.env` needs `DEVSTATIONONE_DATABASE_URL`; `install.sh` generates
`DEVSTATIONONE_DB_ENCRYPTION_KEY` (back it up, docs/BACKUPS.md). Seed or reset
an administrator (same command recovers a lost password):

```bash
set -a; source /etc/devstationone/server.env; set +a
echo 'a-strong-password' | devstationone-admin --email you@example.com
```

Runtime enrollment file `~/.config/devstationone/runtime.env` (mode 600):

```bash
DEVSTATIONONE_SERVER_URL=wss://your-server
DEVSTATIONONE_RUNTIME_TOKEN=<token from Runtimes → Add runtime>
```

then `systemctl --user enable --now devstationone-runtime` and
`loginctl enable-linger`. Re-running `install.sh` upgrades binaries and keeps
config; restart the service afterwards.

## Removing

```bash
# Runtime
systemctl --user disable --now devstationone-runtime
rm ~/.local/bin/devstationone-runtime ~/.config/systemd/user/devstationone-runtime.service
rm -r ~/.config/devstationone

# Server
sudo systemctl disable --now devstationone-server
sudo rm -r /etc/devstationone /usr/local/bin/devstationone-server /usr/local/bin/devstationone-admin /usr/local/share/devstationone
sudo userdel devstationone
```

Revoke the runtime in the UI too, and drop the PostgreSQL database if done.

## Troubleshooting

- **Runtime never shows online:** `journalctl --user -u devstationone-runtime -f`.
  Check `DEVSTATIONONE_SERVER_URL` matches how the browser reaches the server
  (`wss://` behind TLS), the token is not revoked, and the machine clock is sane.
- **`systemctl --user` fails over SSH / service stops at logout:** run
  `loginctl enable-linger $USER` once.
- **Terminal opens then disconnects:** `journalctl -u devstationone-server -f`;
  a proxy missing the `Upgrade`/`Connection` headers breaks WebSockets.
- **Lost admin password:** rerun `devstationone-admin --email ...` (see above).
- **Database panel connection fails:** connections are made by the runtime
  from the dev machine; the database must be reachable from there.
- **Port in use:** set `DEVSTATIONONE_SERVER_ADDR=:9090` and restart.

## Web Push and installing the web app

Browsers can install the web UI as an app (Chrome/Edge: install icon in the
address bar; Android Chrome: "Install app"; Safari: Share → Add to Home
Screen) and can receive alerts while the tab is closed (bell panel → "Turn
on"). Requirements:

- Serve the UI over **HTTPS** (browsers refuse service workers and push
  otherwise, except on `localhost`).
- Set `DEVSTATIONONE_WEBAUTHN_RP_ORIGIN` to the public `https://` origin;
  it is also the VAPID contact URL sent to push services.
- The Control Server needs **outbound HTTPS** to the browsers' push services
  (for example `fcm.googleapis.com`, `*.push.services.mozilla.com`,
  `web.push.apple.com`, `*.notify.windows.com`). Nothing inbound is added.
- Keep `DEVSTATIONONE_DB_ENCRYPTION_KEY` stable: the push private key is
  sealed with it. If it changes, the server logs "push: disabled" and the
  push endpoints return 503 until the key is restored.
- The VAPID keypair is created on first start and stored in Postgres, so it
  is included in database backups (docs/BACKUPS.md).
