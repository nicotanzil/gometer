# App media capture notes

[Documentation](../README.md) · [Project overview](../../README.md)

These are real captures of the embedded dashboard from the development checkout
on 21 October 2026. They show the approved Variant A workspace, Open Scope logo,
compact system typography, neutral surfaces and expanded/collapsed navigation.
The captures include local uncommitted work and do not represent a tagged release.

The walkthrough and result image use a separate bundled-demo process with a
private temporary workspace: 160 completed HTTP attempts, no request errors or
missed arrivals, and passing checks. These are illustrative local measurements,
not performance benchmarks. No private hosts, user data or supplied workload
credentials appear in the assets.

| Asset | State | Capture |
| --- | --- | --- |
| [walkthrough.gif](walkthrough.gif) | Validated builder → real run → saved verdict and charts | 1440 × 960; 11.38 seconds; looping GIF, 376 KiB |
| [test-builder.jpg](test-builder.jpg) | Local API baseline, planned demand and saved scenario | Full page, 1440 × 1515 |
| [run-results.jpg](run-results.jpg) | Completed baseline, checks and workload charts | Viewport, 1440 × 1360 |
| [user-guide.jpg](user-guide.jpg) | First-test instructions and practice settings | Viewport, 1440 × 960 |
| [journey-canvas.png](journey-canvas.png) | Checkout journey, Login inspector and data dependencies | Full page, 1440 × 1266 |
| [journey-flow.png](journey-flow.png) | Nested branches/loops and Browse twice inspector | App full-screen canvas, 1600 × 1200 |
| [jmx-import.jpg](jmx-import.jpg) | Bundled sample review, runtime policies and properties | Full page, 1440 × 1438 |

The reference captures use Light theme at device scale 1. Dark/System, the
collapsed icon rail, drawer navigation and layouts at 1440, 768, 390 and 320px
were also checked locally. Browser checks include real editor drafts, save/reload,
counted journeys, comparison, JMX review and the CSV dialog.

## Reproduce

1. Rebuild the embedded app and demo. Start each on a temporary loopback port,
   with a new dashboard data directory. Keep user workspaces separate.
2. Select Light theme and configure a single-request baseline against the demo's
   `/work`: 20 requests/s, 8s duration, concurrency 16, timeout 1s, diagnostics on,
   p95 limit 200ms, errors/missed arrivals 0%, minimum started rate 19 requests/s.
   Use a 30ms demo delay and its default capacity of eight.
3. Validate, save the scenario and capture the builder. Record the actual start,
   progress and final state. Check the result counts against the demo's metrics;
   retain observed timing when encoding. Never fabricate a passed result.
4. Capture the saved result and Guide. For authoring images, load the bundled
   checkout and flow starters, select Login or Browse twice, and press Fit.
   Use the real Full screen control for the flow image; no traffic is needed.
5. Open **Import JMeter JMX → Review sample plan** and capture review before
   accepting runtime policies. This sends no workload traffic.
6. Inspect every image for clipping, legibility and sensitive content. Update
   dimensions, captions and this date, then stop the capture processes.

The README and documentation index use these relative paths. Refresh the media
when navigation or the visual system changes.
