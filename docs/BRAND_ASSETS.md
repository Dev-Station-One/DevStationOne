# DevStationOne Brand Assets

## Approved identity

The approved identity is the **DS1 infinity/flow mark** with the **teal/cyan palette**.

Primary brand gradient: `#00E5A8 → #00CFEA → #00C2FF`

Approved palette (from `assets/brand-assets/README.md`):

- Primary: `#00E5A8`
- Secondary: `#00C2FF`
- Background: `#0B1220`
- Text / Muted: `#94A3B8`

## Application assets

Installed in `assets/logo/` (served at `/assets/logo/`):

- `icon-mark-dark.png` — DS1 mark on dark tile; top-navigation logo (used with the "DevStationOne" text link)
- `icon-mark-light.png` — DS1 mark on light background; light-context use
- `favicon-16.png` / `favicon-32.png` / `favicon-64.png` / `favicon-128.png` / `favicon-256.png` / `favicon-512.png` — favicon sizes
- `og-image.png` — social preview (cropped from the approved board's OG image)
- `devstationone-top-logo.png` — full stacked lockup (DS1 mark + wordmark + tagline, transparent); sidebar header and login hero

Also installed:

- `web/static/favicon.ico` — served at `/favicon.ico`
- `assets/logo/github-banner-readme-header.png` — GitHub README header banner

Reference board (source crops, not deployed): `assets/brand-assets/` with its own `README.md`.

## Usage

- Top navigation: the vertical sidebar header shows `devstationone-top-logo.png` at 68px height (40px below `lg`, where the sidebar becomes a horizontal top navbar). The folded sidebar scales it to the available width. `navbar-brand-autodark` is deliberately not applied — it inverts the image under dark themes and would destroy the brand gradient.
- Login page: `devstationone-top-logo.png` at 100px height above the sign-in card.
- Favicons: pages link `/favicon.ico`, `favicon-32.png`, `favicon-16.png`. Note `favicon-256.png`/`favicon-512.png` are really 90x89 / 145x127 — do not reference them with those sizes.
- Installable-app icons (ADR-055), rendered from the DS1 glyph of `devstationone-top-logo.png` on `#0B1220`: `pwa-192.png`, `pwa-512.png` (manifest `any`), `pwa-maskable-512.png` (manifest `maskable`, glyph inside the 80% safe zone), `apple-touch-180.png` (the Apple touch icon on every page).

## Known limitation

The brand-assets pack is raster crops from the approved visual board. Production branding should
recreate/export the DS1 mark and wordmark as clean vector artwork (SVG) from the approved design
rather than tracing the board screenshot — see `assets/brand-assets/README.md`.