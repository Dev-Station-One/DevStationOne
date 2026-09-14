<p align="center">
  <img src="assets/devstationone-github-logo.png" alt="DevStationOne" width="420">
</p>

<h1 align="center">DevStationOne</h1>

<p align="center">
  <strong>Your development machines, in your browser. Self-hosted.</strong><br>
  Terminal, files, databases, and services for every dev box you own — behind one private web UI.
</p>

---

DevStationOne is a self-hosted platform that turns your development machines into browser-accessible workspaces. Install a small agent on each machine you develop on, point it at your own Control Server, and open those machines from any browser — persistent terminals, a full file manager, database browsers, and systemd services, all in one tabbed workspace.

Nothing routes through our infrastructure. The Control Server and PostgreSQL run on hardware you control, and development machines connect *out* to it — no inbound ports, no SSH juggling, no vendor relay.

## Why DevStationOne

- **One private web UI for every dev machine.** Laptop, homelab box, GPU rig, cloud VM — all appear as runtimes in the same interface.
- **Terminals that stay alive.** Sessions are real PTYs. Close the laptop, lose the Wi-Fi, come back tomorrow — reattach and the shell, the running dev server, and the Claude Code session are all still there.
- **No inbound firewall holes.** Each runtime dials out to the Control Server over a single authenticated WebSocket. NAT, VPN, or plain internet — if the machine can reach the server, it works.
- **Files, databases, services in the same workspace.** Browse and edit files, connect to PostgreSQL/MySQL/SQL Server/SQLite, start and stop user-level systemd services — without leaving the project tab.
- **Claude Code and AI CLIs just work.** Projects run in their configured directory as their configured user, so project-local `CLAUDE.md`, `.claude/skills`, and MCP configs are picked up naturally. Claude Code is an ordinary process, not a special integration.
- **OpenAPI-first.** Everything the web UI does is a documented REST/WebSocket API at `/api/v1` — scriptable today, native clients later.

## How it works

```text
 Your browser
      │  HTTPS / WebSocket
      ▼
 Control Server  ──  PostgreSQL          (a small VPS, homelab box, or VM)
      ▲
      │  outbound WebSocket (runtime dials in)
      │
 Development Runtime                 ──  on every machine you develop on
      ├── PTY sessions
      ├── Filesystem
      ├── systemd --user services
      └── Database connections
```

- **Control Server** — the internet-facing Go application. Serves the web UI, the OpenAPI REST API, and the WebSocket gateway. Persists everything in PostgreSQL.
- **Development Runtime** — a single Go agent installed on each development machine. Runs as your normal user (never root), connects outbound to the Control Server, and exposes PTYs, files, services, and databases for that machine.
- **Project** — a workspace definition: which runtime, which OS user, which working directory, which shell. Terminals, files, databases, and services all hang off projects.

## Requirements

| Component | Requirement |
|---|---|
| Control Server host | Linux (amd64/arm64), 512 MB RAM+, any OS that runs systemd |
| PostgreSQL | 14 or newer, reachable from the Control Server |
| Development machines | Linux with `systemd --user` (most desktop/server distros) |
| Browser | Any current Chrome, Firefox, Edge, or Safari |

## Quick start

```bash
# 1. Install the Control Server (on your server)
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | sudo bash -s -- --server

# 2. Configure it, then start it
sudoedit /etc/devstationone/server.env
sudo systemctl enable --now devstationone-server

# 3. Install the Runtime (on each dev machine, as your normal user)
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | bash -s -- --runtime

# 4. Enroll the runtime: add a runtime in the server UI (Runtimes → Add runtime),
#    then put the server URL + token in ~/.config/devstationone/runtime.env
nano ~/.config/devstationone/runtime.env
systemctl --user enable --now devstationone-runtime
loginctl enable-linger

# 5. Log in at https://your-server and create your first Project
```

Detailed walkthrough below.

## Installing the Control Server

The Control Server needs a PostgreSQL database. Any managed or self-hosted Postgres 14+ works:

```bash
sudo -u postgres psql -c "CREATE ROLE devstation LOGIN PASSWORD 'choose-a-strong-password';"
sudo -u postgres psql -c "CREATE DATABASE devstation OWNER devstation;"
```

