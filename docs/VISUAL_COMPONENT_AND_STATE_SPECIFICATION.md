# DevStationOne Visual Component & State Specification

This document defines the visual anatomy and required states for the Terminal, File Manager, Database Manager, and responsive/mobile layouts.

## Common States

Every major component must support, where applicable:

- loading
- loaded/normal
- empty
- disabled
- saving/executing
- success
- error
- permission denied
- disconnected/offline
- reconnecting

Errors should appear near the failed operation and explain what failed, why, and what action can resolve it.

## Icon Actions

Small/secondary actions render as **borderless icon buttons** (`.icon-btn`, FontAwesome
Free 7.x solid vendored at `web/static/vendor/fontawesome/`; full map in
`docs/DESIGN_ICON_SYSTEM.md`):

- Anatomy: `<button class="icon-btn [text-danger]" aria-label="…" title="…"><i class="fa-solid fa-…" aria-hidden="true"></i></button>`
- No border, no button chrome; hover/focus show a subtle background tint; focus shows an
  inset focus ring. 44×44px minimum effective target on all viewports.
- Required states: normal, hover, focus-visible, disabled (45% opacity + `not-allowed`).
  Disabled state must remain perceivable without color alone (opacity + cursor).
- Destructive actions (delete, revoke, force stop) use the `text-danger` variant.
- Terminal key-send buttons (Ctrl+C, Ctrl+D, Tab, Esc) keep text labels in the borderless
  `.ghost-btn` style — a glyph cannot convey a key combination.
- Primary CTAs (Create project, Register, Save, Sign in) keep text labels.
- The theme toggle carries both `fa-sun` and `fa-moon`; visibility is CSS-driven from
  `data-bs-theme` (dark shows moon, light shows sun) with no JS involvement.

## Dialogs

All confirmation, text-input, and blocking-error dialogs use the vendored SweetAlert2 library
(`web/static/vendor/sweetalert2/`, helpers in `web/static/js/dialogs.js`: `DS.confirm`, `DS.prompt`,
`DS.error`). Native `window.alert`/`window.confirm`/`window.prompt` must not be used.

- **Confirm** — warning icon, red (`--ds-danger`) Confirm button, Cancel button. Used for every
  destructive action (delete project/file/session, revoke runtime, discard unsaved changes).
  Esc and Cancel both mean "no".
- **Prompt** — text input with default value, Cancel button; empty submits are rejected inline.
  Used for file/folder names, rename/copy/move targets, session names, and destination paths.
- **Error** — error icon, single OK button, blocking modal. Used for API failures surfaced to
  the user.

Dialogs follow the page's `data-bs-theme` (dark default) and use the brand tokens from
`docs/BOOTSTRAP_COLORS.md`.

## Help Popovers

Contextual help (`data-help="<topic-id>"` on an `.icon-btn`, `fa-circle-question`;
markup in `docs/DESIGN_ICON_SYSTEM.md` §8) opens a real `bootstrap.Popover`, not a
custom widget — Tabler's own `.popover`/`.popover-header`/`.popover-body` styling
applies automatically in both themes.

- **Default** — plain `.icon-btn`, same borderless treatment as any other icon action.
- **Hover/focus** — standard `.icon-btn` tint/focus ring; no popover yet (trigger is
  `focus click`, not `hover`, so a mouse pass-over never pops content unintentionally).
- **Open** — popover positioned `auto` (flips to stay on-screen), bold title, one-line
  summary, and a "Read more" link into `/help.html#<topic-id>`.
- **Dismiss** — outside click, `Escape`, or re-clicking the trigger (Bootstrap's own
  focus/click-trigger behavior; no custom close button).
- **Content unavailable** — if a topic's Markdown fails to fetch, the popover shows
  "Help content unavailable." rather than staying empty or throwing — this should
  only ever happen for a misconfigured `data-help` id, never for a shipped topic.
- `project.html`'s single tab-bar help icon updates its title/aria-label/`data-help`
  on every tab switch and hides any currently-open instance of itself, so a stale
  popover from the previous tab can never linger into the new one.

