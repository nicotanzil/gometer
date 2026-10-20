# Dashboard visual system

The visual source is the supplied SmartCare Clinical AI HTML. Its clinical copy,
marketing sections, routes, WebGL shader, and menu scripts are not application
requirements. GoMeter keeps its screens, content, configuration model, validation,
journey coordinates, API routes, theme preferences, and execution behavior.

## Extracted source language

| Area | Source values | Application mapping |
| --- | --- | --- |
| Headings | Bricolage Grotesque, 200–400, −0.035em tracking, tight line height | 300-weight page headings; 400-weight panel headings; scaled for a developer workbench |
| UI and prose | Hanken Grotesk, 300–600, 1.55 line height | 15px body; 13–14px forms and controls; light supporting prose |
| Technical data | IBM Plex Mono, 400–500, tabular figures | URLs, JSON, request methods, metrics, chart units, small labels |
| Background | `#070a13`, `#0c1121`, `#121933` | Page, editable fields, raised selections |
| Panels | `#05070d` | Builder, results, saved scenarios, Guide, JMX, journey nodes |
| Text | `#e9ecf5`, `#b8bfd5`, `#848ba5` | Primary, secondary, and supporting text |
| Signal | `#8db5ff`; primary button `#2b56c9` | Focus, selection, actions, charts, data dependencies |
| Status | Alert `#ff6b7d`, warning `#f0b64e`, success `#4fcf9f` | Errors, insecure HTTPS and migration warnings, passed checks |
| Borders | Blue-white at 11%, 14%, 22%, and 50% opacity | Subtle dividers, cards, inputs, selected controls |
| Corners | Panels 16px, inner surfaces 10–12px, actions 999px | Cards, inputs, pill buttons and status badges |
| Spacing | 4/8/12/16/20/24px rhythm; 20px card gaps; fluid 20–48px gutters; section spacing 88–160px | Component rhythm retained; app sections use 24–36px spacing to suit a workbench |
| Shadows | `0 40px 90px -50px #000`; inset button highlight and blue bloom | Quiet panel depth and focused primary actions |
| Motion | `cubic-bezier(.6,0,.2,1)`, 300–400ms interaction transitions | Hover, focus, and selection; no decorative animation during tests |

The reference's spacious, asymmetric grids become the application's existing
editor/preview, diagram/inspector, and chart grids. Thin dividers and typography
establish hierarchy. The journey editor retains its real nodes, branch paths,
loops, pan, zoom, full screen, and mobile Canvas/Inspector views.

The source centers a 1240px content container, pairs asymmetric two-column
sections with two-column cards and four-column statistics, then stacks the
columns at narrower widths. The dashboard keeps its sidebar and uses a 1440px
main container so the diagram and inspector have room; reports use 1240px.

The original animation vocabulary also includes a 900ms menu curtain, 1s menu
entries staggered by 70ms, a 140s ring rotation, a 22s HUD tilt, 5s fading ticks,
2s pulse rings, animated chart dashes, and an offscreen-paused WebGL glow with a
static fallback. These are decorative presentation examples. GoMeter adopts the
interaction easing, button illumination, and static panel glow; continuous
decoration is omitted to avoid adding load while measuring a system. The source's
reduced-motion behavior is retained.

The source is dark only. GoMeter's Dark theme uses its exact palette; Light uses
an inverse navy/blue palette with the same hierarchy, typography, and components.
System still follows the operating system. Semantic status colors remain distinct
from the blue action accent.

## Implementation

- `internal/dashboard/assets/theme.css` owns the visual tokens and component
  presentation. The existing layout CSS and JavaScript retain behavior.
- `internal/design/assets/fonts.css` defines the reference fonts. All fonts and
  their SIL Open Font Licenses are bundled locally; no CDN is required at runtime.
  Latin and Latin Extended subsets have system fallbacks for other scripts.
- `internal/design/assets/report.css` applies the language to exported reports.
  `internal/design` embeds the same fonts as data URLs in standalone report HTML.
- The dashboard's static allowlist adds the font stylesheet and four font files.
  Report downloads permit embedded font data while retaining their restrictive CSP.
- Existing focus rings, accessible labels, error messages, theme persistence, and
  mobile controls remain. Reduced motion disables transitions. Exported reports
  include a print palette.

Historical HTML prototypes under `docs/` remain design records. The live app and
exported reports are the implementation targets.