Install and start the service:

```bash
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | sudo bash -s -- --server
sudoedit /etc/devstationone/server.env
sudo systemctl enable --now devstationone-server
```

`/etc/devstationone/server.env` — `install.sh` generates a random `DEVSTATIONONE_DB_ENCRYPTION_KEY` for you (back it up — see [docs/BACKUPS.md](docs/BACKUPS.md)); the value you must still set yourself is the database URL:

```bash
DEVSTATIONONE_DATABASE_URL=postgres://devstation:password@localhost:5432/devstation?sslmode=disable
DEVSTATIONONE_SERVER_ADDR=:8080        # default
DEVSTATIONONE_ENV=production
DEVSTATIONONE_LOG_LEVEL=info           # default
DEVSTATIONONE_DB_ENCRYPTION_KEY=...    # generated for you
```

Seed the initial administrator with the `devstationone-admin` CLI. It reads `DEVSTATIONONE_DATABASE_URL`/`DEVSTATIONONE_DB_ENCRYPTION_KEY` the same as the server, so source `server.env` first (as root, since it's `600`):

```bash
set -a; source /etc/devstationone/server.env; set +a
echo 'choose-a-strong-password' | devstationone-admin --email you@example.com
```

Re-running it with the same email resets that account's password (and promotes it to admin if it wasn't already) — use it to recover a lost admin password too. Open `http://your-server:8080`, log in, and you're in.

