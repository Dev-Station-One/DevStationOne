# Design: Icon System (FontAwesome)

Implements `docs/REQUIREMENTS_ICON_SYSTEM.md`. Design only — no code in this document.

## 1. Vendoring

FontAwesome **Free 7.x** (exact patch version pinned at implementation, recorded in
the slice commit). Download the "For Web" package and vendor:

```text
web/static/vendor/fontawesome/
├── css/fontawesome.min.css      # core (families, sizing, borders…)
├── css/solid.min.css            # the only style the UI uses
├── webfonts/fa-solid-900.woff2  # + .ttf fallback
├── LICENSE.txt                  # code license (MIT)
├── LICENSES.txt                 # CC BY 4.0 attribution for icons + OFL for fonts
└── ATTRIB.md                    # one-paragraph attribution note (CC BY 4.0 requirement)
```

Solid style only — no regular, no brands, no kit JS, no SVG framework. Reference in
the `<head>` of **all six pages**, before `app.css`:

```html
<link rel="stylesheet" href="/vendor/fontawesome/css/fontawesome.min.css">
<link rel="stylesheet" href="/vendor/fontawesome/css/solid.min.css">
```

Usage pattern everywhere: `<i class="fa-solid fa-<name>" aria-hidden="true"></i>`.
Icons are decorative; the wrapping button carries the accessible name.

Reference (context7, fontawesome docs — host-yourself/webfonts): webfont method =
css folder + webfonts folder, no JS. Relative font paths inside the vendored CSS
resolve to `../webfonts/` — directory layout above preserves that.

## 2. Core component: `.icon-btn` (borderless icon action)

New utility in `css/app.css`. Replaces the `btn btn-outline-*` pattern on all
small/secondary actions. **No border, no outline chrome** — feedback via cursor and
a subtle hover tint only.

```css
.icon-btn {
  border: none;
  background: transparent;
  color: var(--ds-text-muted);
  border-radius: var(--tblr-border-radius);
  padding: 0 .5rem;
  min-width: 44px;        /* VISUAL spec touch-target rule, desktop too */
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.icon-btn:hover, .icon-btn:focus-visible {
  background: var(--tblr-bg-surface-secondary);
  color: var(--ds-text);
}
.icon-btn:focus-visible {
  outline: 2px solid var(--ds-secondary); outline-offset: -2px;
}
.icon-btn:disabled { opacity: .45; cursor: not-allowed; background: transparent; }
.icon-btn.text-danger      { color: var(--ds-danger); }   /* destructive actions */
.icon-btn.text-danger:hover { color: var(--ds-danger); background: var(--tblr-bg-surface-secondary); }
```

Contract for every `.icon-btn`:

- `aria-label` naming the action (mandatory — the glyph is `aria-hidden`).
- `title` attribute with the same wording (tooltip; no Tooltip JS init needed —
  this matches existing `title` usage across the markup).
- Keep existing `id`/class hooks (`delete-btn`, `data-*`, etc.) so JS handlers
  don't change.

Theme-toggle sun/moon is CSS-driven so `app.js` needs no icon logic:

```css
#theme-toggle .fa-sun  { display: none; }
#theme-toggle .fa-moon { display: inline-block; }
[data-bs-theme="light"] #theme-toggle .fa-sun  { display: inline-block; }
[data-bs-theme="light"] #theme-toggle .fa-moon { display: none; }
```

## 3. Markup conventions

| Context | Pattern |
|---|---|
| Small/secondary action | `<button type="button" class="icon-btn" aria-label="Delete" title="Delete"><i class="fa-solid fa-trash" aria-hidden="true"></i></button>` |
| Destructive variant | same + `text-danger` class |
| Dropdown trigger (Account) | `.icon-btn` with `fa-user` + `fa-caret-down` (caret signals the dropdown; keep `dropdown-toggle` class) |
| Sidebar nav item | `<a class="nav-link"><i class="fa-solid fa-server"></i> Runtimes</a>` — icon then label; CSS aligns `width: 1.25em` |
| App tabs (project workspace) | icon + existing label inside `.nav-link` |
| Primary CTAs | **unchanged** — `btn btn-primary` keeps its text label (optionally icon-prepended; default: not) |
| Terminal key-send buttons | **stay text, lose the border**: ghost style `.ghost-btn` (same box model as `.icon-btn`, keeps its text label, no icon). Icons cannot convey "Ctrl+C"/"Tab"/"Esc" — converting these to glyphs would destroy their meaning |
| Breadcrumb / link buttons | unchanged (`btn-link p-0` is already borderless text) |
| Empty states | Tabler `.empty` block gains a leading `fa-2x`-ish icon above `.empty-title` |
| Page h2 headings | icon prepended (e.g. `fa-folder` Projects, `fa-server` Runtimes, `fa-user` Account) |
| Session-tab close | existing borderless `.close-btn` swaps `&times;` for `fa-xmark` |

