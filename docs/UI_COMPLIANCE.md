# UI compliance status

Tracks where the implemented UI deviates from the authoritative design docs
(`docs/WEB_UI_DESIGN.md`, `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`,
`docs/BRAND_ASSETS.md`, `docs/BOOTSTRAP_COLORS.md`). Every entry names the
product intent it preserves and when to close it. Nothing here waives a
security or API-first rule.

## Applied (2026-09-13)

- Brand: gradient mark + theme-adaptive wordmark in navigation and login
  (BRAND_ASSETS.md); favicon links on every page; `/assets/` served.
- Color system: `web/static/css/app.css` carries the full `--ds-*`/`--bs-*`
  token sets; dark workstation theme is the default, light variant available;
  account-level theme preference (`users.theme`, migration 0007) with a
  navbar toggle.
- Top navbar restructured: sidebar toggle, brand, theme toggle, user menu
  (Account / Log out). Per WEB_UI_DESIGN.md the empty slots below are
  intentionally absent until their backends exist.
- Terminal: session-tab activity marker (background output, cleared on
  select); `runtime offline` connection state surfaced after reconnect
  attempts are exhausted; `runtime revoked` label defined.
- File manager: properties view (path/type/size/mtime/mode); multi-select
  Download, Copy-to, Move-to alongside bulk Delete; CodeMirror theme follows
  the app theme (material-darker in dark mode).

## Deviations (open, deliberate)

| Deviation | Doc | Intent preserved | Close when |
|---|---|---|---|
| No global search, notifications, help in the navbar | WEB_UI_DESIGN.md Navigation | No backend features yet (Activity is Slice 11); dead buttons would violate the state spec | Slices implementing those backends |
| Sidebar Files/Services/Databases links stay disabled | WEB_UI_DESIGN.md Navigation | Tools are project-scoped; a global link needs a project picker that is not specced | A project-context picker slice |
| File tree pane (`Tree \| File List`) not built; breadcrumbs + list only | VISUAL spec File Manager | Navigation works via breadcrumbs + search; agreed with Brian 2026-09-13 | If real tree navigation is needed |
| Editor states `external modification` and `deleted` not detected | VISUAL spec Editor states | Save failures surface server errors near the editor; conflict detection needs mtime/etag plumbing | First complaint of lost edits, or a concurrent-editing slice |
| Multi-select Download staggers anchor clicks (~300 ms apart) | VISUAL spec File list | Browsers throttle simultaneous programmatic downloads | Zip/manifest export slice |
| CodeMirror uses `material-darker` in dark mode rather than a fully matched brand theme | VISUAL spec / BOOTSTRAP_COLORS.md | Readable editor in both themes without theming CodeMirror token-by-token | A dedicated editor theme pass |
