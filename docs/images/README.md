# App media capture notes

[Documentation](../README.md) · [Project overview](../../README.md)

These are real captures of the embedded app from the development checkout on
20 October 2026. They show the GoMeter Open Scope logo, current dark theme, locally bundled
fonts, and existing app controls. The checkout includes pending feature work; these captures
do not represent a tagged release.

All executed workload traffic targets a separate instance of the bundled demo
using an isolated temporary workspace. The recording and result screenshot show
an observed run: 160 HTTP attempts, no request errors, no missed arrivals, and
passed checks. Measurements illustrate this local demo and are not benchmarks.
No private targets, user data, or workload credentials appear in the assets.

| Asset | State | Capture |
| --- | --- | --- |
| [walkthrough.gif](walkthrough.gif) | Configured builder → Validate → live run → completed checks and charts | 1440 × 960; 16.73 seconds; looping GIF, about 285 KiB |
| [test-builder.jpg](test-builder.jpg) | Validated Local API baseline and planned 160 arrivals | Full page, 1440 × 1676 |
| [run-results.jpg](run-results.jpg) | Completed baseline, acceptance checks, and four workload charts | Document crop from the top, 1440 × 1363; later diagnostics omitted |
| [user-guide.jpg](user-guide.jpg) | First-test instructions and practice summary | Viewport, 1440 × 960 |
| [journey-canvas.png](journey-canvas.png) | Authenticated checkout starter, selected Login, and extracted-data links | Full page, 1440 × 1372 |
| [journey-flow.png](journey-flow.png) | Branches and loops starter, selected Browse twice, and scoped counter controls | App full-screen canvas at 1600 × 1200; Fit at 64% |
| [jmx-import.jpg](jmx-import.jpg) | Bundled HTTP sample, component review, runtime policies, and property defaults | Full page, 1440 × 1572 |

The normal viewport is 1440 × 960 with device scale 1. The branch/loop image uses
the app's Full screen control at 1600 × 1200 to make the complete flow readable.
JPEG and PNG screenshots retain browser pixels; no metrics or labels are replaced.
The GIF uses a shared 256-color palette and merges unchanged frames while retaining
the capture timing. No animation is added to the app.

## Reproduce or refresh

1. Build the current source with `make build`, or build both commands to a
   temporary directory if existing binaries must remain untouched.
2. Start the bundled demo in its default configuration: 50ms work and eight
   concurrent work slots. Use a separate loopback port if port 8787 is occupied.
3. Start the dashboard with a dedicated workspace, for example:

   ```sh
   ./bin/gometer ui -listen 127.0.0.1:8792 -data /tmp/gometer-media-workspace
   ```

4. Set **Theme → Dark**, hide the welcome tip, and load **Local smoke test**.
   Set the target to the separate demo's `/work` endpoint. Configure:

   | Setting | Value |
   | --- | --- |
   | Name | Local API baseline |
   | Rate | 20 requests/s |
   | Duration | 8s |
   | Concurrency cap | 16 |
   | Timeout | 1s |
   | Request p95 | 200ms |
   | Maximum errors / missed arrivals | 0% / 0% |
   | Minimum started HTTP rate | 19 requests/s |
   | Diagnostics | Enabled |

   Keep constant arrival spacing, normal certificate verification, and no workers
   or target-server telemetry. Close optional settings before the builder capture.
5. Record the normal viewport, starting with the configured builder. Press
   **Validate**, pause to show its result, then **Start run**. Wait for the actual
   final state, show the verdict and checks, then scroll to the four main charts.
   Capture at about four frames per second and preserve the observed timing when
   encoding the GIF. Do not replace failed results with fabricated pass values.
6. Capture the full builder and the completed result from the top through the
   four main workload charts. Open **Guide** and capture the first-test overview.
7. Load **Authenticated checkout journeys**, select **Login**, enable **Data flow**,
   and press **Fit**. Capture the page. Load **Branches and loops**, select
   **Browse twice**, use **Full screen** at 1600 × 1200, and press **Fit** again.
   These are authoring captures; do not start their workloads.
8. Choose **Import JMeter JMX → Review sample plan** and capture the full review
   before accepting runtime policies. Its defaults target the local demo on port
   8787; importing and reviewing send no workload traffic.
9. Inspect every image and the GIF for legibility, clipping, errors, and sensitive
   content. Update dimensions, date, captions, and README links if needed. Stop
   the temporary dashboard and demo after capture.

Keep these assets together under `docs/images/` so GitHub and offline checkouts
resolve them. Refresh the full set when navigation or the visual system changes.
