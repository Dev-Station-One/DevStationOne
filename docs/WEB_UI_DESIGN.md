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

Top navbar:

- sidebar toggle
- DevStationOne branding
- global search
- notifications
- help
- user menu

## Project Workspace

A Project is the primary UI context. Show:

`project · runtime · user · working directory`

Application tabs:

`Terminal | Files | Source Control | Databases | Services`

Terminal session tabs are a separate level:

`bash | Claude Code | npm dev | +`

Do not confuse application tabs with terminal sessions.

## Terminal

Terminal is the primary workspace. Use xterm.js or another mature terminal emulator. Terminal sessions are persistent server-side PTYs and survive browser disconnects.

The terminal canvas gets the majority of available workspace height. Toolbars stay compact. Session status must be visible and must not rely on color alone.

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
