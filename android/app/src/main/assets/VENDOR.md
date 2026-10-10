# Vendored terminal assets

| File | Source | Version |
|---|---|---|
| `xterm.js`, `xterm.css` | npm `@xterm/xterm` (`lib/xterm.js`, `css/xterm.css`) | 6.0.0 |
| `addon-fit.js` | npm `@xterm/addon-fit` (`lib/addon-fit.js`) | 0.11.0 |
| `addon-web-links.js` | npm `@xterm/addon-web-links` (`lib/addon-web-links.js`) | 0.12.0 |
| `addon-unicode11.js` | npm `@xterm/addon-unicode11` (`lib/addon-unicode11.js`) | 0.9.0 |
| `vendor/dompurify/purify.min.js` | npm `dompurify` — the exact file the web client vendors (`web/static/vendor/dompurify/`), Apache-2.0 / MPL-2.0 dual license | 3.1.6 |
| `textmate/grammars/*.json` (20 languages) | npm `tm-grammars` — `grammars/<name>.json`, unmodified | 1.32.22 |
| `textmate/darcula.json` | sora-editor sample assets (`app/src/main/assets/textmate/darcula.json`) — unmodified | sora-editor main, 2026-10-10 |
| `textmate/languages.json` | authored in-repo, referencing the vendored files above (no `languageConfiguration` entries — symbol-pair autocomplete off) | — |

### Licenses

- **sora-editor** dependency (`io.github.rosemoe:editor`, `language-textmate`, BOM 0.24.6):
  **LGPL-2.1-or-later** — kept as unmodified Maven AARs (standard relink path);
  no source vendoring. `VENDORED? no` — see also DESIGN_ANDROID_PARITY.md §1.
- **tm-grammars** (VSCode TextMate grammar JSONs): **MIT** — copies of the
  package's `LICENSE` and `NOTICE` ship in `textmate/grammars/`. Subset:
  kotlin, java, json, yaml, go, typescript, javascript, css, html, markdown,
  shellscript, docker, toml, ini, c, cpp, csharp, php, rust, sql.
- **darcula theme** JSON: a data file from the sora-editor repository
  (LGPL-2.1-or-later), unmodified.
- Grammars run through sora's own regex engine (no `oniguruma-native`
  module added); grammar behaviors that depend on oniguruma-specific regex
  may render imperfectly — acceptable for the phone editor, noted in
  language-textmate's README caveat (DESIGN_ANDROID_PARITY.md §9).

SHA-256 of each file is in `xterm.sha256`; `make xterm-check` verifies it and
`make desktop-assets` runs it first. To bump: change `XTERM_VERSION` / `FIT_VERSION` /
`WEBLINKS_VERSION` / `UNICODE11_VERSION` in the Makefile, run `make xterm-update`, review the diff, test both clients
(`terminal.html` touch selection depends on the `.xterm-screen` class).
