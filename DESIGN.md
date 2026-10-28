---
name: DevStationOne
description: Self-hosted remote development workstation platform — a dark signal-console UI on Tabler 1.5.1/Bootstrap 5.3.
colors:
  signal-teal: "#00E5A8"
  arc-cyan: "#00C2FF"
  aux-violet: "#855CF6"
  confirm-green: "#10B981"
  instrument-blue: "#3B82F6"
  amber-beacon: "#F59E0B"
  alarm-red: "#EF4444"
  void-navy: "#0B1220"
  console-slate: "#1E293B"
  raised-console: "#243449"
  deck-plate: "#64748B"
  panel-seam: "#334155"
  instrument-grey: "#94A3B8"
  console-light: "#E2E8F0"
  daylight-deck: "#F8FAFC"
  paper-panel: "#FFFFFF"
  mist-plate: "#F1F5F9"
  daylight-seam: "#CBD5E1"
  ink-navy: "#0F172A"
  fog-grey: "#475569"
typography:
  body:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, San Francisco, Segoe UI, Roboto, Helvetica Neue, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, Courier New, monospace"
rounded:
  md: "6px"
spacing:
  tight: "4px"
  base: "8px"
  section: "16px"
components:
  button-primary:
    backgroundColor: "{colors.signal-teal}"
    textColor: "{colors.void-navy}"
    rounded: "{rounded.md}"
  button-primary-hover:
    backgroundColor: "#00CF95"
    textColor: "{colors.void-navy}"
  button-primary-active:
    backgroundColor: "#00B884"
    textColor: "{colors.void-navy}"
  icon-btn:
    backgroundColor: "transparent"
    textColor: "{colors.instrument-grey}"
    rounded: "{rounded.md}"
    width: "44px"
    height: "44px"
  ghost-btn:
    backgroundColor: "transparent"
    textColor: "{colors.instrument-grey}"
    rounded: "{rounded.md}"
    height: "44px"
  terminal-canvas:
    backgroundColor: "#000000"
    rounded: "{rounded.md}"
    height: "60vh"
  link:
    textColor: "{colors.arc-cyan}"
  link-hover:
    textColor: "{colors.signal-teal}"
---

# Design System: DevStationOne

## Overview

**Creative North Star: "The Signal Console"**

Precise, dense, quietly luminous. A dark operations deck that stays neutral until a signal needs your eye — then one lamp lights. The user is a developer operating their own machines; the interface is the instrument panel between them and every runtime, terminal, and database they own. It earns trust by being readable at a glance from across the room: deep navy surfaces, high-contrast instrument text, and a strict lamp discipline where teal means "act here," cyan means "go there," and status colors only ever announce states.

Density is a feature, not a compromise. Tables are dense (`table-vcenter`), chrome is compact, and workspace content — above all the terminal canvas — takes the majority of available space. Decoration has no lane: there are no decorative charts, no marketing gradients across page backgrounds, no chrome that competes with the PTY. Aesthetics come from discipline: 14px system type, 6px radii, borderless icon actions, and lamp badges doing all the emotional work.

The dark theme is the default instrument (the app boots dark); light theme exists and flips surface/text tokens only, preserving the brand lamps. Light theme is a convenience for daylight, not a second identity.

