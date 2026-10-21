# Dashboard visual system

The dashboard follows the approved **Variant A · Workspace** in
[the enterprise prototype](enterprise-prototype.html). The prototype is a design
record: its sample data, generated configurations, simulation and stub controls
are not application behavior. This direction supersedes the earlier SmartCare
style for the dashboard. Exported report styles remain separate.

## Visual rules

| Area | Dashboard treatment |
| --- | --- |
| Typography | Native system sans serif; 13px body, 12–13px labels/controls, 23px page headings; bundled IBM Plex Mono for URLs, JSON, units and measurements |
| Surfaces | Neutral page, plain panels and inputs; thin borders; no decorative gradients, glows or continuous animation |
| Corners | 4–6px for controls, panels, nodes and dialogs |
| Actions | One blue primary accent; secondary actions use neutral borders; success, warning and failure retain semantic colors |
| Spacing | 6/8/12/16/20/24px component rhythm; compact forms; aligned tables; 28px desktop gutters |
| Canvas | Dotted background, vertical execution flow, outline and inspector; selected nodes use a clear border |
| Copy | Short labels and headings; contextual Guide links for explanations; units, errors, consequences and measurement limits remain visible |

Light, Dark and System use the same hierarchy and controls. Theme preference is
browser-local. There are no CDN or framework dependencies.

## Navigation and editor space

The desktop sidebar is 196px expanded and 60px collapsed. Its keyboard-operable
toggle exposes `aria-expanded` and `aria-controls`. Destinations retain accessible
names; icon-only items show hover/focus tooltips, dismissible with Escape. The
collapsed state is saved under `gometer.sidebar.collapsed.v1`, outside scenario
configuration. Collapsing changes available width without replacing editor state.

At 900px and below, the top bar opens a navigation drawer. It traps focus while
open, makes the workspace inert, supports Escape and backdrop dismissal, and
restores focus on close. Choosing a destination closes it and focuses the new
heading. Resizing restores a usable layout without changing the desktop
preference. The theme picker remains in the top bar.

The existing canvas model and coordinates remain authoritative. Presentation
changes preserve branches/loops, child paths, dependencies, pauses, checks,
reordering, move-to-path, undo, unfinished drafts, pan/zoom/fit/full screen and
mobile Canvas/Inspector access. No prototype execution logic enters the app.

## Implementation and verification

- `internal/dashboard/assets/theme.css` owns dashboard tokens and presentation.
  Component styles retain their structural layout rules.
- `navigation.css` and `navigation.js` own the responsive shell, preference and
  navigation accessibility. Existing view handlers retain application behavior.
- Host/Origin checks, worker transport permissions and measurement contracts stay
  in their existing backend modules. Plaintext-control warnings remain visible.
- `make test-ui` checks JavaScript syntax and dependency-free models/controllers.
  `make test-browser` exercises the real app with a disposable workspace and
  counted local HTTP target, including navigation, drafts, save/reload, complex
  journeys, comparison, themes and responsive screens. See [contributing](../CONTRIBUTING.md).

Local browser checks are evidence of the exercised flows, not a security audit,
performance benchmark or production deployment.