## Terminal Components

```text
Project Header
  -> Application Tabs
     -> Terminal Session Tabs
        -> Terminal Toolbar
           -> Terminal Canvas
```

Session tab anatomy:

`[status] [name] [activity] [close]`

Required session states:

- requested
- starting
- running
- detached
- reconnecting
- connection lost
- exited
- failed
- runtime offline
- runtime revoked

Running sessions use a clear status indicator. Background activity may mark an inactive tab, but selecting the tab clears the activity marker and must never steal focus.

The terminal canvas must support keyboard input, copy/paste, selection, scrollback, resize, mouse behavior, signals, links, and terminal-native control sequences. Application shortcuts must not break normal terminal controls such as Ctrl+C, Ctrl+D, Ctrl+Z, Ctrl+L, Ctrl+A, and Ctrl+R.

### Cards render mode (Slice 17)

An alternate per-session rendering mode, chosen at creation (new-session
dialog: name + xterm/cards choice) and fixed for that session — no
resize/mouse/terminal-native-sequence support, since it is a plain-text
+ structured-card view, not a terminal emulator.

Card states, each falling back to a plain scrollback line when detection
doesn't match:

- yes/no prompt — two buttons (Yes/No)
- numbered menu — one button per option
- diff (`diff --git`) — collapsible card, Collapse/Expand toggle, view-only
- free-text prompt — no card; labels the pane's persistent input box with
  the detected prompt text
- full-screen terminal output detected (vim, htop, fzf, arrow-key menus) —
  a one-line notice, then plain text for the rest of that session; never
  attempts a card again

The pane is scrollback (interleaved plain lines and cards, append-only)
plus one persistent input box fixed at the bottom, always present.

## File Manager Components

Desktop:

```text
File Header / Breadcrumbs / Actions
Tree | File List
```

File list should support name, type, size, modified time, permissions where useful, selection, multi-select, sorting, and contextual actions.

Context actions include open, edit, preview, download, rename, copy, move, delete, and properties. Directory actions also include create file/folder.

Editor states:

- loading
- loaded
- modified
- saving
- saved
- save failed
- read only
- external modification
- deleted
- binary
- too large
- permission denied

Modified files must be visibly marked and closing an unsaved editor requires explicit save/discard handling.

## Source Control Components

Desktop:

```text
Changed Files (Staged / Unstaged) | Diff | Commit box
```

Changed-file list states:

- loading
- clean (no changes)
- changes present
- runtime offline

Each entry shows its status code (modified, added, deleted, renamed —
with its original path, untracked) and stage/unstage actions. Diff view
states: no selection, loading, loaded, diff too large/binary.

Commit box states:

- idle (staged empty — commit disabled)
- ready (staged non-empty, message entered)
- committing
- committed (brief confirmation, list refreshes)
- commit failed

## Journal Components

Table states (per the Common States): loading (`Loading journal…`),
error (inline alert with retry via refresh), empty (`No journal notes
yet` card), loaded (dense table, hover rows). Pagination shows
`Notes N–M of total` with Prev/Next disabled at the ends.

Row anatomy: DateTime cell (created time, plus a secondary `Edited <ts>`
line when modified) | snippet (plain text, truncated) | creator name |
icon actions (view, edit, delete).

Editor modal (XL, static backdrop): Summernote toolbar + editing area,
commit-association card (picker select + free-text SHA input, both
optional), attachments list with download links and remove for
unsaved-only attachments. Save button in the header. States: saving
(disabled while request in flight is implicit — the Save click posts
and closes on success), inline error alert for validation/upload
failures.

Viewer modal (XL): title, metadata line (created at/by, Edited marker),
optional commit association alert, DOMPurify-sanitized body (embedded
images resolve to attachment URLs), attachments list with download
links. States: loading (fetched per open), error alert on fetch
failure.

## Database Manager Components

Desktop:

```text
Connection Tree | Database Workspace
```

Connection states:

- new
- testing
- connected
- disconnected
- connection failed
- authentication failed
- timeout
- TLS error
- permission denied
- driver unsupported
- runtime offline