**Key Characteristics:**
- Dark navy workstation surfaces (`Void Navy` #0B1220 on `Console Slate` #1E293B), 14px system UI type, 6px corners.
- One strict accent pair: `Signal Teal` #00E5A8 for primary actions/active states, `Arc Cyan` #00C2FF for links and navigation.
- Status is lamps: low-tint Tabler badges (`bg-*-lt`) always paired with text or an icon, never color alone.
- Borderless icon actions with 44×44px effective targets and inset focus rings; primary CTAs keep text labels.
- Terminal-first: a pure-black xterm.js canvas with 60vh (70vh desktop) of workspace height; the gradient never approaches it.
- Brand gradient (`#00E5A8 → #00CFEA → #00C2FF`) appears only in the logo lockup and restrained brand accents.

## Colors

A dark instrument palette: navy void surfaces, slate structure, and one neon teal/cyan voice; status colors are operational lamps, not decoration.

### Primary
- **Signal Teal** (#00E5A8): Primary actions, important active states, brand accent. Primary buttons paint it with dark `Void Navy` text, never white. Also the link hover color. Used sparingly — its rarity is the point.
- **Arc Cyan** (#00C2FF): Links, secondary actions, focus-visible outline, navigation signals. The "go here" color; deliberately cooler and calmer than teal.

### Secondary (Operational Lamps — Tertiary)
Status colors, always carried by a Tabler low-tint badge (`bg-green-lt`, `bg-red-lt`, `bg-yellow-lt`, …) with readable text or an icon alongside:
- **Confirm Green** (#10B981): successful operations, online, active sessions.
- **Amber Beacon** (#F59E0B): warnings, attention, offline, unsaved-dirty cells.
- **Alarm Red** (#EF4444): errors and destructive actions (`text-danger` icon actions, confirm dialogs). Never large fills.
- **Instrument Blue** (#3B82F6): informational states.

### Tertiary
- **Aux Violet** (#855CF6): optional accent, used rarely if at all. Do not introduce secondary usage without a reason; it is the palette's spare fuse.

### Neutral
- **Void Navy** (#0B1220): application background, dark `data-bs-theme="dark"` base; also primary-button text on teal.
- **Console Slate** (#1E293B): persistent surfaces — cards, panels, navigation.
- **Raised Console** (#243449): elevated panels and popovers; buys depth by tone, not shadow.
- **Panel Seam** (#334155): the `--bs-border-color` dark border on inputs and structural edges.
- **Deck Plate** (#64748B, used with opacity): shared borders and dividers (`--ds-border`).
- **Instrument Grey** (#94A3B8): muted text — secondary metadata only, never critical content; also idle icon-action color.
- **Console Light** (#E2E8F0): default body text on dark.
- **Pilot White** (#FFFFFF): headings/emphasis on dark.

Light theme (`[data-bs-theme="light"]`, same lamps, swapped neutrals): **Daylight Deck** #F8FAFC background, **Paper Panel** #FFFFFF surfaces, **Mist Plate** #F1F5F9 elevated, **Daylight Seam** #CBD5E1 borders, **Ink Navy** #0F172A text, **Fog Grey** #475569 muted. Brand lamps are identical in both themes.

### Named Rules

**The Gradient Lock Rule.** The teal→cyan brand gradient belongs to the logo lockup and restrained brand accents only. Never as a page background, a card fill, or anything near terminal output.

**The Lamp Rule.** Status never depends on color alone: every state pairs its lamp color with text or an icon, in a low-tint badge — not a saturated fill.

**The Neutral Glass Rule.** Terminal output is visually neutral. Brand colors and the gradient never tint terminal text; the terminal is real instruments, not marketing surface.

## Typography

**Display/UI Font:** System UI stack (`system-ui, -apple-system, BlinkMacSystemFont, San Francisco, Segoe UI, Roboto, Helvetica Neue, sans-serif`) — Tabler's default, no UI webfont.
**Body Font:** Same stack, base size 0.875rem (14px).
**Mono Font:** Tabler stack (`ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, Courier New, monospace`) by default, with four self-hosted 400-weight coding faces the user can switch to per preference (`web/static/css/fonts.css`): **JetBrains Mono Nerd Mono**, **Fira Code**, **Cascadia Code**, **Source Code Pro**. Each `@font-face` loads only once a preference selects it.

**Character:** Machine-neutral and fast-loading — the UI type is whatever the OS renders best, and identity is carried by color discipline rather than letterforms. Code, commands, SQL, and diffs are always monospace.

### Hierarchy
- **Display/Page heading** (Tabler `h2`, semibold, page top): page identity; usually paired with a leading FontAwesome icon and a trailing help `icon-btn`.
- **Section/Card title** (Tabler `h3`, semibold, `card-title`): card and panel headers, with icons.
- **Pretitle** (`page-pretitle`, muted small): project context line / breadcrumb trail above headings.
- **Body** (0.875rem / 14px, 400): table cells, form labels, cards. Dense by design.
- **Label/Meta** (`text-secondary`, small): timestamps, descriptions under names, helper text, empty states.
- **Mono** (0.8125rem–13px): command strings, git diffs, session scrollback, SQL, backup codes.

### Named Rules

**The System Stack Rule.** No UI webfont is shipped. The interface renders in the platform's system stack — self-hosted means self-efficient; identity lives in the lamps, not letterforms.

## Layout

A Tabler 1.5.1 folded-sidebar layout: a single left rail (`navbar-vertical` + `navbar-expand-lg`) is the app's only global chrome. On ≥992px the rail is foldable and pinnable (`folded-hover`); pages carry no `<header>` element — fold toggle, theme toggle, notifications bell, account, log out, and about live in a bottom-pinned cluster of the same rail. Below 992px the vertical rail becomes a horizontal top navbar with the cluster behind the hamburger as offcanvas.

- Pages are `container-fluid` with a `page-body` wrapper; page rhythm is `mb-3` (16px) between title row, toolbars, and content.
- Content is dense Tabler patterns: `row row-cards` grids of `card` blocks, `table card-table table-vcenter` data tables (`min-width: 640px` with horizontal scroll on mobile), single-card datatables for list pages (projects, users).
- The Project workspace is the density peak: application tabs (Overview, Terminal, Commands, Files, Code, Source Control, Databases, Backups, Docker, Journal — horizontally scrollable), and inside Terminal a second tab level for sessions. Do not confuse application tabs with terminal session tabs.
- Workspace priority when height is scarce: terminal canvas (60vh, 70vh ≥992px, min 320px) > session tabs > application tabs > project identity > secondary controls.
- Breakpoints follow Bootstrap/Tabler: 576px (phone), 768px (tablet), 992px (desktop — the rail folds), 1200px (large desktop). Components adapt to actual available width; under 767px the working directory leaves the project header (title tooltip keeps it) and app-tab labels collapse to icon-only (labels stay for screen readers).
- Touch: effectively ≥44×44px hit areas on icon actions, toolbar buttons, and table buttons; nothing essential is hover-only.

## Elevation & Depth

Depth is tonal first, shadow second: surfaces step up through `Void Navy` → `Console Slate` → `Raised Console`, hover feedback is a tint (`--tblr-bg-surface-secondary`), never a border or drop shadow. Real shadows are reserved for things that float above the document: the notifications flyout, modals, and dropdowns (Tabler's own shadow tokens). Nothing at rest casts a shadow.

### Shadow Vocabulary
- **Floating chrome** (`var(--tblr-dropdown-box-shadow)` / Tabler modal shadow): dropdown flyouts, popovers, modals only — never panels, never cards at rest.

### Named Rules

**The Flat-At-Rest Rule.** Panels, cards, and tables sit flat on their surface. If an element is flat at rest, it gets depth from a tone step or a border, not a shadow.

## Shapes

- Radios are small and consistent: 6px (`--tblr-border-radius`) on buttons, inputs, cards, panels; badges are Tabler low-tint pills.
- Tabs (application and session) are top-corner-only rounded (6px top), sitting flush on the toolbar below — `session-tab` carries a top border + side borders and drops the bottom border, the classic file-folder silhouette.
- Icon actions have no shape to speak of: transparent background, no border, and hover adds only a surface tint. Focus is an inset 2px ring (`outline: 2px solid var(--ds-secondary); outline-offset: -2px`) in Arc Cyan.
- Inputs are Bootstrap/Tabler defaults: 6px radius, `Panel Seam`/`Daylight Seam` border, surface background; no glows.

## Components

### Buttons
- **Shape:** 6px radius (Tabler default).
- **Primary:** `Signal Teal` fill with `Void Navy` text (never white), Bootstrap `--bs-btn-*` themed in `app.css` because neither Bootstrap nor Tabler hands `--bs-primary` to buttons. Hover #00CF95, active #00B884, both keeping dark text. Text labels required ("Sign in", "Create project", "Run", "Compose up").
- **Outline/Secondary:** `btn-outline-secondary` and `btn-outline-primary` for the alternatives row (passkey sign-in, 2FA enrollment choices).
- **Ghost** (`.ghost-btn`): borderless text buttons for terminal key-sends (Ctrl+C, Ctrl+D, Tab, Esc) — a glyph cannot convey a key combination. Idle `Instrument Grey`, hover tints to surface + `Pilot White`.
- **Icon actions** (`.icon-btn`): borderless FontAwesome buttons — the icon is the whole control, 44×44px minimum, hover = surface tint only. Destructive variants add `text-danger` (trash, ban, force stop). Disabled = 45% opacity + `not-allowed`, plus outline for chrome-free perception.
- **Focus:** inset Arc Cyan ring on ghost/icon buttons; default Bootstrap focus ring on labeled buttons.

### Chips (Badges)
- **Style:** Tabler low-tint `bg-*-lt` pills — green/gray/yellow/red families map to runtime status, session counts, exit codes, branch ahead/behind (`bg-green-lt` "online", `bg-red-lt` "exit 1", `bg-warning-lt` "running", `bg-secondary-lt` "0").
- **State:** lamps announce a state; they are never the only carrier of it (text is inside or beside).

### Cards / Containers
- **Corner Style:** 6px; Tabler `card` chrome with `card-header` titles carrying icons.
- **Background:** theme surface (`Console Slate` dark / `Paper Panel` light) via Tabler tokens.
- **Shadow Strategy:** flat at rest; none (see Elevation).
- **Border:** Tabler default panel border; `list-group` items are the recurring inner rows (git status entries, journal attachments, notification rows).
- **Internal Padding:** Tabler card padding; dense tables sit flush inside `table-responsive` wrappers.

### Inputs / Fields
- **Style:** Bootstrap/Tabler defaults — surface background, 6px radius, 1px seam border; small variants (`form-select-sm`) inside toolbars.
- **Focus:** native Bootstrap focus ring; SQL editor (`CodeMirror`) keeps a seam border and surface background.
- **Error:** inline `alert alert-danger` near the failed operation; errors explain what failed, why, and possible action. Table edit states tint cells: dirty `bg-warning-lt`, new `bg-success-lt`, deleted struck through on `bg-danger-lt`.

### Navigation
- **Style:** dark vertical rail (`data-bs-theme="dark"`) with fixed 1.25em icon gutter (`fa-solid` centered, 0.5rem gap) so labels align; sections in small-caps nav titles; full stacked brand lockup (68px, 32px folded, 40px mobile) at the top.
- **States:** Tabler active tint for active page; hover-expand labels when folded; the pinned state pins the thumbtack icon (icon swaps state, off Tabler's `aria-pressed`).
- **Bottom cluster:** fold/pin, theme toggle (sun/moon CSS swap), notifications bell with fixed-position 22rem flyout panel (computed placement, Tabler dropdown chrome restored from tokens), account, log out, about — all real `.nav-link` items, not a bolted-on icon row.
- **Mobile:** offcanvas drawer behind the hamburger; content gets the full width.

### Dialogs
- Confirmations, prompts, and blocking errors use SweetAlert2 (`DS.confirm`, `DS.prompt`, `DS.error` in `dialogs.js`). Confirm: warning icon, `Alarm Red` confirm button, themed by `data-bs-theme`. Native `alert`/`confirm`/`prompt` never appear.

### Tables
- **Style:** Tabler `table-vcenter` datatables; sortable headers are the `sort-th` pattern — click-to-sort with `aria-sort`, resting `⇅` chevron at 35% opacity, one-direction arrow at full opacity.
- **States:** loading text, `empty` cards with icon + title + subtitle + action link, inline error alerts, horizontal scroll wrappers on mobile.

### Terminal (Signature)
- The instrument the whole system exists for: an xterm.js canvas on pure black (#000) filling 60–70vh of the workspace, with a compact dark toolbar above (key-send ghosts, font preference selects, session status text, force-stop danger icon).
- Session tabs are a separate level of tabs under application tabs, each carrying `[status dot] [name] [activity] [close]` with a full lifecycle state set (requested → running → exited, detached, reconnecting, connection lost, runtime offline/revoked). Background activity marks the inactive tab; selecting it clears the marker and never steals focus.
- Sessions survive disconnects; the canvas is the client, the PTY is the truth.

## Do's and Don'ts

### Do:
- **Do** theme through the `--ds-*`/`--bs-*` tokens in `app.css` — new components inherit light/dark for free.
- **Do** render small/secondary actions as `.icon-btn` (or `.ghost-btn` for key labels) with `aria-label` + `title`.
- **Do** express state with a low-tint badge plus text/icon, and keep every component's loading/empty/error/disabled state real.
- **Do** keep 44×44px effective targets and visible inset focus rings on chrome-less controls.
- **Do** hide unimplemented sidebar sections entirely — never ship disabled placeholders.
- **Do** use SweetAlert2 helpers for every dialog and Tabler patterns for every list state.

### Don't:
- **Don't** repaint primary actions white-on-teal, hand-roll button colors, or paint Tabler's default blue over the brand teal.
- **Don't** use the brand gradient on page backgrounds, cards, or anywhere near terminal output.
- **Don't** ship decorative charts or cards that don't help development work.
- **Don't** use native `window.alert`/`confirm`/`prompt` or load a second Bootstrap copy alongside Tabler's.
- **Don't** make essential behavior hover-only, color-only, or desktop-only.
- **Don't** log or display credentials, tokens, terminal input/output, or file contents.