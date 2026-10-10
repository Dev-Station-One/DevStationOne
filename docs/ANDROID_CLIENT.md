# ANDROID_CLIENT.md — DevStation Android client

Native Android terminal client (ADR-053). Fork of the muxalot app's proven
architecture (same author): WebView-rendered xterm.js, fully native input
and touch handling, terminal tabs with per-tab colors, pairing via
QR-scanned device keys with per-request ECDSA signatures — no password, no
stored token, nothing to steal.

## Scope

- **Screens**: server list (multi-server) → projects → terminal tabs, plus
  settings (app lock, screenshot flag) and the shortcut-dock macro editor.
- **Terminal**: one session per tab, named (the session `name` is the tab
  label, the UUID identity is server-side). Reconnect with backoff +
  immediate kick on network change; the server's 256 KiB scrollback replay
  makes reattach seamless (`tty.reset()` on open, like tmux attach).
- **Input**: captured natively (KeyCaptureView + InputEncoder — sticky
  Ctrl/Alt, app-cursor-aware arrow sequences, bracketed paste with ESC
  stripping); the WebView renders only, matching "tmux repaint + native
  keys" muxalot behavior against DevStation's raw PTY sessions.
- **Dock**: bottom bar (ESC/TAB///:/arrows) + drill-in fan (sticky
  modifiers, ctrl combos, F-keys, navigation, user-defined shortcut
  macros), ported from muxalot in full.
- **NOT in the app**: file management (the web UI owns it by design), Quick
  Commands (ADR-045), databases/services/git screens — web-only for now.

## Auth (ADR-053)

The device holds an ECDSA P-256 keypair in the Android Keystore; every
request (WebSocket upgrades included) signs
`devstationone-v1\nMETHOD\nHOST\nREQUEST_URI\nts\nnonce` into a
`Authorization: Sig id,ts,nonce,sig` header. Pairing: on the server run

```sh
devstationone-admin pair --email you@example.com --name "Pixel" --url https://ds1.example.com
```

scan the QR (or enter URL + code in the app's Pair screen). Devices are
managed/revoked in the web UI (Settings → Paired devices; Users manager for
admins). HTTPS only — the app refuses `http://` (cleartext is blocked at
the manifest level too; use the TLS proxy front, e.g. `ds1.premadev.com`
for the dev instance).

## Build

Gradle + AGP 8.7.2, Kotlin 2.0.21, compileSdk 36, minSdk 26 (same proven
pair as muxalot). From the repo root:

```sh
cd android
ANDROID_HOME=<sdk> gradle assembleDebug   # gradle 8.14+; a wrapper dist is cached for 8.14.3
```

The unsigned debug APK lands at `android/app/build/outputs/apk/debug/`.
Release builds are minified (`assembleRelease`) and need a signing key
(`keystore.properties` in `android/`, gitignored — same structure as
muxalot's); CI release signing is out of scope here.

## Install (sideload)

`adb install app-debug.apk` or copy the APK to the phone and open it.

## Vendored pieces

`assets/` carries xterm.js + addon-fit + addon-unicode11 + addon-web-links,
JetBrains Mono NF Mono, and their license files — identical vendoring
policy to the web client (docs/REQUIREMENTS_ICON_SYSTEM.md); see
`assets/VENDOR.md`. The clipboard/search/progress addon work tracked in
docs/XTERM.md applies here too when that slice lands (same files).

## Session lifecycle notes

- Tabs list every non-terminal session (`requested`/`starting`/
  `running`/`detached`); exited/closed/failed sessions lose their tab on
  the next refresh, mirroring the web Terminal tab.
- Stream close first asks `GET /api/v1/sessions/{id}` for the status:
  `exited|closed|failed` ends the reconnect loop (EXITED state), anything
  else reconnects.
- Close-tab offers Detach (REST `POST /sessions/{id}/detach`, process keeps
  running, matches muxalot's detach) or Kill (`DELETE /sessions/{id}`).
- Resize rides `POST /sessions/{id}/resize` debounced by the pane; there is
  no JSON control channel on DevStation streams (docs/PROTOCOL.md).