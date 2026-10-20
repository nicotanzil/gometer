# GoMeter brand assets

**Open Scope** is GoMeter's logo: an open instrument frame around a stepped
measurement line. The open edge suggests visibility; the steps suggest measured
load. The mark works in one color and is separate from the wordmark.

[Download the logo and icon bundle](gometer-logo-assets.zip) · [Visual preview](logo-preview.html)

## Assets

All runtime assets live in `internal/design/assets/` and are bundled in the binary.
The SVG wordmarks use outlined Bricolage Grotesque 600 lettering. They have no font
or network dependency. The font's SIL Open Font License remains alongside them.

| Asset | Use |
| --- | --- |
| [gometer-mark.svg](../../internal/design/assets/gometer-mark.svg) | Standalone mark; `currentColor` when inline or used as a CSS mask |
| [gometer-logo-dark.svg](../../internal/design/assets/gometer-logo-dark.svg) | Blue mark and near-white wordmark for dark backgrounds |
| [gometer-logo-light.svg](../../internal/design/assets/gometer-logo-light.svg) | Deep-blue mark and navy wordmark for light backgrounds |
| [gometer-logo-mono.svg](../../internal/design/assets/gometer-logo-mono.svg) | Single-color wordmark and mark |
| [gometer-app-icon.svg](../../internal/design/assets/gometer-app-icon.svg) | Navy app tile with blue mark |
| [favicon.svg](../../internal/design/assets/favicon.svg) | Browser favicon with adjusted small-size geometry |
| [favicon.ico](../../internal/design/assets/favicon.ico) | Browser fallback containing 16, 24, 32, 48, and 64px images |
| [gometer-icon-192.png](../../internal/design/assets/gometer-icon-192.png) | 192px app icon |
| [gometer-icon-512.png](../../internal/design/assets/gometer-icon-512.png) | 512px app icon |
| [gometer-icon-1024.png](../../internal/design/assets/gometer-icon-1024.png) | 1024px app icon |
| [apple-touch-icon.png](../../internal/design/assets/apple-touch-icon.png) | Opaque 180px touch icon; the operating system applies corner rounding |

## Color and sizing

- Dark: signal blue `#8db5ff` and text `#e9ecf5` on navy `#070a13`.
- Light: deep blue `#2b56c9` and text `#11192e` on white.
- Keep at least one frame-bar width of clear space around the mark.
- Use the supplied favicon artwork at 16 and 24px. At larger sizes, scale the SVG.
- Keep the frame opening, square corners, and stepped line. Use solid fills.
- Keep the mark's proportions; scale the complete shape uniformly.

[Visual preview](logo-preview.html) shows both themes, monochrome, actual small
sizes, and the mark in a compact developer-tool sidebar.
