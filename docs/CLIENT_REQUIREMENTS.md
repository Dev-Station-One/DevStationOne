# Native Client Requirements (GTK3 + WebKit2GTK, formerly energye/energy)

Brainstorm output 2026-09-17. Architecture/design is a later step (`/sc:design`).

**Stack note (ADR-052):** the brainstorm and initial design targeted
energye/energy's Webview path. Implementation hit a hard blocker — no
compilable energy v3 release has a matching public `libenergy` runtime
binary — so the client (`DevStationClient` repo) was rebuilt on direct
GTK3 + WebKit2GTK-4.1 via cgo. The product requirements below are
unaffected; only the technology choice in NFR-3 and the OQ answers changed.
See `docs/NATIVE_CLIENT_DESIGN.md` and `docs/DECISIONS.md` ADR-052.

## Product

Dedicated DevStationOne desktop client: a native window wrapping the existing
Control Server web UI. Not a general-purpose browser, not a kiosk. The web UI
(Tabler workspace, xterm.js tabs, Files, Databases, Services) is the client UI;
the desktop app adds native OS integration around it.

**Why it exists over a PWA tab:** system tray, native notifications, real
local downloads. Those three are the entire justification — do not grow scope
beyond them.

## Decisions made

| Question | Decision |
|---|---|
| Product shape | Dedicated DevStation client (webview shell + native extras) |
| Rendering backend | System Webview path (WebKit2GTK on Linux) — not CEF |
| Platforms | Linux x86 only, first. Windows later if wanted; macOS out of scope |
| Native extras | System tray, native notifications, local downloads |
| Connection | None of its own. Loads Control Server URL in webview; web login reused. No custom transport, no port forwarding |
| Distribution | Self/internal only. Plain binary, no packaging pipeline |

**Rejected for cause** (recorded so we don't re-litigate):
- CEF backend: ~200MB bundle, Wayland crash ([energy#59](https://github.com/energye/energy/issues/59) — X11-only on Linux), unrar/proprietary-codec licensing friction on distribution. None needed since we wrap an existing web UI rather than embed Chromium features.
- Own connection layer/tunneling: PWA tab covers it; violates minimal scope.
- Multi-platform day one: QA burden with no current user outside this machine.

## Functional requirements

1. **FR-1 Window** — Single main window loads the configured Control Server URL (default `https://ds1.premadev.com`, overridable). Reuses web login/session cookies; no client-side auth logic.
2. **FR-2 Tray** — App lives in the system tray. Close to tray (does not quit); quit from tray menu. Tray menu: show window, quit.
3. **FR-3 Notifications** — Native OS notifications triggered by server-sent events surfaced by the web UI (e.g. session disconnect, long-running command finished). No browser permission prompt involved. Exact event set defined at design time.
4. **FR-4 Downloads** — Downloads from the web UI land in the real local download directory with visible progress; not handled as in-page blobs.
5. **FR-5 Configuration** — Server URL and download directory read from a small config file / CLI flags. No GUI config screen.

## Non-functional requirements

- **NFR-1 No root / no daemon.** Runs as the normal user. May run as a systemd user unit like the dev server (optional convenience, not required).
- **NFR-2 No stubs.** Per repo rules: tray, notifications, downloads are real implementations, not TODOs.
- **NFR-3 Footprint.** No bundled Chromium; small binary (~8.5MB) using system GTK3/WebKit2GTK via cgo — small, documented build-time dependency (`libgtk-3-dev`, `libwebkit2gtk-4.1-dev`), no bundled runtime library (ADR-052 superseded the original no-CGO preference after energy's libenergy proved unusable).
- **NFR-4 Wayland-safe.** Must run on Wayland as well as X11 (primary reason CEF was rejected; WebKit2GTK handles both).
- **NFR-5 Never log** tokens, cookies, or terminal content, same as all DevStation components.
- **NFR-6 Offline behavior** — shows a plain error state when the server is unreachable; no fake UI.

## User stories / acceptance criteria

- **US-1** As Brian, I launch `devstationone-client`; the DevStation workspace opens in a native window, I log in once, and closing the window leaves it in the tray. *Accept: window hides to tray; tray "Show" restores; tray "Quit" exits.*
- **US-2** A terminal session drops while the window is in the tray; I get a native OS notification without granting any browser permission. *Accept: notification appears; clicking it raises the window.*
- **US-3** I download a file from the Files panel; it appears in my real `~/Downloads` (configurable) with completion notification. *Accept: file on disk, byte count correct.*
- **US-4** Control Server is down; I see an honest error page, tray still works. *Accept: clear error, no crash, reconnect when server returns.*

## Open questions — all resolved 2026-09-17, see docs/NATIVE_CLIENT_DESIGN.md §0

- ~~OQ-1~~ tray: no GTK/LCL dependency either way — DBus StatusNotifierItem, vendored (`energye/systray`) or hand-rolled godbus (slice B decision).
- ~~OQ-2~~ notifications: own `WebKitUserContentManager` JS bridge (not energy's); web UI posts events, godbus freedesktop notifications on client side.
- ~~OQ-3~~ downloads: **reversed post-ADR-052** — direct WebKit2GTK exposes `download-started`/`WebKitDownload`; native interception works, no server token endpoint needed.
- ~~OQ-4~~ repo placement: **own git repo `DevStationClient`** (user decision 2026-09-17); server-side slices stay in the DevStation repo.
- ~~OQ-5~~ single window confirmed (wv wraps browser in one window; web UI owns tabs).