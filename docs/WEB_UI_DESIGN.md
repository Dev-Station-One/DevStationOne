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
`<header>` element. Global search was part of the original top-navbar
concept but was never implemented; it remains unimplemented, not a top-bar
casualty. A help entry, also from that original concept, **is** implemented
— see "Help system" below.

## Help system

Two parts, both pure static assets — no DB/API/OpenAPI/protocol surface:

- **Contextual popovers**: a `.icon-btn` with `data-help="<topic-id>"` next
  to a page's `<h2>` (every top-level page) or a project workspace tab
  (`project.html`'s single `#app-help-btn`, whose `data-help` is rewritten
  by the `shown.bs.tab` handler to match whichever tab is active — see
  `docs/DESIGN_ICON_SYSTEM.md` §8). Clicking it opens a real
  `bootstrap.Popover` (title + one-line summary + "Read more" link into the
  manual), not a hand-rolled tooltip — see ADR-036 for why that needed a
  second, narrower Bootstrap vendor drop instead of the existing
  `bootstrap.bundle.min.js`.
- **Manual page** (`web/static/help.html`, sidebar entry + global `?`
  shortcut wired in `app.js`): a two-pane TOC/search + rendered-Markdown
  view, reusing the same `marked.parse()` + `DOMPurify.sanitize()` pattern
  `project.html`'s file preview already uses.

Content lives as flat-frontmatter Markdown under `web/static/help/*.md`
(`id`/`title`/`summary` keys, deliberately not YAML — no parser dependency
for three flat fields), enumerated by `web/static/help/index.json` (an id
list only; titles/summaries stay single-sourced in each file's own
frontmatter). `web/static/js/help.js` fetches, caches, and renders both the
popovers and the manual page from that same content. Search is a plain
client-side substring match over title/summary/body — the doc set is small
and single-author, so a fuzzy-search library would be premature.

Adding a topic: drop a new `.md` file with frontmatter into
`web/static/help/`, add its id to `index.json`, and reference it from a
`data-help="<id>"` icon or a `[Link](/help.html#<id>)` cross-reference.

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
- about (opens the About modal — see below)

## About modal

A single shared modal (`#about-modal`), injected into `<body>` on first
open by `renderAboutModal()` in `web/static/js/app.js` — one definition
instead of seven copies in each page's static HTML, matching
`renderSidebar()`/`renderBell()` above. Content: app name, one-line
description, MIT license/copyright, a link into the help manual. Opened
via `tabler.Modal.getOrCreateInstance(...).show()` from the bottom
cluster's "About" nav-link, closed via a plain click handler calling
`.hide()` — deliberately not `data-bs-toggle="modal"` /
`data-bs-dismiss="modal"`, since `project.html` also loads the real
`bootstrap.bundle.min.js` (for `Tab`) and this sidesteps ever needing to
reason about whether a given Data API attribute is safe to double-register
there, the same lesson ADR-034 already paid for once with the
notifications-bell dropdown.

## Projects list

The projects page is a single datatable card listing every project on the
server (ADR-037) — description, owner, and last-accessed only mean
something when the list spans users. Filter row: a text search box
(name/description/working directory) plus owner and runtime selects.
Headers are click-to-sort (`aria-sort`, CSS chevrons in `app.css`); Name
cells show the optional description as a muted sub-line. "New project"
opens a header-button modal shared with row Edit (pre-filled, PATCH);
owner/admin see the Edit/Delete icon buttons, others see an openable row
only. The modal class comes from the `vendor/bootstrap/js/modal/` drop
(ADR-036 pattern — `tabler.min.js` exposes no `window.bootstrap`), opened
programmatically, not via `data-bs-toggle`.

## Project Workspace

A Project is the primary UI context. Show:

`project · runtime · user · working directory` (working directory hidden under 767px — full path stays in the title tooltip)

Application tabs:

`Terminal | Commands | Files | Source Control | Databases | Backups | Docker | Journal`

Terminal session tabs are a separate level:

