# DevStationOne Web UI Design

## Purpose

This is the authoritative web UI design direction for DevStationOne. The browser client is a responsive remote development workstation, not a dashboard-heavy SaaS application.

## Visual Direction

- Use Tabler folded layout with a top navbar and foldable/pinnable left navigation.
- Use Bootstrap 5.3-compatible components and spacing.
- Support light and dark themes through centralized theme variables.
- Keep application chrome compact and give workspace content maximum available space.
- Avoid decorative charts/cards when they do not directly help development work.

## Icons

FontAwesome Free 7.x (solid), vendored self-hosted at
`web/static/vendor/fontawesome/` — see `docs/DESIGN_ICON_SYSTEM.md` for the full
convention and icon map. Rules:

- Small/secondary actions are **borderless icon buttons** (`.icon-btn`): the icon
  alone, no outline/button chrome. Feedback comes from a subtle hover tint, never
  a border.
- Every icon-only action carries an `aria-label` and a `title` tooltip.
- Destructive actions use the `text-danger` variant (trash, ban, force stop).
- Primary CTAs keep their text labels.
- Sidebar navigation, application tabs, page headings, and empty states carry
  icons.
- Actions whose meaning a glyph cannot convey (terminal key sends such as
  Ctrl+C/Tab/Esc) keep their text label but use the borderless `.ghost-btn` style.
- The approved deviation from Tabler's native icon set is recorded in
  `docs/REQUIREMENTS_ICON_SYSTEM.md`.

## Navigation

Sidebar sections (full design):

- WORKSPACE: Dashboard, Projects, Runtimes
- TOOLS: Files, Source Control, Databases, Services
- SYSTEM: Activity, Settings

Until the TOOLS and SYSTEM features are implemented, those sections are
hidden from the sidebar entirely (items and their section headers) rather
than shown as disabled entries. They return section by section as each
feature ships.

The sidebar markup is rendered by `web/static/js/app.js` from a single
template; each page carries only `<aside id="ds-sidebar" data-active="…">`
with the navbar scaffold classes. Collapsed (folded) sidebar swaps the
stacked brand lockup for the square favicon mark (`favicon-32.png`) and
hides nav labels via Tabler's `.nav-link-title` folded behavior.

**Deviation from earlier design**: there is no separate top bar. The fold
toggle, theme toggle, notifications bell, and account/logout links (wired
centrally — pages do not duplicate the markup or handler) are rendered by
the same `renderSidebar()` template in `web/static/js/app.js`, as a
bottom-pinned `<ul class="navbar-nav mt-auto">` inside `#sidebar-menu` (so
the mobile navbar toggler reveals it, and Tabler's folded-hover CSS
collapses/reveals it with the rest of the rail). No page carries a
`<header>` element. Global search and a help entry were part of the
original top-navbar concept but were never implemented; they remain
unimplemented, not top-bar casualties.

Every item in that bottom cluster is a real `.nav-link` (icon +
`.nav-link-title`), styled identically to Dashboard/Projects/Runtimes above
it — not a bare icon-button toolbar — so it gets the same hover-expand
label and matches the rest of the sidebar instead of reading as a bolted-on
row of icons. The fold/pin toggle uses `fa-thumbtack`/`fa-thumbtack-slash`
(not `fa-bars`, which collides with the unrelated mobile navbar-toggler
hamburger elsewhere on the page) — the icon itself swaps to reflect
pinned vs. unpinned state via `[aria-pressed="true"]`, same pattern as the
sun/moon theme toggle, plus Tabler's own active-nav-item tint — when the
sidebar is pinned open.