Schema hierarchy:

```text
Connection
  -> Database/Catalog
     -> Schemas
        -> Tables / Views / Functions
           -> Columns / Indexes / Constraints
```

Main workspace tabs may include Data, Structure, Indexes, Constraints, SQL, Users, Roles, and Permissions as supported by the engine.

Data browser requirements:

- server-side pagination
- filtering
- sorting
- row selection
- insert/edit/delete where supported
- refresh
- horizontal scrolling or responsive transformation

SQL editor states:

- idle
- editing
- executing
- cancelled
- success
- syntax error
- database error
- timeout
- connection lost

Query execution must show progress/state and provide cancellation where supported. Results must distinguish successful empty results from query failures.

## Mobile Layouts

Mobile shell:

```text
Top Navbar
Project Context
Application Tabs
Workspace
Terminal Session Tabs / Contextual Toolbar
```

The sidebar becomes an offcanvas navigation drawer. Application tabs and terminal session tabs horizontally scroll rather than wrapping.

Terminal priority when height is constrained:

1. terminal canvas
2. terminal session tabs
3. application tabs
4. project identity
5. secondary controls

Files on mobile use breadcrumbs plus a list/card representation. Tree navigation opens as a drawer/sheet.

Database connection/schema navigation opens as a drawer/sheet. Dense relational tables may become record cards or remain horizontally scrollable with preserved headers.

Editors and SQL workspaces use nearly the full viewport. When the software keyboard opens, the editor/workspace resizes instead of being hidden.

Services use compact cards with status and primary actions.

## Responsive Breakpoints

Use responsive behavior rather than device-specific assumptions. Suggested guidance:

- under 576px: phone
- 576–767px: large phone/small tablet
- 768–991px: tablet
- 992–1199px: desktop
- 1200px and above: large desktop

Components must adapt to actual available width.

## Touch and Accessibility

Interactive controls should generally have at least a 44x44px effective touch target. Do not make essential functionality hover-only. Support keyboard navigation, visible focus, semantic labels, accessible dialogs, and status text independent of color.

## Final Visual Principle

DevStationOne should feel like a remote development workstation. Terminal, files, databases, and services are integrated views of the same Project context. Workspace content takes priority over application chrome, and every meaningful operational state is visible and actionable.

## Quick Commands Components (Slice 29, ADR-045)

States per the Commands tab (`web/static/js/commands.js`):

- **Loading** — "Loading commands…" text.
- **Error** — alert banner with the API's error message (refresh button
  adjacent).
- **Empty** — card: "No commands yet. Add one by hand or import from a
  Makefile, package.json, or the scripts/ folder."
- **Loaded** — list-group rows: name (semibold), command in monospace
  (truncated, title tooltip), **Run** (primary, small), edit (pencil) and
  delete (trash, danger) icon buttons, and a per-row **History** expander.
- **History expander (collapsed)** — summary "History"; fetches the last
  10 runs for that command on first open only.
- **History row** — exit badge (`bg-success-lt` "exit 0" green / `bg-red-lt`
  "exit N" red / `bg-warning-lt` "running" while the session is live),
  "time ago" of `started_at`, runner display name (email fallback), duration
  (`finished_at - started_at`), and an **Output** button (only when a tail
  or error exists) opening the output modal.
- **Output modal** — read-only monospace `<pre>` of the captured output (full run output, capped at 100 MiB)
  tail (`white-space: pre-wrap`, scrollable); empty tail with an error shows
  the error instead.
- **Add/Edit modal** — Name input + Command textarea (monospace,
  spellcheck off); validation error inline in the modal.
- **Import modal** — scrollable checkbox list grouped by source (Makefile
  targets / `package.json` scripts / `scripts/` executables), each row
  `[checkbox] name — command` (checked by default, name and command
  editable after import in the normal editor); footer "Import selected"
  (disabled when nothing detected).
- **Run in flight** — the Run button disables until the session-create
  round-trip returns; the terminal tab it opens behaves like any session
  (status dot, activity marker, disconnect-safe).
