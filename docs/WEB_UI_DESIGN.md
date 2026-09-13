# DevStation Web UI Design

## Purpose

This is the authoritative web UI design direction for DevStation. The browser client is a responsive remote development workstation, not a dashboard-heavy SaaS application.

## Visual Direction

- Use Tabler folded layout with a top navbar and foldable/pinnable left navigation.
- Use Bootstrap 5.3-compatible components and spacing.
- Support light and dark themes through centralized theme variables.
- Keep application chrome compact and give workspace content maximum available space.
- Avoid decorative charts/cards when they do not directly help development work.

## Navigation

Sidebar sections:

- WORKSPACE: Dashboard, Projects, Runtimes
- TOOLS: Files, Databases, Services
- SYSTEM: Activity, Settings

Top navbar:

- sidebar toggle
- DevStation branding
- global search
- notifications
- help
- user menu

## Project Workspace

A Project is the primary UI context. Show:

`project · runtime · user · working directory`

Application tabs:

`Terminal | Files | Databases | Services`

Terminal session tabs are a separate level:

`bash | Claude Code | npm dev | +`

Do not confuse application tabs with terminal sessions.

## Terminal

Terminal is the primary workspace. Use xterm.js or another mature terminal emulator. Terminal sessions are persistent server-side PTYs and survive browser disconnects.

The terminal canvas gets the majority of available workspace height. Toolbars stay compact. Session status must be visible and must not rely on color alone.

## Files

Files is a project-aware file manager with breadcrumbs, tree/list navigation, search, create, upload/download, rename, copy, move, delete, metadata, editor, and preview. The Project directory is the default context.

The editor supports syntax highlighting, search/replace, save state, Markdown preview, image preview, and appropriate large-file safeguards.

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
