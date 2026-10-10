# XTERM.md — Terminal addon slice

State after requirements review (2026-10-10). Scope agreed in brainstorm; no
implementation yet. This slice concerns the web client's xterm.js terminal only.

## Current state (verified)

- Vendored xterm.js core, offline-served under `web/static/vendor/xterm/` per
  the no-CDN/CSP-strict asset policy (same decision as the icon system,
  docs/REQUIREMENTS_ICON_SYSTEM.md). Core reports version `6` — addons must be
  vendored from the matching 6.x line; mixed-version addon/core pairs are the
  known breakage mode.
- Only `addon-fit` is loaded (`vendor/xterm/addon-fit.js`),
  instantiated in `paneFor()` (`web/static/project.html`).
- **No** copy/paste or clipboard handling anywhere: no
  `navigator.clipboard` use, no `attachCustomKeyEventHandler`, no selection copy.
- Multiple sessions per project already work end to end: `termState` map,
  one WebSocket per session (`/api/v1/sessions/{id}/stream`), session tabs with
  activity badges, reconnect with backoff (`scheduleReconnect`), server-side
  256 KiB scrollback tail replayed on attach (`internal/sessions/hub.go`).
- Cards render mode (`web/static/js/termcards.js`, Slice 17) is a separate
  terminal presentation with no xterm.js instance. Out of scope for addons.

## Slice scope: vendor and wire five addons

| Addon | Purpose | UI work |
|---|---|---|
| `addon-clipboard` | Ctrl+Shift+C/V copy/paste, OSC 52 from terminal apps | minimal (key interception) |
| `addon-unicode11` | Correct character widths for emoji/CJK (Claude Code output is emoji-heavy) | none |
| `addon-web-links` | Clickable URLs in output | none |
| `addon-search` | Find in terminal buffer | yes — find bar UI (input, next/prev, match count, close) |
| `addon-progress` | OSC 9;4 progress events → determinate progress on the session tab | minimal (reuse existing tab badge UI) |

All five are vendored locally and loaded per-session in `paneFor()` next to
FitAddon, disposed with the session lifecycle (`termState`).

## Key integration points

- **Shared key-interception layer:** a single `attachCustomKeyEventHandler` in
  `paneFor()` intercepts clipboard keys (Ctrl+Shift+C/V) and search
  (Ctrl+Shift+F) before `onData` forwards them to the PTY. Plain Ctrl+C, Ctrl+F
  must reach the shell untouched (readline meanings).
- **Search scope:** client-side over the xterm.js buffer = live output since
  attach + the up-to-256 KiB server scrollback replay. History from before the
  client attached is not searchable (same per-client limit tmux panes have).
- **Progress:** fires only when a running program emits OSC 9;4. Inert until
  then; harmless. Wire the event to the existing per-tab activity badge so
  background builds/agent runs show determinate progress. Emitters are
  tool-specific — verify in a live session before promising behavior.
- **Cards mode:** no addon loading, no search UI, no progress wiring there.

## Constraints and caveats

- Everything served from `web/static/vendor/` — no CDN, no CSP exceptions.
- **Secure context:** `navigator.clipboard` (used by the clipboard addon)
  requires HTTPS. Works on the public host upstream of TLS
  (`ds1.premadev.com`); unavailable on the bare-LAN HTTP upstream
  (`http://192.168.25.165:18091`) — copy from selection may degrade, paste-in
  needs the user-gesture fallback path. Call this out in the UI rather than
  silently failing.
- Addon versions must match the vendored core (6.x) exactly.

## Non-goals (reviewed, rejected for now)

- `addon-attach` — legacy Node/socket attach plumbing; the project has its own
  WebSocket protocol.
- `addon-ligatures` — requires canvas renderer; typography niche.
- `addon-canvas` — legacy renderer, superseded by webgl; fallback only.
- `addon-serialize` — serializes the client buffer; server scrollback already
  covers the need. Reconsider only if a feature consumes it.
- `addon-webgl` — real perf win, but new failure modes (context loss,
  cards-mode interplay). Add only if rendering lags measurably.
- `addon-image` (sixel/iTerm images) — niche tooling; defer until wanted.
- `addon-web-fonts` — terminal fonts already handled via
  `TERMINAL_FONT_STACKS` / font-size prefs in `project.html`.
- `addon-unicode-graphemes` — full grapheme clustering; only revisit if
  unicode11 alone leaves broken emoji clusters.

## Out of scope, previously parked

- tmux in the other project (muxalot) — dropped; its concerns don't apply here.
- Server-side scrollback: already exists (`internal/sessions/hub.go`,
  256 KiB tail, replay-on-attach). Not part of this slice.
- PTY death on runtime restart and scrollback reflow after resize-while-
  detached: known limitations, unchanged, outside this slice.

## Acceptance criteria

1. Terminal renders emoji/CJK-aligned output correctly (unicode11 active).
2. URLs in output are clickable (web-links active).
3. Ctrl+Shift+C copies selection to the system clipboard; Ctrl+Shift+V pastes
   from it; shell receives plain Ctrl+C/F unmodified; OSC 52 from an app
   reaches the clipboard.
4. Ctrl+Shift+F opens a find bar; next/prev/highlights work; close restores.
5. A program emitting OSC 9;4 surfaces progress on its session tab; tabs of
   non-emitting programs are unaffected.
6. All five addons work with zero external network requests (CSP holds).
7. Regression: session tabs, reconnect, resize, and cards mode behave exactly
   as before with addons loaded.