`bash | Claude Code | npm dev | +`

Do not confuse application tabs with terminal sessions.

The terminal toolbar carries a **dictate-to-terminal** mic action
(`#dictate-toggle`, `icon-btn ds-mic`): speech-to-text via the Web Speech
API (ADR-056), pasting final transcripts into the active session's PTY.
While listening the button carries the `ds-on` pulsing state and an interim
caption chip may appear; see `web/static/help/terminal.md` for the user
facing description.

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
stage/unstage, commit, and pull/push on the current branch — branch
switching, a history graph, and merge/rebase/stash UI are separate later
features.

Show the changed-file list split into Staged and Unstaged groups, each
entry carrying its status code (modified, added, deleted, renamed,
untracked). Selecting an entry shows its diff. Stage/unstage per entry and
in bulk; a commit message box and Commit button are disabled until the
staged group is non-empty and a message is entered.

The panel's header row — current-branch badge, Pull, Push, Refresh — sits
above the changed-file list and stays visible regardless of the panel's
clean/dirty state; it does not disappear when the tree is clean, so
pull/push stay reachable right after a commit. The badge shows ahead/behind
counts against the branch's upstream when one is set. Pull/Push are
disabled (with a tooltip) when the repository has no remote at all. Pull
acts on the current branch's upstream; a resulting merge conflict is shown
as an inline banner naming the conflicted paths, pointing at the Terminal
tab to resolve — this panel does not offer in-browser conflict resolution.
Push targets only the current branch, never forces, and auto-publishes a
branch with no upstream yet on first push.

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

## Users manager (admin)

`users.html` reuses the projects-page datatable + shared-modal pattern
verbatim: search box, click-to-sort headers, role badges, per-row Edit and
Delete icon buttons, one modal for create (email + display name + role +
password) and edit (email locked, blank password keeps the current one).
The `Users` sidebar entry is injected by the shared auth-session fetch in
`app.js` for administrators only; the page itself redirects on a 403, and
the API is the real gate. Guards (self-delete, last admin, ownership
blocks) surface as the server's 400 message in the modal's error alert or
a blocking error dialog.

## Commands tab (Slice 29, ADR-045)

One row per Quick Command: name (bold), the command string in monospace
(truncated, full text on hover), a primary **Run** button, pencil/trash
edit+delete icons, and a collapsible **History** expander per row (fetches
that command's last 10 runs on first open: exit badge, "time ago", runner,
duration, an Output button opening a read-only monospace `<pre>` modal).
Toolbar: **New command**, **Import**, refresh. Import opens a modal listing
detected candidates (grouped Makefile targets / `package.json` scripts /
`scripts/` executables) each with a checkbox — "Import selected" pre-creates
only the checked ones (nothing saves until accepted). Add/Edit is a small
modal: Name + Command (textarea, monospace). Empty state: "No commands yet.
Add one by hand or import from a Makefile, package.json, or the scripts/
folder." A run opens the PTY session's terminal tab (switching to Terminal)
named after the command; rows stack full-width as tap targets on mobile.

## Account page — Sign-in & security

A card between the profile and preference cards (`account.html`, ADR-047):
a `list-group` of the account's second factors loaded from
`GET /api/v1/auth/2fa/methods` — authenticator app, email codes, one row
per passkey (name, added date, "can sign in without a password" when
resident, last-used date), and a backup-codes row with a remaining count.
Row removal is a `text-danger` trash icon behind a `DS.confirm`; the last
remaining factor is refused by the server (409) and surfaced as an error
dialog. Below the list, three outline buttons add a method: authenticator
app (inline setup panel: otpauth key + code input), passkey (WebAuthn
ceremony via the shared `/js/webauthn.js` helpers, same code login.html
uses for enrollment/passwordless), and email codes (single POST). Fresh
backup codes — from a first factor or the Regenerate button — surface in a
SweetAlert2 dialog with a `user-select:all` `<pre>` so the one-time codes
can be copied before the dialog closes.
