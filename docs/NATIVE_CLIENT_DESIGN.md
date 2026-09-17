# Native Client Design (docs/CLIENT_REQUIREMENTS.md)

Design for `devstationone-client` — a direct GTK3 + WebKit2GTK desktop
client. Implements FR-1..FR-5, NFR-1..NFR-6 from `docs/CLIENT_REQUIREMENTS.md`.

**Superseded stack note (ADR-052, 2026-09-17):** this doc originally
designed the client on energye/energy's Webview path. Slice A hit a hard
blocker — no energy v3 release has a matching public `libenergy` runtime
binary (see ADR-052 for the full trail) — so the client was rebuilt directly
on GTK3 + WebKit2GTK-4.1 via cgo. Sections below are updated in place; OQ-1,
OQ-2, OQ-3 answers changed as a result (OQ-3 in particular: native download
interception, ruled out under energy, **is** available with direct
WebKit2GTK bindings).

## 0. Resolved open questions (research findings, 2026-09-17; OQ-1..3 revised post-ADR-052)

| OQ | Answer | Source |
|---|---|---|
| OQ-1 tray | No GTK/LCL tray dependency either way. Direct-GTK client: vendor `energye/systray` (standalone, pure DBus `StatusNotifierItem` + `com.canonical.dbusmenu`, no libenergy dependency) or a small from-scratch godbus implementation — decide in slice B. | [energye/systray](https://github.com/energye/systray) |
| OQ-2 notifications | Own minimal JS bridge via `WebKitUserContentManager` (`register_script_message_handler` + `run_javascript`), not energy's `window.energy`. Same shape: web UI posts a message, client dispatches. | WebKit2GTK `WebKitUserContentManager` docs |
| OQ-3 downloads | **Reversed from ADR-051.** Direct WebKit2GTK bindings (unlike energy's wrapper) expose `download-started` on `WebKitWebContext` and `webkit_web_view_download_uri`. Native download interception is available — reconsider replacing the token-endpoint design (D-3) with a native download handler in slice D. | WebKit2GTK `WebKitDownload` API |
| OQ-4 repo placement | **Own git repo** — `/home/btafoya/projects/DevStationClient` (user decision 2026-09-17). Server-side slices (download token endpoint if still needed, bridge JS) stay in the DevStation repo; client Go code lives in the new repo. | user decision |
| OQ-5 windows | Single window: one `GtkWindow` containing one `WebKitWebView`. The web UI already manages all tabs. | webview.go |

## 1. Architecture

```text
┌───────────────────────────────────────────────────────────────┐
│ devstationone-client (single Go binary + cgo, Linux x86)      │
│                                                               │
│  GtkWindow + WebKitWebView ── loads Control Server URL        │
│        │  own JS bridge (WebKitUserContentManager)            │
│        ├─► inbound: script message handler                    │
│        └─► outbound: run_javascript (progress, completion)    │
│                                                               │
│  tray      : DBus StatusNotifierItem (energye/systray or own) │
│  notif     : godbus org.freedesktop.Notifications             │
│  downloads : native WebKitDownload (download-started signal)  │
│  config    : $XDG_CONFIG_HOME/devstationone/client.json       │
└───────────────────────────────────────────────────────────────┘
                                │ plain HTTPS
                                ▼
                     Control Server (unchanged UI)
```

Control Server changes: none required if downloads go native (OQ-3
reversal) — WebKit's own download signal fires on the existing
authenticated session, no token endpoint needed. Web UI changes are limited
to a small client-detection module for notifications. Re-evaluate D-3 in
slice D before touching the server.

## 2. Design decisions

### D-1 Tray = DBus StatusNotifierItem, no GTK/LCL dependency

Vendor `energye/systray` (standalone package, no libenergy runtime
dependency — verify at slice-B start that it truly stands alone post-ADR-052)
or hand-roll ~150 lines of godbus against `org.kde.StatusNotifierItem` +
`com.canonical.dbusmenu`, matching the same protocol energy used. Menu:
"Show" / "Quit". Close-to-tray: the GTK `delete-event` handler
(`webview.go`'s `goDeleteEvent`) hides the window instead of allowing
destroy; `Quit` from the tray menu calls `gtk_main_quit`. StatusNotifierItem
is supported by GNOME (extension), KDE, XFCE; bare-WM fallback stays a
documented limitation, no snixembed proxy in scope.

### D-2 Notifications = godbus `org.freedesktop.Notifications`, triggered by own JS bridge

- Client implements `notify(name, summary, body, urgency, action)` over
  standard freedesktop notifications (godbus, small dependency).
- Bridge protocol: client registers a `WebKitUserContentManager` script
  message handler (e.g. `dsClient`); web UI posts
  `window.webkit.messageHandlers.dsClient.postMessage({type: 'ds:notify', ...})`.
  Clicking a notification → client calls `webkit_web_view_run_javascript`
  to raise the relevant tab.
- Only events the web UI already knows about (session disconnect, long
  command finished, download done, runtime up/down — matches slice-C scope
  agreed 2026-09-17).
- Web UI guards: if the client's handler isn't registered (plain
  browser/PWA), behavior is exactly today's in-page notifications. One
  small module (`internal/webui/.../clientBridge.js`), no framework change.

### D-3 Downloads = native WebKitDownload (reversed from ADR-051)

ADR-051 ruled this out because energy's webkit2gtk wrapper bound no download
signals. Direct WebKit2GTK bindings do: `webkit_web_context_get_default()`'s
`download-started` signal plus `WebKitDownload`'s `SetOnDecideDestination`/
`SetOnFinished`. Design for slice D:

1. Client hooks `download-started` on the shared `WebKitWebContext`,
   decides the destination path under `downloadDir` (name-collision →
   numeric suffix), and lets WebKit stream the file — no token endpoint,
   no server change, downloads ride the existing authenticated WebKit
   session cookie.
2. Client tracks progress via `WebKitDownload`'s progress signal and posts
   completion through the JS bridge (D-2) for a web-UI toast.
3. If a future need arises to trigger a download the browser wouldn't
   naturally initiate (e.g. programmatic export), fall back to
   `webkit_web_view_download_uri()` from the client side.

Server-side `POST /api/downloads/tokens` from ADR-051 is **not needed**
unless slice D discovers a case native interception can't cover.

### D-4 Config = JSON file + flags, no GUI

`~/.config/devstationone/client.json`: `{"server_url": "...",
"download_dir": "~/Downloads", "hide_to_tray": true}`.
Flags override: `--server-url`, `--download-dir`, `--devtools`.
Default `server_url`: `https://ds1.premadev.com`.

### D-5 Build/runtime

- Own module `github.com/btafoya/devstationone-client`, git repo
  `DevStationClient` (user decision 2026-09-17).
- cgo build against `gtk+-3.0 webkit2gtk-4.1` via pkg-config (NFR-3's
  no-CGO preference is superseded by ADR-052 — a small, statically-linkable
  cgo dependency on system libraries already required at runtime beats a
  broken purego binding to a dead vendor runtime).
- Runtime deps: `libwebkit2gtk-4.1`, GTK3 — apt deps of the running distro,
  documented in README, not bundled. Dev build also needs
  `libgtk-3-dev`/`libwebkit2gtk-4.1-dev`.
- Runs as the normal user; no autostart unit (user decision 2026-09-17,
  manual launch).

### D-6 Notifications/tray actions require no app-side trust decisions

The client treats every bridge message as untrusted web content (it is):
whitelist event types (`ds:notify`, `ds:download`), cap payload sizes,
validate the token/URL pair server-side only through the token endpoint,
never accept arbitrary local paths. A compromised web page can therefore at
most trigger a notification or a legitimate server-authorized download —
mirrors what a browser would allow anyway.

## 3. Component map (files to create/modify)

Client — **own git repo `DevStationClient`** (`/home/btafoya/projects/DevStationClient`):
- `main.go` — app entry, config load, reachability check *(done, slice A)*
- `webview.go` — GTK3 window + WebKitWebView, cgo bindings *(done, slice A)*
- `tray.go` — systray wiring *(slice B)*
- `notify.go` — freedesktop notifications (godbus) *(slice C)*
- `bridge.go` — `WebKitUserContentManager` message handler, whitelisted
  event types *(slice C)*
- `download.go` — `download-started` handler, progress, completion
  *(slice D)*

Server (this repo, DevStation) — **only if slice D's native downloads need
a server change**, which the current design doesn't expect:
- `internal/webui/assets/.../clientBridge.js` — client detection + bridge
  events; notification posting

Docs: `docs/CLIENT_REQUIREMENTS.md` (mark OQs resolved), `docs/ARCHITECTURE.md`
(component list), this file.

## 4. Risks

| Risk | Mitigation |
|---|---|
| Direct cgo bindings mean hand-written WebKit2GTK API surface, no framework buffer | Keep `webview.go` minimal and well-commented; one file per feature (tray/notify/bridge/download) as new cgo surfaces are added |
| `energye/systray` may have its own dependency surface worth checking (per ADR-052 residual risk) | Verify standalone build before vendoring in slice B; fall back to hand-rolled godbus SNI if not clean |
| Script message handler must survive login redirect | `WebKitUserContentManager` handlers persist across navigation on the same view — verify in slice C, no re-injection needed unlike energy's per-page approach |
| GNOME needs AppIndicator extension for StatusNotifierItem | Document; out of scope to auto-install |
| webkit2gtk-4.1 vs 4.0 pkg-config name differs by distro | `webview.go`'s `#cgo pkg-config` line documented as the thing to edit for older distros (see README) |

## 5. Implementation order (slices)

User-confirmed 2026-09-17: notification event set for slice C = **all four** —
session disconnect, command finished, download done (client), runtime up/down.
No systemd autostart unit (manual launch). Close-to-tray default on
(`hide_to_tray: true` in client.json, configurable).

1. **Slice A** — window + config: GTK/WebKit window loads server URL,
   honest error page on unreachable server, config file/flags. **Done and
   verified 2026-09-17**: builds clean under `CGO_ENABLED=1`, `go vet` and
   `gofmt` clean, headless (Xvfb) smoke test against the live dev Control
   Server renders the real login page. Superseded energy-based slice A
   design per ADR-052.
2. **Slice B** — tray + close-to-tray + quit (D-1). Accept: US-1.
3. **Slice C** — notifications bridge (D-2) incl. web UI `clientBridge.js`.
   Accept: US-2.
4. **Slice D** — native download handler (D-3, reversed from token-endpoint
   design). Accept: US-3.

Each slice: full vertical per repo rules (tests, smoke, docs, CodeGraph).