Behind a reverse proxy with TLS? See [Exposing the Control Server](#exposing-the-control-server).

## Installing the Development Runtime

Run this on each machine you develop on, **as your normal user**. The runtime never needs root and never opens an inbound port.

```bash
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | bash -s -- --runtime
```

The binary installs to `~/.local/bin` — no root needed. Make sure `~/.local/bin` is on your `PATH`.

Next, create a runtime on the server and get an enrollment token:

1. Log in to the web UI → **Runtimes** → **Add runtime**.
2. Give it a name (e.g. `homelab`) and copy the enrollment token. It is shown **once**.

Enroll and start the agent:

```bash
mkdir -p ~/.config/devstationone
cat > ~/.config/devstationone/runtime.env <<'EOF'
DEVSTATIONONE_SERVER_URL=wss://your-server
DEVSTATIONONE_RUNTIME_TOKEN=paste-token-here
DEVSTATIONONE_ENV=production
EOF
chmod 600 ~/.config/devstationone/runtime.env
systemctl --user enable --now devstationone-runtime
loginctl enable-linger          # keep the agent alive when you log out
```

The agent reconnects automatically across restarts, network drops, and machine reboots. When it comes online it appears green in the Runtimes page.

To enroll non-interactively (provisioning scripts, dotfiles bootstrap), write the env file from your tooling the same way — the agent reads `DEVSTATIONONE_SERVER_URL` and `DEVSTATIONONE_RUNTIME_TOKEN` from its environment or the env file above.

## Your first project

A Project is the workspace everything else lives in:

1. **Projects → New project.** Pick the runtime, OS user (e.g. `brian`), working directory (e.g. `/home/brian/projects/myapp`), and shell.
2. **Open the project.** You get a tabbed workspace:

```text
Project: myapp
 ├── Terminal tabs        bash · Claude Code · dev server (multiple at once)
 ├── Files                browse, edit, upload, download, search
 ├── Databases            PostgreSQL · MySQL/MariaDB · SQL Server · SQLite
 └── Services             manage systemd --user units on that machine
```

3. **Open a terminal tab.** It's a real PTY in the project directory as the project user — resize it, detach, close the browser, come back later and reattach. Sessions survive disconnects by design.
4. **Run `claude` in a terminal tab** if you use Claude Code — it sees the project's own files, `CLAUDE.md`, and `.claude/` configuration, because it's running exactly where it expects to.

## Managing DevStationOne

### Day to day

| Task | Where |
|---|---|
| Create/edit/delete projects, change working directory or user | **Projects** |
| Open additional parallel terminal sessions | Project → **+ tab** |
| Reattach to a running session after disconnect | Project → session list |
| Browse, edit, upload, download files; create dirs; rename/move/copy/delete | Project → **Files** |
| Register a DB connection, browse schema, edit data, run SQL | Project → **Databases** |
| Start/stop/restart/enable user services | Project → **Services** |
| Add/remove development machines, revoke access | **Runtimes** |
| Create users, reset passwords | **Settings** |
| Review who did what | **Activity** |

### Updating

Re-run the install script — it replaces the binaries and keeps your config:

```bash
# Control Server
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | sudo bash -s -- --server
sudo systemctl restart devstationone-server

# Development Runtime (on each machine)
curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | bash -s -- --runtime
systemctl --user restart devstationone-runtime
```

Updates never require re-enrollment; the runtime keeps its credentials across upgrades.

### Removing

```bash
# Runtime (on the dev machine)
systemctl --user disable --now devstationone-runtime
rm ~/.local/bin/devstationone-runtime ~/.config/systemd/user/devstationone-runtime.service
rm -r ~/.config/devstationone

# Server
sudo systemctl disable --now devstationone-server
sudo rm -r /etc/devstationone /usr/local/bin/devstationone-server /usr/local/bin/devstationone-admin
sudo userdel devstationone
# drop the PostgreSQL database when you're done
```

Also revoke the runtime on the server (**Runtimes → revoke**) so its credentials stop working.

## Configuration reference

**Control Server** (`/etc/devstationone/server.env`):

| Variable | Default | Description |
|---|---|---|
| `DEVSTATIONONE_DATABASE_URL` | *(required)* | PostgreSQL connection string |
| `DEVSTATIONONE_DB_ENCRYPTION_KEY` | *(required)* | Base64, 32 raw bytes — encrypts stored database connection credentials. `install.sh` generates this for you on first install; never rotate it without re-entering every stored connection's credentials (docs/BACKUPS.md) |
| `DEVSTATIONONE_SERVER_ADDR` | `:8080` | Listen address |
| `DEVSTATIONONE_ENV` | `development` | Set to `production` on real deployments |
| `DEVSTATIONONE_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error` |

Administrator accounts are seeded with the `devstationone-admin` CLI (same `DEVSTATIONONE_DATABASE_URL`/`DEVSTATIONONE_DB_ENCRYPTION_KEY`), not with server env vars:

```bash
set -a; source /etc/devstationone/server.env; set +a
echo 'the-password' | devstationone-admin --email you@example.com
```

**Development Runtime** (`~/.config/devstationone/runtime.env`):

| Variable | Default | Description |
|---|---|---|
| `DEVSTATIONONE_SERVER_URL` | *(required)* | Control Server base URL (`wss://` / `https://`) |
| `DEVSTATIONONE_RUNTIME_TOKEN` | *(required)* | Enrollment token issued when the runtime was created |
| `DEVSTATIONONE_ENV` | `development` | Set to `production` on real deployments |
| `DEVSTATIONONE_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error` |

## Exposing the Control Server

The server speaks plain HTTP on `:8080` and upgrades to WebSocket for terminals and runtime connections. Put it behind TLS before exposing it beyond localhost.

**Caddy** (automatic certificates):

```caddy
dev.example.com {
    reverse_proxy localhost:8080
}
```

**nginx** (certbot for certificates):

```nginx
server {
    listen 443 ssl;
    server_name dev.example.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 3600s;   # long-lived terminal WebSockets
    }
}
```

Then set `DEVSTATIONONE_SERVER_URL=wss://dev.example.com` on every runtime and log in at `https://dev.example.com`.

Do not want it internet-facing at all? Bind to a private interface (`DEVSTATIONONE_SERVER_ADDR=10.0.0.5:8080`) or a Tailscale/WireGuard address — runtimes only need to reach the server, never the reverse.

## Security hardening

- **The Control Server runs as an unprivileged system account, not root.** `install.sh` creates a `devstationone` system user (no login shell, no home directory) and the systemd unit runs as it, alongside `NoNewPrivileges`/`ProtectSystem=strict`/`ProtectHome`.
- **Always terminate TLS.** Terminal bytes, file contents, and credentials cross this connection.
- **Strong admin password, changed after first login.** The bootstrap password exists only to get you in the door.
- **Revoke runtimes you decommission** — revoked enrollment tokens stop working immediately.
- **The runtime is deliberately unprivileged.** It runs as your user, uses `systemd --user`, and refuses to operate outside the project directories you define. Filesystem containment is enforced on the runtime itself, not just server-side.
- **One runtime per OS user.** If two people share a machine, give each their own runtime enrollment.
- **Keep PostgreSQL private.** Only the Control Server needs to reach it — never expose port 5432.
- Terminal input/output, file contents, and credentials are never written to server or runtime logs.

## Troubleshooting

**Runtime never shows green**
```bash
journalctl --user -u devstationone-runtime -f
```
Common causes: wrong `DEVSTATIONONE_SERVER_URL` (must match how the browser reaches the server, `wss://` when behind TLS), revoked or already-used enrollment token (tokens are single-use — create a new one), clock skew on the dev machine.

**`systemctl --user` fails over SSH**
Run `loginctl enable-linger $USER` once — without linger, user services stop when your SSH session ends.

**Terminal opens then disconnects immediately**
Check the server log for runtime connection errors: `journalctl -u devstationone-server -f`. If you're behind nginx without the `Upgrade`/`Connection` headers above, WebSockets fail — terminals included.

**Can't log in / forgot admin password**
Run `devstationone-admin --email you@example.com` again (source `server.env` first, as above) with a new password on stdin — it resets that account's password in place, no need to touch the database or restart the server. (Set `DEVSTATIONONE_LOG_LEVEL=debug` for detail while diagnosing; never leave debug on in production.)

**Database connection fails in the Databases panel**
Connections are made **by the runtime, from the dev machine** — the database must be reachable from that machine, not from the Control Server. Check host/port/credentials from the dev machine first.

**Port 8080 already in use**
Set `DEVSTATIONONE_SERVER_ADDR=:9090` (or any free port) in `/etc/devstationone/server.env` and restart.

## FAQ

**Does my code or terminal traffic go through your servers?**
No. There are no vendor servers. Control Server, PostgreSQL, and all runtimes are yours; traffic only ever crosses your own network paths.

**Do dev machines need public IP addresses or port forwards?**
No. The runtime dials outbound to the Control Server. NAT and firewalls are fine as long as the machine can reach the server.

**Can multiple people use one instance?**
Yes. Create a user per person in Settings. Administrators see and manage every project; other users see only the projects they created — a project you don't own is indistinguishable from one that doesn't exist. Sessions, files, and databases are project-scoped.

**Does the machine need a desktop environment?**
No. The runtime is a headless Go binary using `systemd --user`. Any server distro works.

**Windows or macOS dev machines?**
Runtime targets are Linux today (it depends on PTYs and `systemd --user`). Windows/macOS support and native Android/Linux/Windows clients are planned — the OpenAPI-first design keeps future clients from needing backend rewrites.

**Why not just SSH?**
You can keep SSH. DevStationOne adds persistent reattachable sessions across devices, file management, database browsers, and service management in one UI — and machines need zero inbound exposure. SSH remains the fallback when you're on a machine without the runtime.

**Is it production-ready?**
Yes — real PTYs, real filesystem operations, real database drivers, real systemd integration. No mocked paths or placeholder features.

## Building from source

Requires Go 1.26+ (see `go.mod`) and Docker (for the dev database):

```bash
git clone https://github.com/Dev-Station-One/DevStationOne.git
cd DevStationOne
make db-up        # dev PostgreSQL on localhost:55432
make build        # builds all three binaries
make test         # full test suite
make test-install # smoke-tests install.sh against a local build
```

## Documentation

| Doc | Contents |
|---|---|
| [docs/PRD.md](docs/PRD.md) | Product requirements |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System architecture |
| [docs/API.md](docs/API.md) · [docs/openapi.yaml](docs/openapi.yaml) | REST/WebSocket API reference and contract |
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | Control↔runtime and terminal WebSocket protocols |
| [docs/SECURITY.md](docs/SECURITY.md) | Security model |
| [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Roadmap and build progress |

## License

[MIT](LICENSE) — Copyright (c) 2026 Brian Tafoya
