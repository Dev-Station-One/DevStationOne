# Code Tab — Requirements Specification

Status: implemented (Slice 19, `docs/IMPLEMENTATION_PLAN.md`).

## Goal

A new `Code` tab in the Project workspace: a VS Code-like in-browser editor.
Left: file tree sidebar (reuses the tree component built for the
"Choose a working directory" modal). Right: tabbed open files rendered in
CodeMirror 6. Full width/height editor pane. No competing visual system —
sidebar chrome follows Tabler, editor pane is always dark.

## Decisions made (with Brian)

| Question | Decision |
|---|---|
| Code completion | CodeMirror built-in autocomplete now (buffer words + language packages); LSP hook designed in, LSP itself is a later slice |
| Transport | WebSocket |
| Live-edit semantics | Debounced whole-file sync — no OT/CRDT. Client sends full buffer ~300ms after last keystroke; server pushes whole-file content when an external party (terminal, Claude Code) changes an open file |
| Tree scope | Tree + CRUD context menu (new file/folder, rename, delete) against existing files API; upload/download stay in Files tab |
| Tab persistence | Server-side, per user + project, in Postgres (open tabs + cursor positions) |
| Theme | Editor pane always dark; tabs/sidebar follow Tabler theme |
| Search | In-file find/replace only (CodeMirror built-in); project-wide search is a later slice |
| WS shape | `code.*` types on the runtime control connection; new project-scoped browser editor stream endpoint |
| Change detection | Runtime polls stat (mtime+size) on open files, ~1s |
| Tab-state schema | Single `state jsonb` row per user+project |
| Files cross-link | Files tab deep-links text files into Code tab |
| Fonts | Two account settings (terminal font, editor font) + shared font size; woff2 self-hosted, no CDN |

## Functional requirements

1. **File tree sidebar** — lazy-loading directories rooted at the Project
   working directory (client cannot override root; same trust rules as the
   Files tab). Context menu: new file, new folder, rename, delete. Refresh.
   Click file → opens in editor tab.
2. **Editor tabs** — multiple open files, dirty indicator, close with
   confirmation if unsaved changes are outside the debounce window, middle-click
   / × close, switch active tab.
3. **Live sync over WebSocket** —
   - Open: server streams file content + current mtime/hash for the open set.
   - Save: debounced (~300ms idle) whole-buffer write from client; always-on
     autosave; explicit Ctrl+S forces an immediate flush.
   - External change: server pushes whole-file content + new revision when a
     file in the open set is modified outside the editor (inotify/poll on the
     runtime side); editor reloads buffer non-destructively if the user has no
     pending unsaved edits, prompts/merges conservatively if they do.
   - Binary and oversized files (> ~1–2 MB) refused with a clear message and
     an "open in Files tab" affordance.
4. **Syntax + completion** — CodeMirror 6 language packages for the common
   set (JS/TS, Go, Python, HTML/CSS, JSON, YAML, Markdown, SQL, shell);
   built-in autocomplete keyed by language; completion extension point left
   pluggable for future LSP sources.
5. **Tab state persistence** — server stores open tabs, active tab, and
   cursor/scroll position per user + project; restored when the Code tab is
   reopened.
6. **In-file find/replace** — CodeMirror find/replace panel.
7. **Layout** — full width/height pane; responsive (tree collapses/hides on
   narrow viewports); keyboard shortcuts match VS Code conventions where
   trivial (Ctrl+S save-flush, Ctrl+F find, Ctrl+W close tab...).

## Non-functional requirements

- Authorization identical to existing files endpoints; all paths server-validated
  (no client-trusted paths). WS subject to same per-user auth as terminal WS.
- Terminal I/O and file contents never logged.
- Editor remains responsive on large files within the size cap.
- Survives browser disconnect/reconnect: open file set re-synced on reconnect,
  unsaved buffer flushed before disconnect is best-effort (autosave makes this
  a ≤300ms window).

## User stories / acceptance criteria

1. Open Project → Code tab → see tree of project directory; expand folders
   lazily.
2. Click a source file → opens in a CodeMirror tab with correct syntax
   highlighting for its language.
3. Type → dirty dot appears on tab; ~300ms after typing stops the buffer is
   saved (dirty dot clears); Ctrl+S flushes immediately.
4. In a Terminal tab (or Claude Code) edit the same file → within a couple of
   seconds the Code tab shows the updated content without reload.
5. Right-click file in tree → rename/delete/create work as labeled; tree and
   any open tab stay consistent (deleted open file closes its tab).
6. Reload the browser → reopen Code tab → same open tabs, same active tab,
   cursor positions restored.
7. Open a >2 MB or binary file → refusal message with Files-tab affordance,
   editor stays usable.
8. Find/replace within a file works via Ctrl+F/Ctrl+H.

## Open questions resolved

- WS endpoint: the runtime control connection gains `code.*` message types
  (docs/PROTOCOL.md). The browser-facing side is a new project-scoped
  editor stream endpoint (`GET /api/v1/projects/{projectID}/editor/stream`,
  cookie-auth) — not the terminal session stream, which is session-keyed
  and carries binary PTY frames. One auth path, reuses the runtime
  connection manager; the Control Server fans pushes out per project.
- External-change detection: runtime polls stat (mtime+size) on the open file
  set every ~1s. No inotify watches.
- Editor tab state: single `state jsonb` column per user+project
  (upsert debounced, one SELECT on load).
- Files tab deep-links text files into the Code tab's editor.

## Font preferences (account UI)

8. **Two separate selects** in the account settings UI:
   - **Terminal font**: `system` (browser monospace stack), `jetbrainsmono-nerd`
     (existing), plus plain `fira-code`, `cascadia-code`, `source-code-pro`.
     Plain fonts lose Nerd glyphs — that's accepted.
   - **Editor font**: `system`, `fira-code`, `cascadia-code`, `source-code-pro`
     (JetBrains Mono excluded from the editor list per decision).
   - **Font size**: one shared size applied to terminal + editor; preset select
     (12–18px range).
9. Fonts bundled as woff2, self-hosted by the Control Server (no CDN
   dependency — the app must work on LAN/offline). Each face ~100–400KB,
   regular weight only this slice.
10. Extends the existing `users.terminal_font` preference plumbing
    (`ValidTerminalFont` / `SetTerminalFont` / account settings endpoint):
    new column `editor_font`, new column `font_size`, extended font
    validation lists. xterm.js and CodeMirror read the same preference
    values client-side.

## Explicitly out of scope (later slices)

- LSP-based completions (hook point reserved)
- Project-wide search
- Multi-cursor collaborative/OT editing
- Upload/download, drag-drop in the tree (Files tab keeps these)
- Git integration, diff view, minimap