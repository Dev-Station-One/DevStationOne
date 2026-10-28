# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The web UI (Tabler, served by the Go Control Server) is the surface this design record governs. A native Jetpack Compose Android client and a Linux desktop client (WebKit2GTK wrapper running this web UI, separate repo `/home/btafoya/projects/DevStationClient`) exist or are planned, but their design lives outside this record (user decision, 2026-10-27).

## Users

A solo developer, Brian Tafoya's archetype: someone who codes across several machines they own or control — a laptop, a homelab box, a GPU rig, a cloud VM — and needs to reach any of them from any screen (browser, tablet, borrowed machine, phone) without SSH acrobatics. They run the Control Server themselves and install the Runtime agent on each dev machine. Multi-user accounts exist as a product capability, but the design target is the individual operator using the product to do development work, not an administrator managing other people's environments.

## Product Purpose

DevStationOne is a self-hosted remote development environment platform. It replaces the SSH + tmux + port-forward + VPN routine with one private URL: a Control Server the user operates, and Runtime agents that dial out from each dev machine. Opening the server in a browser gives a full workspace per project — terminals, files, an editor, Git, databases, Docker, services, journal — where running sessions persist independent of any client. Success means the user can close the laptop, come back tomorrow on another device, reattach to the same session mid-command, and never lose work or data to a browser disconnect.

## Positioning

Neighboring products (SSH stacks, hosted dev-clouds, vendor-relay tools) cannot truthfully claim all of:

- **Zero inbound ports.** Runtimes connect outbound only, so NAT, CGNAT, and home routers need nothing opened. The user's server is the only Internet-facing component, and they run it.
- **Sessions that outlive every client.** Real PTYs on the runtime survive browser closes, device switches, and days away; clients reattach, nothing restarts.
- **A whole workspace, not a shell.** Files, code editor, Git, four database engines, Docker, and `systemd --user` services live beside the terminals in the same project tab.
- **Own hardware, no relay.** No vendor-hosted account or relay touches the data; the Control Server and its PostgreSQL run where the user puts them.
- **API-first.** Every capability the web UI has is a documented REST/WebSocket surface, so clients and scripts ride the same contract.

## Operating Context

- Development happens in **Projects** — each a runtime + OS user + working directory + shell — opened as one tabbed workspace. Multiple terminal sessions run per project simultaneously.
- AI CLIs (Claude Code and friends) are ordinary processes started in the project directory; project-local `CLAUDE.md`, `.claude/`, skills, and MCP config work unmodified. DevStationOne has no Claude-specific integration and must not invent one.
- Database work targets private databases (localhost-only on the dev machine); connections execute from the Runtime, so private databases never need Internet exposure.
- Service management uses `systemd --user` at the Runtime host; root is never required for ordinary operation.
- Real usage scenes: laptop browser at the desk, phone or tablet away from the desk, a borrowed machine needing no install beyond a URL, and a PWA install on Chrome/Edge/Safari with web push while the tab is closed.
- Long-lived background work is the norm: dev servers, builds, Claude Code sessions left running for hours or days.

## Capabilities and Constraints

- **Control Server** (Internet-facing, Go + PostgreSQL): REST/OpenAPI API, authentication, authorization, WebSocket gateway, runtime connection manager, Tabler web UI.
- **Development Runtime** (Go, runs as normal user, outbound connection): real PTYs, filesystem operations with server-independent containment, `systemd --user` management, database connectors (PostgreSQL, MySQL/MariaDB, SQL Server, SQLite), capability reporting.
- **Project workspace tabs:** Terminal, Files, Code, Source Control (Git), Databases, Docker, Journal.
- **Users, devices, runtimes:** multi-user support, device pairing with per-device signing keys, runtime enrollment and revocation, activity/audit log.
- **Web push** and PWA install are available for supported browsers.
- **Terminology** (fixed, used everywhere): Control Server, Development Runtime (or "Runtime"), Project, Session, runtime, device.
- **Constraints:** self-hosted only — no SaaS hosting, no vendor relay; no Kubernetes/container management (a Docker tab exists; that is the ceiling); database access never requires direct Internet exposure of the database; client-provided paths/IDs/env values are never trusted — the Control Server authorizes and the Runtime enforces containment independently; no sensitive values (terminal I/O, credentials, tokens) in logs.
- **Undecided product facts:** Windows/macOS/Linux desktop clients are "coming soon" (design not committed); no formal accessibility standard target has been chosen yet.

## Brand Commitments

- Tabler 1.5.x is the UI framework, using the folded-sidebar layout pattern (`https://preview.tabler.io/layout-folded.html`); patterns must be Tabler 1.5.x / Bootstrap 5.3 compatible.
- `docs/BRAND_ASSETS.md` (logo/lockup assets) and `docs/BOOTSTRAP_COLORS.md` (color tokens) are authoritative for visual branding — no substitute treatments.
- `docs/WEB_UI_DESIGN.md` and `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md` are authoritative for visual hierarchy, workspace layout, component anatomy, responsive behavior, and UI states; deviations must be documented, never silently simplified.
- The terminal is xterm.js — no custom terminal renderer.
- Public voice (per README): plain, confident, benefit-led sales copy. "Close the laptop. Your dev environment keeps running." is the tagline on record. Dyslexia-aware: Brian reads better with clear structure and direct wording.

## Evidence on Hand

- Complete working system: Control Server + Runtime in this repo, a persistent dev instance (systemd user service `devstationone-dev.service`, upstream `http://192.168.25.165:18091`, public `https://ds1.premadev.com`), seeded dev user `dev@local.test`.
- Canonical contracts: `docs/openapi.yaml` (HTTP), `docs/PROTOCOL.md` (WebSocket), plus PRD, API, ARCHITECTURE, SECURITY, DEPLOYMENT, and UI docs under `docs/`.
- A real Android client that builds and a physical Pixel 8a test device (out of this design record's scope).
- README (`README.md`) with the marketing copy in use.
- Absences: no testimonials, case studies, press, benchmarks, or pricing — future work must not fabricate any of these.

## Product Principles

1. **The session is the truth; the client is disposable.** Any client can die and any client can reattach. Nothing of value lives only in a browser.
2. **Capability-honest UI.** The interface adapts to what the connected Runtime reports and never fakes a capability it doesn't have (SQLite has no roles; we say so).
3. **Everything is the API.** The web UI is one client over a documented contract; anything the UI can do, the API must expose.
4. **Server-side authority on high-impact operations.** Resources are authorized on the Control Server; the Runtime enforces its own filesystem containment; clients are never trusted.
5. **Own hardware, own data.** The product works entirely inside the user's infrastructure, with no vendor in the path.

## Accessibility & Inclusion

Fully responsive, mobile-first, touch-friendly — no critical function may be desktop-only. No formal WCAG target (e.g. 2.1 AA) has been chosen yet; that is an open decision for future work.