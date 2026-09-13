# DevStationOne Bootstrap 5.3 Color System

DevStationOne uses a dark developer-workstation visual language built around a teal → cyan gradient. Bootstrap 5.3 should be themed through CSS custom properties rather than scattered component-specific colors.

## Core palette

| Token | Hex | Use |
|---|---|---|
| Primary | `#00E5A8` | Primary actions, active states, brand teal |
| Secondary | `#00C2FF` | Secondary actions, links, cyan accent |
| Success | `#10B981` | Successful operations / online |
| Info | `#3B82F6` | Informational states |
| Warning | `#F59E0B` | Warnings / attention |
| Danger | `#EF4444` | Errors / destructive actions |
| Accent | `#855CF6` | Optional purple accent |
| Background | `#0B1220` | Application background |
| Surface | `#1E293B` | Cards, panels, navigation |
| Surface Elevated | `#243449` | Elevated panels / popovers |
| Border | `#64748B` | Borders and dividers, normally used with opacity |
| Text | `#FFFFFF` | Primary text |
| Muted Text | `#94A3B8` | Secondary text |

## Bootstrap 5.3 theme variables

```css
:root {
  --bs-primary: #00E5A8;
  --bs-primary-rgb: 0, 229, 168;
  --bs-secondary: #00C2FF;
  --bs-secondary-rgb: 0, 194, 255;
  --bs-success: #10B981;
  --bs-success-rgb: 16, 185, 129;
  --bs-info: #3B82F6;
  --bs-info-rgb: 59, 130, 246;
  --bs-warning: #F59E0B;
  --bs-warning-rgb: 245, 158, 11;
  --bs-danger: #EF4444;
  --bs-danger-rgb: 239, 68, 68;
  --bs-light: #F8FAFC;
  --bs-light-rgb: 248, 250, 252;
  --bs-dark: #0B1220;
  --bs-dark-rgb: 11, 18, 32;

  --bs-body-bg: #0B1220;
  --bs-body-color: #E2E8F0;
  --bs-emphasis-color: #FFFFFF;
  --bs-secondary-color: #94A3B8;
  --bs-tertiary-color: #64748B;
  --bs-border-color: #334155;
  --bs-link-color: #00C2FF;
  --bs-link-hover-color: #00E5A8;
}
```

## DevStationOne-specific variables

```css
:root {
  --ds-primary: #00E5A8;
  --ds-secondary: #00C2FF;
  --ds-success: #10B981;
  --ds-info: #3B82F6;
  --ds-warning: #F59E0B;
  --ds-danger: #EF4444;
  --ds-accent: #855CF6;

  --ds-bg: #0B1220;
  --ds-surface: #1E293B;
  --ds-surface-elevated: #243449;
  --ds-border: #64748B;
  --ds-text: #FFFFFF;
  --ds-text-muted: #94A3B8;

  --ds-brand-gradient: linear-gradient(90deg, #00E5A8 0%, #00CFEA 55%, #00C2FF 100%);
}
```

## Usage rules

- Use `--ds-primary` for primary actions and important active states.
- Use `--ds-secondary` for links, secondary actions, and cyan highlights.
- Use the brand gradient primarily for the logo and restrained brand accents.
- Do not use the gradient for large page backgrounds or behind terminal text.
- Use `--ds-bg` for the main application background.
- Use `--ds-surface` for persistent UI surfaces.
- Use muted text for secondary metadata, never for critical content.
- Status must never depend on color alone; pair status colors with text or icons.
- Terminal output should remain visually neutral and should not inherit brand colors.
- Maintain WCAG-aware contrast when using muted text or accent colors.

## Light theme

Bootstrap's light theme should preserve the same brand colors while switching surfaces and text to light equivalents. The logo mark remains teal/cyan and the wordmark should use the appropriate dark text treatment.

Suggested light tokens:

```css
[data-bs-theme="light"] {
  --bs-body-bg: #F8FAFC;
  --bs-body-color: #0F172A;
  --bs-emphasis-color: #0F172A;
  --bs-secondary-color: #475569;
  --bs-border-color: #CBD5E1;

  --ds-bg: #F8FAFC;
  --ds-surface: #FFFFFF;
  --ds-surface-elevated: #F1F5F9;
  --ds-border: #CBD5E1;
  --ds-text: #0F172A;
  --ds-text-muted: #475569;
}
```

## Button theming note

Bootstrap 5.3 compiles `.btn-primary` with literal colors and Tabler paints it with its own
literal blue, so `--bs-primary` alone never reaches buttons. `web/static/css/app.css` overrides
the `--bs-btn-*` variables **and** the painted `background-color`/`border-color`/`color`
properties (base, hover, focus-visible, active, disabled) to apply brand teal with dark text.