Account and Log out are two independent nav-links, not a dropdown menu —
one less interactive layer for two of the most-used actions. Notifications
is the only dropdown left, and the only one that needs real positioning:
`#sidebar-menu` scrolls internally (`overflow:auto`, for a long nav list),
which clips anything absolutely-positioned trying to escape the rail —
the same reason Tabler forces vertical-sidebar dropdown-menus to
`position:static` (and strips their chrome) in the first place, since
they're designed as in-place accordion submenus, not flyouts. The panel
opts out of that: `app.js`'s `positionPanel()` sets `position:fixed` with
`top`/`left` computed from the bell's real screen position (clamped to the
viewport width for narrow screens) each time it opens, and its chrome
(background/border/shadow) is restored from Tabler's own dropdown design
tokens in `app.css`. Its rows reuse this app's existing `.list-group`
pattern (see `project.html`'s git-status and journal-attachment lists)
rather than bare `dropdown-item-text` rows.

The sidebar must never load a second Bootstrap copy: Tabler bundles its
own, and a second copy's data-api closes every dropdown the moment the
first opens (Summernote therefore uses the jQuery-only `lite` build,
not the Bootstrap-5 build; `project.html` additionally loads real
Bootstrap for modals/tabs and works around the same collision for its
sidebar dropdown-toggle button (notifications) specifically, per ADR-034).

Sidebar bottom cluster:

- fold/pin toggle (`data-bs-toggle="sidebar-folded"`, `fa-thumbtack`)
- theme toggle
- notifications bell (`.dropend` flyout panel)
- account (link to `/account.html`)
- log out

## Project Workspace

A Project is the primary UI context. Show:

`project · runtime · user · working directory`

Application tabs:

`Terminal | Files | Source Control | Databases | Services | Journal`

Terminal session tabs are a separate level:

`bash | Claude Code | npm dev | +`

Do not confuse application tabs with terminal sessions.

## Terminal

Terminal is the primary workspace. Use xterm.js or another mature terminal emulator. Terminal sessions are persistent server-side PTYs and survive browser disconnects.

The terminal canvas gets the majority of available workspace height. Toolbars stay compact. Session status must be visible and must not rely on color alone.

New-session creation asks for a name and a render mode (xterm, the
default, or cards — Slice 17), chosen once and fixed for that session's
lifetime. Cards mode (`web/static/js/termcards.js`) replaces the xterm.js
canvas with a plain-text scrollback plus structured cards for recognized
prompts — a yes/no card, a numbered-menu card, and a collapsible diff card
— each with a hard fallback to plain text on no match, and a permanent
fallback to plain text for the rest of the session the moment full-screen
terminal output (vim, htop, fzf, an arrow-key menu) is detected. A single
persistent input box at the bottom of the pane sends typed lines; it picks
up the detected prompt text as its label for a free-text prompt instead of
a separate input card.

## Files

Files is a project-aware file manager with breadcrumbs, tree/list navigation, search, create, upload/download, rename, copy, move, delete, metadata, editor, and preview. The Project directory is the default context.

The editor supports syntax highlighting, search/replace, save state, Markdown preview, image preview, and appropriate large-file safeguards.

## Source Control

Source Control is a VSCode-style panel scoped to status, diff,
stage/unstage, and commit — branch/push/pull, history graph, and
merge/rebase/stash are separate later features.

Show the changed-file list split into Staged and Unstaged groups, each
entry carrying its status code (modified, added, deleted, renamed,
untracked). Selecting an entry shows its diff. Stage/unstage per entry and
in bulk; a commit message box and Commit button are disabled until the
staged group is non-empty and a message is entered.

## Databases

Databases is a database IDE-style workspace. Desktop uses a connection/schema tree beside the main workspace. It supports PostgreSQL, MySQL/MariaDB, SQL Server, and SQLite.

Provide connection management, catalog/schema/table browsing, data browsing, pagination/filtering/sorting, SQL editing, query results, and engine-appropriate user/role/permission management.

SQLite must explicitly indicate unsupported server-style user/role administration.

## Services

Services represents `systemd --user` services where available. Show service status, start/stop/restart, enable/disable, and logs.

## Journal

Developer notes for the Project (Tabler datatables pattern,
`docs/JOURNAL_REQUIREMENTS.md`). A server-side-paginated table —
DateTime (creation time, with an "Edited <ts>" sub-line when modified) |
HTML-stripped snippet | Created By (display name, email fallback) |
action icons (view, edit, delete) — with row click (or the view icon)
opening an XL modal that renders the full note.

- Editor: XL modal with Summernote (Bootstrap 5 build, vendored), an
  optional commit association picker (`git log` — SHA | subject | author
  | datetime, plus a free-text SHA field), and an attachment list
  (upload via paperclip; pasted/uploaded images insert into the body and
  are stored as attachments, never inline base64).
- Viewer: XL modal with the note body rendered through DOMPurify (note
  HTML is untrusted), creation/edit metadata, the commit association,
  and downloadable attachment links.
- Delete requires confirmation. Timestamps render with the app's Intl
  formatter ("Mon Sep 14, 11:12 AM" shape).

## Mobile

Mobile is a first-class responsive workspace, not a shrunken desktop UI.

- Tabler sidebar becomes offcanvas.
- Application tabs horizontally scroll.
- Terminal session tabs horizontally scroll.
- Terminal receives maximum viewport space.
- File/database trees become drawers or sheets.
- Dense tables become scrollable or card/list representations.
- Forms become single-column.
- Touch targets should generally provide at least 44x44px effective hit areas.
- No essential behavior may depend on hover.

## Accessibility

Use semantic HTML, keyboard navigation, visible focus, accessible dialogs/menus, ARIA labels where necessary, sufficient contrast, and status indicators that do not depend solely on color.

## API-First Rule

Every browser capability must use the documented API. The browser must not become an authorization authority and must not override server-resolved Project, runtime, filesystem, session, database, or service context.