Ghost button (key-send only):

```css
.ghost-btn {
  border: none; background: transparent; color: var(--ds-text-muted);
  padding: 0 .75rem; min-height: 44px; cursor: pointer;
}
.ghost-btn:hover, .ghost-btn:focus-visible { color: var(--ds-text); background: var(--tblr-bg-surface-secondary); }
```

## 4. Icon map (all pages, complete inventory)

Names are FA Free solid; **verify each against the vendored copy at implementation**
(alias spellings shift between majors).

### Global (all pages: index, projects, runtimes, account, project)

| Element | Today | Becomes |
|---|---|---|
| Sidebar fold toggle ("Menu") | `btn-outline-secondary` text | `.icon-btn` `fa-bars`, title "Fold sidebar" |
| Theme toggle (`#theme-toggle`) | `btn-outline-secondary` + `◐` | `.icon-btn` `fa-sun`/`fa-moon` (CSS swap per §2) |
| Account dropdown trigger | `btn-outline-secondary dropdown-toggle` text | `.icon-btn dropdown-toggle` `fa-user` + `fa-caret-down` |
| Log out (dropdown item) | text | icon-prefixed text item `fa-right-from-bracket` (dropdown items keep text) |
| Account (dropdown item) | text | icon-prefixed text item `fa-user-gear` |
| Sidebar: Dashboard / Projects / Runtimes / Files / Databases / Services / Activity / Settings | text nav links | icon + text: `fa-gauge-high`, `fa-cubes`, `fa-server`, `fa-folder-tree`, `fa-database`, `fa-gears`, `fa-clock-rotate-left`, `fa-gear` |

### index.html

| Element | Becomes |
|---|---|
| Empty state | leading icon `fa-desktop` above title; CTA "Go to Projects" unchanged |

### projects.html

| Element | Becomes |
|---|---|
| h2 "Projects" | `fa-cubes` prepended |
| Create project | unchanged (`btn-primary` text) |
| Row Delete (in JS string) | `.icon-btn text-danger` `fa-trash`, aria-label "Delete project" |
| Projects empty state | leading icon `fa-cubes` |

### runtimes.html

| Element | Becomes |
|---|---|
| h2 "Runtimes" | `fa-server` prepended |
| Register | unchanged |
| Row Revoke (in JS string) | `.icon-btn text-danger` `fa-ban`, aria-label "Revoke runtime" |
| Runtimes empty state | leading icon `fa-server` |

### account.html

| Element | Becomes |
|---|---|
| h2 "Account" | `fa-user` prepended |
| Session Revoke (in JS string) | `.icon-btn text-danger` `fa-ban`, aria-label "Revoke session" |

### project.html

