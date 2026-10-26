# App media capture notes

[Documentation](../README.md) · [Project overview](../../README.md)

These are real captures of the embedded dashboard from the development checkout.
The result image and worker catalog capture are from 23 October 2026; the other captures are from
21 October 2026. They show the approved Variant A workspace, Open Scope logo,
compact system typography, neutral surfaces and expanded/collapsed navigation.
The captures record local fixture behavior at their capture dates. Their assets
are included in v0.9.0; they do not establish production or company SSO acceptance.
The shared Tests capture is from 25 October 2026 and uses IBM Plex typography.

The walkthrough uses a separate bundled-demo process with a private temporary
workspace: 160 completed HTTP attempts, no request errors or missed arrivals,
and passing checks. The result image uses the counted local fixture in
`internal/dashboard/test/results-browser.cjs`: 60 completed HTTP attempts over
three seconds, no errors or missed arrivals, and six passing checks. It shows
the outcome and latency summaries with expandable checks and interactive charts.
The worker catalog capture uses two disposable loopback worker processes: one has a
dated successful connection observation and the other has not been checked.
Registration and connection checks sent no workload; the verifier later completed
20 independently counted requests across both workers.
These are illustrative local measurements, not performance benchmarks.
No private hosts, user data or supplied workload
credentials appear in the assets.

| Asset | State | Capture |
| --- | --- | --- |
| [walkthrough.gif](walkthrough.gif) | Validated builder → real run → saved verdict and charts | 1440 × 960; 11.38 seconds; looping GIF, 376 KiB |
| [test-builder.jpg](test-builder.jpg) | Local API baseline, planned demand and saved scenario | Full page, 1440 × 1515 |
| [team-tests.png](team-tests.png) | Shared authoring, managed-reference selector and private drafts with a disposable signed-in identity | Full page, 1440 × 2336 |
| [tests-library.png](tests-library.png) | Personal-mode Tests foundation: real saved definitions, revisions and independent copies | Viewport, 1440 × 1000 |
| [worker-catalog.jpg](worker-catalog.jpg) | Two named local workers, registration form, connection status and setup help | Viewport, 1440 × 1100 |
| [run-results.jpg](run-results.jpg) | Completed counted baseline, outcome/latency summaries and interactive charts | Viewport, 1440 × 1360 |
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

To refresh only the result image, run `internal/dashboard/test/results-browser.cjs`
with `GOMETER_RESULTS_SCREENSHOT` set to the absolute output image path. Set
`GOMETER_BINARY` to a rebuilt executable, plus `PLAYWRIGHT_MODULE` and
`CHROME_PATH` when needed. The verifier creates temporary fixture servers and a
private workspace, verifies the failed and passing runs, captures the passing
view, and stops its processes. It never uses the user's saved test history.

To reproduce the worker catalog image, run
`internal/dashboard/test/workers-browser.cjs` with `GOMETER_WORKERS_SCREENSHOT`
set to an absolute `.jpg` or `.png` path. It uses two private local worker
processes, a temporary dashboard workspace, and an independently counted target.
The image is captured before workload execution. The same verifier checks
selection, restart, shared browser access, immutable run identities, old bundles,
and that an unavailable worker never causes a local fallback. Use the same
`GOMETER_BINARY`, `PLAYWRIGHT_MODULE`, and `CHROME_PATH` settings as above.

The Tests library image was captured on 23 October 2026 by
`internal/dashboard/test/definitions-browser.cjs` with
`GOMETER_TESTS_SCREENSHOT` set to the absolute output path. It shows actual
personal-mode records after two-session conflict and copy checks. This fixture
sends no workload and contains no real team identities or credentials. The image
does not demonstrate PostgreSQL, OIDC, managed secrets or the queue. The separate
team capture shows authoring; team queued execution remains pending.
Older captures retain the navigation present on their capture dates.

To reproduce the shared Tests image, run `make test-workspace` with
`GOMETER_TEAM_BROWSER=1`, `GOMETER_TEAM_SCREENSHOT` set to an absolute `.png`
path, and the Playwright/Chrome paths described in the
[team verification guide](../team-workspace.md#verification).
The disposable PostgreSQL/OIDC/browser fixture checks authoring and private drafts
before capture. It uses demonstration identities and public workload fields;
no company LDAP, live workload secrets or team executor is involved.
