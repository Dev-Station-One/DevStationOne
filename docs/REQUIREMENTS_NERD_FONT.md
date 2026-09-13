# Requirements: Nerd Font support in the terminal (user preference)

Status: **Implemented** (storage: `users.terminal_font` column, migration 0006).

## Goal

Users whose terminal programs (powerline prompts, tmux themes, `ls` icons,
Claude Code's glyph output) emit Nerd Font glyphs must see them rendered
instead of tofu boxes, controlled by a preference that each user sets
themselves and that follows them across devices.

## Decisions (fixed)

| Decision | Choice |
|---|---|
| Font source | Vendored server-side in `web/static/vendor/fonts/` (woff2) |
| Font | JetBrainsMono Nerd Font **Mono** (single Regular weight) |
| Preference storage | Account-level, server-side, follows the user across devices |
| Setting UI | Selector in the terminal toolbar (next to Ctrl+C / Tab / Esc) |
| Applies to | Terminal (xterm.js) only — CodeMirror editor and rest of UI unaffected |

## Functional requirements

1. **Font asset**: one `JetBrainsMonoNerdFontMono-Regular.woff2` vendored in
   the repo, served as a static file. `@font-face` declared with
   `font-display: swap`; browsers fetch it only when the preference is
   active.
2. **Preference model**: every user has a terminal font preference with
   exactly two values:
   - `system` (default) — current behavior, unchanged;
   - `jetbrainsmono-nerd` — xterm.js `fontFamily` set to
     `"JetBrains Mono NFM", monospace`.
   The preference must survive browser restarts, devices, and sessions.
3. **API**: the preference is readable and updatable through the
   authenticated REST API (OpenAPI-documented), consistent with the
   API-first rule — future clients must be able to set it without a
   browser.
4. **UI**: a compact selector in the terminal toolbar. Changing it applies
   immediately to every open session (all `termState` entries re-render
   with `st.term.options.fontFamily = ...`), and persists via the API.
   Sessions opened later must pick up the saved preference on creation.
5. **States** (per the visual state spec): selector disabled while the
   request is in flight; save failure surfaces a near-the-operation error
   and leaves the previous value selected; loading the account on a fresh
   browser reflects the server-stored value before the first terminal opens.
6. **Rendering**: Nerd Font glyphs live in Private Use Areas; no changes to
   the binary WebSocket framing or the runtime are required or permitted.

## Non-functional requirements

- Repo growth from the font: ≤ ~1.5 MB (single woff2).
- No CDN dependency — everything served from the Control Server (CSP
  allowlist already restricts external origins).
- Licensing respected: JetBrains Mono is SIL OFL 1.1, Nerd Fonts patches
  are MIT; the vendored directory carries a LICENSE note naming both.
- No runtime or protocol changes. No migration beyond preference storage.

## User stories / acceptance criteria

1. With preference `system`, a terminal running a Nerd-Font prompt shows
   tofu (current behavior) — unchanged baseline.
2. User selects `JetBrainsMono Nerd Font` in the toolbar: within one
   second, an open powerline prompt renders its glyphs; a newly opened
   session renders glyphs without interaction.
3. User logs in on a different browser/device: preference is already
   active; terminals render glyphs.
4. Font file missing/corrupt: terminals still render (fallback to
   `monospace`), no console errors beyond the failed font fetch.
5. API client sets the preference via the documented endpoint; the browser
   reflects it on next load.

## Open questions (for implementation planning, not blockers)

- Storage shape: dedicated `users.terminal_font` column vs a `preferences`
  JSONB column — decide in design; either satisfies requirement 2.
- Whether to also ship a Bold weight for crisper faux-bold avoidance
  (default: no — browser-synthesized bold is acceptable for v1).
