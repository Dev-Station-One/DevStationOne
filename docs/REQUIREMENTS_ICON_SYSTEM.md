# Requirements: Icon System (FontAwesome)

Status: Implemented (2026-09-13). Design: `docs/DESIGN_ICON_SYSTEM.md`. FA Free 7.3.1
vendored (solid only). Conventions recorded in `docs/WEB_UI_DESIGN.md` (§Icons) and
`docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md` (§Icon Actions).

## Goal

Replace text-only buttons with icon buttons across the DevStationOne web UI, and add
iconography to navigation and page chrome. The UI currently has no icon system: all
buttons are text, with a single unicode glyph (`◐`) on the theme toggle.

## Decisions (from discovery session)

| Question | Decision |
|---|---|
| Icon library | **FontAwesome** (user's explicit choice over Tabler Icons) |
| Delivery | **Vendored locally** into `web/static/vendor/fontawesome/` — consistent with xterm, jquery, codemirror, tabler; no CDN, no CSP exceptions, offline installs work |
| Button treatment | **Borderless icon-only for small actions** — no outline button chrome, no borders, just the icon (see requirement 2). Text is retained on primary CTAs ("Create project") — those stay text buttons |
| Scope | **All pages, one pass**: `index.html`, `login.html`, `projects.html`, `project.html`, `runtimes.html`, `account.html`, plus buttons built dynamically in `js/app.js` and `js/dialogs.js` |
| Coverage | **Buttons + nav + chrome**: also icon the sidebar navigation, section headings, and empty states |

## Functional requirements

1. **Vendor FontAwesome.** Self-hosted webfont build (woff2 + CSS) under
   `web/static/vendor/fontawesome/`. No external requests at runtime. License file
   (FA Free: icons CC BY 4.0, code MIT/OFL as applicable) is vendored alongside.
2. **Borderless icon-only buttons.** Small/secondary action buttons render the icon
   alone — no outline button styling, no border, no box. This replaces the current
   `btn btn-outline-*` pattern on those actions. Feedback comes from cursor and a
   subtle hover state (background tint or color shift), never a border. Every
   icon-only action MUST have:
   - an `aria-label` (accessibility, non-negotiable), and
   - a tooltip (Bootstrap tooltip via Tabler, or `title` as fallback) so the action
     remains discoverable.
3. **Primary CTAs unchanged in kind.** Standalone primary buttons keep their text
   labels. Optionally prepend an icon, but text may not be removed from them.
4. **Navigation icons.** Sidebar navigation items (Projects, Runtimes, Account, and
   any others present) get a leading icon.
5. **Chrome icons.** Section headings and empty states on all pages get appropriate
   icons. The `◐` unicode theme-toggle glyph is replaced with a real sun/moon icon
   pair.
6. **Dynamic content.** Buttons constructed in JS (`js/app.js`, `js/dialogs.js` —
   e.g. table row Delete buttons) use the same icon conventions.
7. **No behavior changes.** This is a presentation-only change. Existing handlers,
   IDs, data attributes, and flows must not change.

## Known icon inventory (from current markup)

| Action / location | Icon intent |
|---|---|
| Sidebar fold toggle ("Menu") | hamburger |
| Theme toggle (`◐`) | sun / moon |
| Account dropdown toggle | user circle |
| Log out | right-from-bracket |
| Create project (CTA) | keep text; optional plus icon |
| Project row Delete | trash |
| Workspace page actions (terminal toolbar, tabs, file actions) | mapped at implementation; full inventory taken from `project.html` at that time |

Exact icon choices are finalized during implementation against the FontAwesome Free
set; this table fixes intent, not glyph names.

## Non-functional requirements

- All icon assets load from the same origin; zero third-party runtime requests.
- Works in both light and dark themes (icons inherit `currentColor`).
- Responsive: icon-only buttons must not widen mobile layouts; touch targets keep
  adequate size.
- Page weight impact acceptable for self-hosted use (woff2 subset or full FA Free
  webfont — implementer's choice, documented).

## Documented deviation

`CLAUDE.md` names Tabler as the UI foundation and Tabler's design language assumes
Tabler Icons. FontAwesome's visual weight differs from Tabler Icons' 1.5px outline
style. Per the project rule, this deviation is documented here and the product
intent (user's explicit choice) is preserved. If visual mismatch becomes a problem,
reverting to Tabler Icons is a contained change because all icon usage routes
through the same markup convention.

## Acceptance criteria

- [ ] FontAwesome vendored locally; app renders with no external network requests.
- [ ] Every icon-only action is borderless (no `btn-outline-*` chrome) with
      `aria-label` + tooltip.
- [ ] All six pages plus JS-constructed buttons use the icon conventions.
- [ ] Sidebar nav, headings, empty states, theme toggle iconed.
- [ ] No behavioral regressions; existing tests and builds pass.
- [ ] `docs/WEB_UI_DESIGN.md` and `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
      updated to reference the icon conventions (part of the same slice).

## Open questions

- None blocking. FA Free major version (6 vs 7) and full-webfont vs subset are
  implementation choices to be documented in the slice's commit.

## Next step

Proceed to implementation planning (`/sc:design` or direct slice execution per
`docs/IMPLEMENTATION_PLAN.md`).
