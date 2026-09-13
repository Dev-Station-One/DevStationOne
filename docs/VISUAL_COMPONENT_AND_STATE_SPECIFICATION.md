# DevStation Visual Component & State Specification

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

DevStation should feel like a remote development workstation. Terminal, files, databases, and services are integrated views of the same Project context. Workspace content takes priority over application chrome, and every meaningful operational state is visible and actionable.