| Element | Becomes |
|---|---|
| Page header Edit | `.icon-btn` `fa-pen-to-square` |
| Page header Delete | `.icon-btn text-danger` `fa-trash` |
| App tabs Overview/Terminal/Files/Databases/Services | icon + text: `fa-circle-info`, `fa-terminal`, `fa-folder`, `fa-database`, `fa-gears` |
| "+ New" session | `.icon-btn` `fa-plus`, title "New terminal session" |
| Terminal key buttons Ctrl+C/Ctrl+D/Tab/Esc | `.ghost-btn` (text kept, border removed) |
| Force stop | `.icon-btn text-danger` `fa-circle-stop`, title kept |
| Services Refresh | `.icon-btn` `fa-rotate` |
| Service card Start/Stop/Restart/Enable/Disable/Logs | `.icon-btn` (disabled state preserved): `fa-play`, `fa-stop`, `fa-rotate`, `fa-toggle-on`, `fa-toggle-off`, `fa-file-lines` |
| Files New file / New folder / Upload | `.icon-btn` `fa-file-plus`, `fa-folder-plus`, `fa-upload` |
| Files search submit | `.icon-btn` `fa-magnifying-glass` |
| Selection bar Delete selected / Download / Copy to… / Move to… / Clear | `.icon-btn text-danger` `fa-trash`, `fa-download`, `fa-copy`, `fa-up-down-left-right`, `fa-xmark` |
| File row actions (JS `entryActions`) Open/Edit/Download/Rename/Copy/Move/Properties/Delete | `.icon-btn` `fa-folder-open`, `fa-pen`, `fa-download`, `fa-i-cursor`, `fa-copy`, `fa-up-down-left-right`, `fa-circle-info`, `.text-danger fa-trash` |
| File editor Preview/Download/Close | `.icon-btn` `fa-eye`, `fa-download`, `fa-xmark` (Save stays `btn-primary` text) |
| File properties Close | `.icon-btn` `fa-xmark` |
| Session-tab close (JS) | `.close-btn` content `fa-xmark` |
| Terminal / services / files empty states | leading icons `fa-terminal`, `fa-gears`, `fa-folder-open` |

### login.html

No actions to convert. Includes the stylesheet links for consistency only.

## 5. Files changed (implementation surface)

1. `web/static/vendor/fontawesome/**` — new (vendored FA Free 7.x solid).
2. `web/static/css/app.css` — `.icon-btn`, `.ghost-btn`, sun/moon swap, nav/tab
   icon alignment rule. Replace `#theme-toggle` min-size block (absorbed by
   `.icon-btn`).
3. Six `*.html` — head links, header cluster (menu/theme/account), sidebar nav
   icons, page h2 icons, project.html panels per §4, empty states.
4. `web/static/projects.html` inline JS — row Delete button string.
5. `web/static/runtimes.html` inline JS — Revoke button string.
6. `web/static/account.html` inline JS — session Revoke button string.
7. `web/static/project.html` inline JS — `entryActions`, service card buttons,
   session-tab close, editor buttons.
8. `docs/WEB_UI_DESIGN.md`, `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md` —
   icon-conventions section (part of the same slice).
9. `docs/REQUIREMENTS_ICON_SYSTEM.md` — status → implemented.

No JS behavior changes: handlers bind to `id`/`data-*`/class hooks that survive
(`.delete-btn`, `.revoke-btn`, `[data-send-key]`, `[data-action]`…). `app.js`
theme logic untouched (CSS does the swap). `dialogs.js` untouched.

## 6. Risks / decisions recorded

- **Terminal key buttons stay text.** An icon for "Ctrl+C" is undiscoverable;
  border removal alone satisfies the no-outline rule. (Flagged for the user —
  say the word if you want glyphs there anyway.)
- **Disabled icon buttons** (service Start/Stop/Enable/Disable) rely on opacity +
  `cursor: not-allowed`; verified against the VISUAL spec's disabled state.
- **Density:** file-row action clusters grow from 7 text buttons to 7 icons — net
  narrower rows, matching the VISUAL spec's dense-table intent.
- **Page weight:** one woff2 (~150 KB) + two CSS files, fetched once, cached.
  Acceptable for self-hosted use.
- **Tabler visual language:** FontAwesome solid is heavier than Tabler Icons'
  1.5 px outlines. Documented as an approved deviation in the requirements doc.

## 7. Acceptance (design-level, mirrors requirements doc)

- All six pages render with zero external network requests.
- No remaining `btn-outline-*` on small actions (grep-verifiable: zero
  `btn-outline` occurrences outside primary-CTA-adjacent legit uses; target: none).
- Every `.icon-btn` has both `aria-label` and `title`.
- Theme toggle swaps sun/moon in both directions without JS changes.
- Manual smoke: dashboard → projects (create/delete row) → project workspace
  (terminal tabs, files toolbar + row actions, services cards) → runtimes →
  account, in dark and light.
