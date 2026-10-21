# GoMeter development roadmap

Each milestone should leave a usable application. This file carries the plan
between sessions; update it when functionality or priorities change.

## 1. HTTP engine and CLI

- [x] Go module and a CLI with `run`, `validate`, and `version` commands.
- [x] Fixed-user and fixed-arrival-rate workloads, with bounded concurrency.
- [x] HTTP methods, headers, bodies, timeouts, and explicit success statuses.
- [x] Sequential rate phases for baseline, stress, and recovery tests.
- [x] Separate scheduled, started, completed, failed, and dropped demand.
- [x] Bounded latency histograms and per-phase results.
- [x] Threshold checks, JSON output, and a standalone HTML report.
- [x] Local demo target, correctness tests, race checks, and usage documentation.

## 2. Real application scenarios

- [x] Multi-request journeys with response assertions and token extraction.
- [x] Per-user cookies, authentication, variables, and parameterized data.
- [x] Weighted scenario mixes and configurable arrival distributions.
- [x] Scenario-level results and thresholds.

## 3. Interactive app

- [x] Browser dashboard to create, validate, run, and compare tests.
- [x] Live phase, throughput, latency, error, and missed-demand charts.
- [x] Saved runs, reusable scenarios, and import/export.
- [x] Sequential journey canvas with request/data editing, dependency links, undo,
  sequence preview, and final per-step measurements.

## 4. Scale and observability

- [x] Worker coordination and distributed traffic scheduling for rate-mode tests.
- [ ] Distributed users mode, dynamic balancing, and durable worker recovery.
- [x] Generator CPU, memory, connection, and scheduler diagnostics.
- [x] Detailed HTTP timing with explicit sample populations.
- [x] Target-server telemetry integration with selected Prometheus scalar series.
- [ ] Baseline comparisons and automated regression detection.

## 5. Prove the advantages

- [ ] Reproducible benchmark harness against JMeter and other relevant tools.
- [ ] Match workload, hardware, connection behavior, TLS, and reporting costs.
- [ ] Independently verify request counts and latency against reference targets.
- [ ] Publish measured resource efficiency, pacing accuracy, and limitations.

## Current scope

Milestones 1, 2, and 3 are complete. The current implementation includes opt-in
target-server telemetry, distributed rate-mode workers, generator diagnostics,
and HTTP timing in milestone 4. JSON scenarios
and Go's standard library keep this version reproducible without third-party
runtime dependencies.
Tests support single requests and weighted journeys with nested if/else, bounded
fixed-count/while loops, sequential HTTP requests,
isolated sessions, variables, data rows, extraction, and assertions. No claim
of superiority over JMeter is justified until milestone 5 is complete.

## Handoff for the next session

Read this file, README.md, docs/scenarios.md, docs/dashboard.md,
docs/diagnostics.md, docs/workers.md, and docs/telemetry.md first. Continue
the bundled JMeter parity program in docs/jmeter-parity.md. The next slice is guided JMX CSV attachment and mapping, then
richer HTTP workflows. Native local users-mode CSV datasets are implemented;
rate/fleet allocation remains separate. The journey canvas now edits
requests, nested branches and loops, checks, extractions, data, variables, and
per-journey criteria. Foreach, once-only, switch, shared fragments, and richer
failure actions remain in the execution-controls backlog.
Baseline regression work moves to the
final evidence stage of that program.
Distributed users mode, balancing, and durable worker recovery remain separate
extensions to the first worker version. Reuse the existing engine and validation.
Preserve the measurement contract:
journey arrivals and HTTP request attempts have separate counts and rates.

### Guided JMX import (12 October 2026)

- [x] Guided file selection, component compatibility review, literal property
  inputs, source paths, journey preview, and explicit save.
- [x] Limited JMeter 5.6.3 duration-based HTTP conversion with scoped headers,
  static variables, raw bodies, fixed loops, basic assertions, and explicit
  cookie behavior. Unsupported settings block executable output.
- [x] Required GoMeter runtime review, server-side reanalysis, bounded XML,
  original source retention, and persistent non-runnable migration drafts.
- [x] Malformed/unsupported fixtures, counted native HTTP execution, persistence,
  authorization and source-provenance checks. See docs/jmx-import.md.
- [ ] Broader JMX mappings and cross-engine conformance;
  CSV data, redirects, richer controllers and failure actions remain open.

### Finite users iterations (after v0.3.0)

- [x] Per-user outer request/journey attempts; duration, count, or first-limit stop.
- [x] Builder stop choices, preview, progress, CLI flag, JSON and HTML reports.
- [x] Finite JMX Thread Group loops with optional scheduler; no implicit time cap.
- [x] Schema 7 stopping contract, native bundle round trips and consistency checks.
- [x] Next increment: user ramps and startup delay.
- [x] Native local users-mode CSV data.
- [ ] Following increments: JMX CSV migration and richer HTTP workflows.

Validation: `make check build` passed, including vet, race tests, UI tests, and
both binaries. Independent local-target tests cover exact per-user counts,
failure consumption, isolated cookie retention, actual elapsed throughput,
duration drain, and cancellation. Native schema 7 imports preserve stopping
settings and reject mismatches. Browser checks passed 50/50 HTTP attempts,
an early duration stop at 6/100, Stop during pacing, invalid count blocking,
and a guided finite JMX import executing 5 journeys / 15 HTTP attempts.
Builder and results fit at 320px; browser warning/error logs were empty.
This verifies native conversion behavior, not cross-engine equivalence or
comparative performance. The isolated review is at localhost:52740, with
workspace data under `/private/tmp/gometer-iterations-review` and QA images in
ignored `work/iterations-qa/`.

### Test-wide failure actions (15 October 2026)

- [x] Users-mode graceful/immediate error stops with separate admission/work contexts.
- [x] Freeze load denominators during graceful drain; retain partial counts.
- [x] Cancel future ramp starts; immediate also cancels active HTTP and pauses.
- [x] Distinguish manual interruption; require a failed stopping check.
- [x] Builder, canvas guidance, live results, CLI and standalone HTML.
- [x] Schema 10 bundles with policy/reason/check/timeline validation.
- [x] JMX Stop Test/Stop Test Now mappings with explicit native drain differences.
- [x] Native local users-mode CSV datasets and sharing/end-of-file semantics.

Focused checks cover active-peer drain/cancellation, journey pauses, future user
starts, frozen progress, manual interruption during drain, disabled thresholds,
healthy completion, journey-level errors, JMX conversion and portable results.
`make check build` passed all race tests, vet, UI checks and both builds. Final
CLI summary and late failure during duration drain checks also passed with race
instrumentation. Browser fixtures observed a 0.3s admission window with 3s
of graceful drain (5 HTTP attempts), immediate cancellation (2 attempts, one
canceled), and manual Stop during graceful drain (interrupted with error flag
retained). Only 2/3 planned users started; future ramp starts were canceled.
Error thresholds were disabled, but both policies failed their mandatory check.
The guided JMX review showed the graceful sampler/journey difference at review
and save; the converted plan stopped after 1/2 journeys and one HTTP attempt.
The 320px controls fit without page overflow and browser warning/error logs
were empty. Review: localhost:53140, workspace `/private/tmp/gometer-test-stop-review`,
QA images in ignored `work/test-stop-qa/`. Checkpoint before this increment:
`2fa5450`. No cross-engine equivalence is claimed.

### User failure actions (14 October 2026)

- [x] Default next iteration, continue journey, and stop this user in users mode.
- [x] Retain request/journey failures and transactional verification semantics.
- [x] Stop only the failed user; preserve peers and pending ramp starts.
- [x] Mandatory stopped-user check, schema 9 metadata and bundle validation.
- [x] Builder controls, preview, journey guidance, live results, CLI and HTML.
- [x] Explicit JMX Continue/Start Next Thread Loop/Stop Thread mappings.
- [x] Native local users-mode CSV datasets.

Focused execution tests cover nested loops, deadlines, safety limits, missing
values after failed verification, peer isolation, cancellation, JMX conversion,
CLI artifacts, and native bundle consistency. `make check build` passed all race
tests, vet, UI tests and both builds; the final builder fix passed UI checks and
build. Browser verification of the same nested plan observed 20 HTTP attempts
with Continue, 4 with the default next iteration, and 2 with Stop this user.
The last run failed its mandatory check with request/journey error thresholds
disabled. Saved settings, rate-mode cleanup, simple failure previews, default
criteria and the 320px layout were checked. Browser warning/error logs were
empty on the final build. QA images are in ignored `work/failure-qa/`; the
isolated dashboard is at localhost:52940. No cross-engine or capacity equivalence
is claimed.

### User ramps and startup delay (14 October 2026)

- [x] Absolute user-start schedule, lazy user creation, cancellation of future starts.
- [x] Separate startup/load/drain timing and sampled active/started/finished users.
- [x] Builder controls, schedule preview/chart, CLI flags, starter and guides.
- [x] JMX default ramp/scheduler delay conversion with explicit millisecond rounding.
- [x] Schema 8 schedule contract, native bundle import validation and old schemas.
- [x] Next increment: native CSV datasets for local users-mode journeys.

Validation: `make check build` passed vet, all race tests, UI tests and both
binaries. Follow-up dashboard/starter and duration-only tests passed after final
chart refinements. Fixtures cover scheduled first starts, retained per-user
cookies, finite completion, one-user ramps, startup/ramp cancellation, duration
closure with journey drain, JMeter millisecond rounding, inactive source delays,
and schema 8 import/persistence consistency. The CLI emits JSON/HTML schedule data.
Browser QA passed 12/12 HTTP attempts with all four users started, an early
duration stop with 1/4 users started, and startup Stop with zero HTTP attempts.
The guided ramped JMX plan completed 6 journeys / 18 HTTP attempts across three
users. Invalid durations block execution, switching to rate mode removes user
schedule fields, 320px builder/results have no page overflow, and console
warning/error logs were empty. Observed users remain sampled, not exact peaks.
This verifies native behavior against source-backed mapping rules; executing
the same fixtures in JMeter and comparative performance remain unverified.
Review: localhost:52840 with isolated `/private/tmp/gometer-users-review` data;
demo target localhost:52841; screenshots in ignored `work/users-qa/`.

### Executable control flow acceptance (10 October 2026)

- Recursive request/if/loop configuration preserves existing journey files.
  Validation checks definite variable availability, scoped iteration counters,
  nesting depth (8), counts/while caps (1000), and worst-case execution cost
  (10,000 nodes per journey). Conditions use explicit comparisons, no scripts.
- Fixed loops execute a positive count; while loops check before each iteration
  and fail with `loop_limit` when the condition stays true at the cap. Failures,
  deadlines, and cancellation propagate through enclosing controls.
- Schema 6 distinguishes repeated request attempts, distinct reached/skipped
  journeys, and conditional bypasses. Bounded per-control counters describe
  decisions and body starts. Current workers merge counts and histograms;
  schema 2–5 imports and sequential result schemas remain supported.
- The canvas supports nested child paths, conditions, loop modes/limits,
  scoped counters, path moves, subtree duplication/removal, undo, structural
  previews, and read-only observed decisions/iterations. `examples/flow.json`
  and the Branches and loops starter use the local demo.
- Validation includes Go vet/race fixtures, local worker execution/aggregation,
  persistence and import tampering checks, report escaping, and browser checks
  for control edits, responsive bounds, and independently counted requests.

### Sequential journey canvas acceptance (10 October 2026)

- The canvas uses the existing CLI configuration and engine validation. There is
  no second execution format, external diagram dependency, or network request
  triggered by editing, validating, panning, zooming, or previewing.
- Request, pause, assertion, extraction, dataset, variable, weight, timeout, and
  per-journey criteria controls preserve unrelated configuration. Reordering
  reports missing dependencies; drafts block validation, save, export, and start.
- Final diagrams use recorded per-step and journey measurements, show skipped
  requests without fabricated latency, and keep per-step p95 distinct from
  whole-journey p95. Executable branches/loops were added in the subsequent slice.
- Go vet, all race-enabled tests, both builds, and eight JavaScript model tests
  passed. The browser suite passed conversion from POST to journey, drag and
  button reordering, undo, extraction/assertion edits, draft retention, save/load,
  literal HTML content, and page bounds at 1440/1024/768/400/360/320 pixels.
- The counted target received zero requests during editing, preview, validation,
  and saving. Twenty successful journeys produced exactly 60 HTTP requests;
  twenty deliberate login assertion failures produced 20, with later steps
  skipped. This verifies the editor integration, not comparative performance.

### Target-server telemetry implementation

- Opt-in `telemetry` configuration selects 1–16 scalar gauges/counters from one
  Prometheus text endpoint. Names and full label sets match exactly; no PromQL,
  automatic discovery, aggregation, OpenMetrics, or timestamp freshness guesses.
- Collection starts asynchronously near admission, skips missed ticks, and
  makes one bounded final observation after drain. Scrapes never enter request
  counters, HTTP timing, latency populations, or load acceptance checks.
- Counter increases/rates cover consecutive valid readings only. Missing values
  and reset intervals stay unknown. Gauge means/extrema are sampled; server
  metrics can include unrelated traffic and omit early activity or brief spikes.
- Endpoint failures are visible as incomplete telemetry without failing load
  checks. Stop-canceled pending probes are counted separately, then a final
  bounded scrape is attempted if sampling started. No automatic health verdict.
- Private exporter tokens and optional verified TLS CAs are environment/runtime
  settings, never browser payloads or saved configurations. No redirects,
  automatic decompression, proxy forwarding, or workload credential reuse.
- Distributed telemetry runs once on the coordinator, which strips the exporter
  configuration before worker reservation. Workers retain their engine schemas.
- Configured telemetry uses schema 5 locally or with workers. Other runs retain
  schemas 3/4, and the dashboard imports schemas 2–5 without network requests.
  Whole-run statistics and the latest 900 scrape observations persist/export.
- The dashboard shows values, explicit gaps, resets, coverage, and selectable
  gauge/counter-rate charts. Its workload timeline and scrape timeline are
  separately bounded. Wide telemetry tables scroll within narrow panels.
- The rebuilt demo exposes actual process CPU, Go heap/goroutines, bounded work
  capacity/occupancy, and work outcomes at `/metrics`. Scrapes do not count as
  demo work. Details and supported format limits are in docs/telemetry.md.

Target telemetry validation on 2026-10-05:

- `make check` passed go vet and race tests in all ten internal packages. Native
  binaries built, seven examples validated, and JavaScript syntax checking passed.
  Linux/amd64 and Windows/amd64 cross-builds passed; runtime checks used macOS.
- Collector tests cover exact/escaped labels, shared family TYPE metadata,
  whitespace, real zeros, duplicate/missing/non-finite series, gaps, resets,
  retention, clock adjustments with elapsed-time bounds, body/line caps,
  deadlines, verified TLS, and redirect rejection.
  A five-second parser fuzz check completed 29,311 executions without failure.
- Cancellation tests cover a pending probe discarded during Stop, the final
  independent scrape, and zero exporter requests before a canceled shared start.
  Engine/coordinator tests preserve workload counts and isolate credentials;
  only the coordinator scrapes during distributed tests.
- CLI tests cover credential setup before traffic, no-network validation,
  diagnostics-off with explicit telemetry, credential-free reports, and
  `-telemetry=false` without credentials or an accessible CA file.
- Dashboard tests cover live observations, workspace reopen, schema 5 bundle
  imports without traffic, and rejection of inconsistent observation accounting.
  HTML tests verify escaped metadata, explicit missing values, and valid zeroes.
- CLI stress planned 680 arrivals, dispatched/completed 679, and reported one
  scheduler miss. Fourteen scrapes were complete; occupancy reached eight slots,
  rejection increase was 295, and baseline/recovery had zero request errors.
  The intentional overload correctly produced exit 1. Scratch JSON/HTML reports
  are `work/telemetry-stress.json` and `work/telemetry-stress.html`.
- Browser stress across two local workers completed 679 HTTP attempts, with
  complete worker results and 15 complete coordinator scrapes. The sampled
  occupancy peak was eight slots; 292 observed rejections matched measured 503s.
  This validates coordination/measurement behavior, not scaling performance.
- Browser users mode with diagnostics disabled passed while one selected series
  was absent: the missing row stayed unknown, telemetry was incomplete, and
  workload checks remained independent (118 attempts, eight recorded scrapes).
  A distributed low-rate run passed 600 attempts with 63 complete scrapes.
  Browser Stop saved 282 partial HTTP attempts and 30 complete scrapes, with
  future canceled arrivals excluded from measured counts. The narrow table scrolls internally;
  both 1240px and 586px layouts had no page overflow.
- Live observations, chart switching, MiB units, and malformed metric JSON
  blocking were inspected. Browser error/warning logs were empty. Updated
  dashboard, demo, and workers remain at ports 8790, 8787, and 8801/8802,
  respectively. Existing `.gometer/` history is preserved. The dashboard/workers
  retain the private token from ignored `work/distributed-token`.
- Preview: `work/telemetry-preview.jpg`. HTML generation and escaping passed;
  standalone HTML visual layout remains unverified under the earlier browser
  local-file restriction.

Implementation decisions for distributed workers:

- Rate-mode workers execute interleaved global arrival ordinals. Fractional
  phases, pause phases, seeded jitter, mix selection, IDs, and data rows preserve
  the shared offered schedule. Journeys remain on their assigned worker.
- The fleet concurrency cap divides into fixed worker quotas. There is no work
  stealing, load replay, or automatic reassignment when a worker fails.
- All workers pass clock/protocol/authentication preflight and reserve before
  a shared future start. Three probes estimate offset; reported round trips
  expose uncertainty. Scheduling then uses each worker's monotonic clock.
- Worker control requires a shared environment token and remote TLS. There are
  no token fields in configurations, reports, or browser payloads. Redirects and
  browser-origin worker control are rejected. TLS verification stays enabled.
- One active/reserved job per worker, 30-second reservation expiry, and a
  10-second coordinator lease. Cancellation collects available partial results;
  unavailable final measurements remain unknown and fail the run.
- Sparse raw histogram buckets merge before global thresholds are evaluated.
  Final counts are checked across run, phases, scenarios, and steps. Resource
  summaries stay per process; in-flight peaks sum worker peaks as an upper bound.
- Distributed schema 4 results and compact worker timeline counters persist in
  the dashboard. Schema 2/3 imports remain supported. Local results stay schema 3.
- Worker control payloads are bounded to 32 MiB. Dashboard bundles retain their
  existing 16 MiB limit and never contain raw transfer histograms or credentials.

Distributed worker validation on 2026-10-05:

- `make check` passed go vet and race tests in all nine internal packages.
  Both binaries built and the dashboard JavaScript passed syntax checking.
- Linux/amd64 and Windows/amd64 cross-builds passed. Worker processes ran on
  macOS; cross-builds do not establish runtime behavior on those other systems.
- Tests cover fractional and pause phases, jitter, global journey IDs and mix
  weights, fixed quota division, exact histogram merging, and nested accounting.
- Control tests cover authentication, no-traffic preflight failures, reservation
  expiry, idempotent commits, coordinator lease expiry, worker loss, cancellation,
  verified TLS, redirect rejection, and loopback-only plaintext listeners.
- Dashboard tests preserve schema 4 worker results/timeline points across
  persistence and imports. Slow cleanup after Stop cannot extend the load window
  or prevent importing a saved partial run. Imports send no target traffic.
- Two separate local worker processes passed CLI smoke (40 HTTP attempts),
  checkout (20 journeys / 60 attempts), and jittered mix (40 journeys, Health 30 /
  Work 10), with no errors or missed demand. Diagnostics-off omitted resource and
  HTTP timing fields. Scratch JSON/HTML reports are under ignored `work/`.
- Browser smoke passed 100 attempts; checkout passed 300 journeys / 900 attempts.
  Both workers reported their assigned arrivals and final process diagnostics.
  A final smoke after restoring both workers passed another 100 attempts.
- Browser Stop collected 218 partial checkout journeys / 654 HTTP attempts,
  including one canceled checkout. The load timeline remained bounded.
- Stopping one owned worker during a browser run correctly failed the run,
  preserved 141 known HTTP attempts from its peer, and marked the lost worker's
  final counts Unknown. Its planned remainder includes future canceled arrivals;
  it is not measured dropped traffic. Both workers were restored afterwards.
- Live counters, final complete/incomplete rows, desktop and narrow layouts were
  inspected. Neither width had page overflow, and browser error logs were empty.
- Dashboard remains at http://127.0.0.1:8790 with `.gometer/` history preserved.
  Local workers are at 127.0.0.1:8801 and :8802. These three processes inherit the
  same token from owner-readable ignored `work/distributed-token`; restarting
  them requires that environment value. Never include the token in reports.
- Preview screenshots are `work/workers-preview.jpg` and
  `work/workers-loss-preview.jpg`. The demo target remains at 127.0.0.1:8787.
  These local checks validate behavior, not distributed scaling performance.

Implementation decisions for diagnostics:

- Diagnostics default to enabled; JSON `diagnostics: false` and the CLI
  `-diagnostics=false` flag disable both HTTP tracing and process sampling.
- Schema 3 adds optional `generator` and `http` fields. The dashboard still
  imports schema 2 bundles and preserves older history without inventing metrics.
- CPU is process time divided by sampled wall time; 100% means one core. macOS
  and Linux expose CPU and process lifetime RSS peak through getrusage. Other
  platforms explicitly mark these unavailable and keep Go runtime sampling.
- Samples occur at start, about once per second, and after drain. CPU/GC deltas
  cover this window; Go memory/goroutine peaks are sampled. Dashboard work is
  part of the same process. Resident-memory peak can include earlier runs.
- Seven bounded histograms describe observed HTTP intervals. Populations differ
  for reused connections and failed attempts; durations overlap and cannot be
  summed. HTTP metrics aggregate by run, phase, and journey step.
- Trace callbacks freeze at network completion. Unfinished events have no
  invented duration; callbacks that arrive after completion are ignored.
- There is no automatic saturation verdict or claim about target-server health.

Diagnostics validation on 2026-10-05:

- `make check` passed go vet and race tests in all eight internal packages.
  Additional timeout, response-cap, and correlation-capture timing assertions
  passed the engine race tests. Both binaries built; JavaScript syntax passed.
- Linux/amd64 and Windows/amd64 cross-builds passed. Runtime resource sampling
  was exercised on macOS; the cross-builds do not establish OS runtime behavior.
- Tests cover HTTP/1.1 reuse, HTTP/2 over TLS, delayed bodies, unavailable OS
  counters, CPU deltas, sampled resource peaks, cancellation, and late callbacks.
- Dashboard tests preserve diagnostics through persistence, restart, and
  schema 3 imports, while accepting schema 2 bundles without sending traffic.
- CLI smoke: 40 attempts / 38 reuses. Checkout: 20 journeys / 60 attempts,
  20 timing samples per step. Both passed with no errors or missed demand.
- Localhost verification observed one DNS lookup and two address dials,
  including one failed dial followed by a successful connection.
- Browser smoke: 100 attempts / 98 reuses. Checkout: 50 journeys / 150 attempts,
  with live resource and timing panels, no errors, and no missed demand.
- CLI and browser diagnostics-off runs omitted transport/resource fields and
  resource timeline samples. The browser displayed the absence message.
- Native exported journey bundle preserved all 150 timings, 50 timings per
  step, schema 3, and the resource timeline. Existing workspace history remained.
- Browser comparisons displayed new timing/CPU/heap rows and flagged differing
  instrumentation. Older saved results showed the diagnostics absence message.
- Desktop and narrow layouts were inspected; the narrow view had no horizontal
  overflow and browser error logs were empty. Sample definitions and observer
  overhead are in docs/diagnostics.md.
- The updated dashboard remains at http://127.0.0.1:8790 using `.gometer/`.
  Scratch reports and the preview screenshot are under ignored `work/`.

Implementation decisions for milestone 3:

- `gometer ui` serves embedded HTML/CSS/JavaScript and starter configurations on
  an explicit loopback IP. The same Go validator and engine execute every run.
- One active dashboard run; browser disconnects do not cancel it. Stop and
  process signals cancel, drain, and persist partial results. Startup marks
  unfinished reservations abandoned rather than resuming them.
- `.gometer/` stores atomic, owner-readable scenario and run files. Limits are
  200 scenarios, 1000 runs, and 900 retained timeline points per run. The final
  result still covers the entire run.
- Charts use interval counter deltas and cumulative latency quantiles. Cards
  retain the CLI load-window denominator. HTTP and journey units are explicit.
- Configuration and run-bundle imports send no traffic. Bundle imports validate
  final count equations and assign a new ID; original traffic is not verified.
  Comparison flags differing conditions and makes no regression verdict.
- Host/origin checks and a random startup API token protect local controls.
  Native downloads use a scoped HttpOnly SameSite cookie; API mutations still
  need a custom token header. Short-lived configuration exports stay in memory.
- A failed final save leaves the result available for export and blocks new
  dashboard runs until restart. The dashboard has no remote/multi-user mode.

Milestone 3 validation on 2026-10-05:

- `make check` passed go vet and race tests in all seven internal packages;
  both binaries built and the dashboard JavaScript passed syntax checking.
- API integration tests covered no-traffic validation, host/origin/token
  rejection, one-run concurrency, progress, cancellation, graceful shutdown,
  persistence/restart, native downloads, import counts, disabled threshold
  round-trips, abandoned-run recovery, timeline bounds, and output failures.
- Browser smoke: 40 HTTP attempts passed with no errors or missed demand.
- Browser checkout: 20 journeys / 60 HTTP attempts passed with correct units,
  per-step results, and separate request/journey cards.
- Browser comparison flagged different targets and arrival units. Native run
  export/import preserved results and marked the imported run.
- Native editor configuration download passed browser verification and CLI
  validation. Report downloads use attachment responses for browser compatibility.
- Browser Stop saved an interrupted 6-attempt run with one canceled request.
- Desktop charts and mobile layout were visually checked; the narrow viewport
  had no horizontal overflow. Browser error logs were empty.
- QA artifacts live in ignored `work/dashboard-qa`; the user workspace defaults
  to `.gometer/`. The existing demo listener was reused and left running.

Implementation decisions for milestone 2:

- Report schema 2 adds journey, scenario, and step results. Legacy request
  metrics and thresholds remain supported; max_drop_percent checks the workload
  arrival unit. Explicit scenarios use journeys/second for configured rates.
- Scenario selection uses a deterministic weighted cycle in configuration
  order. Arrivals support constant spacing or seeded stratified jitter;
  Poisson arrivals are not implemented.
- Data rows are inline strings, selected by scheduled scenario ordinal.
  Variables reset per journey. Users retain cookies across iterations; rate
  arrivals receive fresh sessions.
- Steps remain on the base URL origin. Templates provide literal, URL-query,
  path-segment, and JSON-string expansion. JSON selectors use RFC 6901 pointers.
- Verification fails fast and records later steps as skipped. Admitted journeys
  drain remaining steps under a journey timeout. Correlation capture is capped
  at 1 MiB; sessions cap cookie keys and stored bytes and report limit failures.

Validated on 2026-10-05:

- `make check` passed go vet and race-enabled tests in all six internal packages.
- Both executable builds and all five example validations passed.
- Authenticated checkout: 20/20 journeys, 60 HTTP attempts, no errors or drops.
- Jittered weighted mix: 40 journeys, Health 30 / Work 10, no errors or drops.
- Deliberate assertion failure: 10 failed journeys, 20 attempts, 10 checkout
  steps skipped, and exit 1.
- Capacity limit: 100 scheduled journeys, 17 admitted, 83 dropped, 51 requests,
  and exit 1. The JSON and HTML report used the correct journey demand counts.
- Legacy smoke: 40 requests passed. Journey SIGINT saved consistent partial
  reports and exited 130.
- HTML generation, escaping, and branch rendering were verified; visual layout
  remains unverified because the earlier browser tool blocked local-file URLs.

Milestone 1 validation on 2026-10-04:

- `go test -race ./...` passed all five internal packages.
- `go vet ./...` and both executable builds passed.
- All three example scenarios passed configuration validation.
- Local smoke run: 40/40 completed, 20 started req/s, zero errors or drops.
- Local stress run: 980/980 completed, baseline 0% errors, overload 49.67%
  errors, recovery 0% errors; correctly returned exit 1.
- POST/users run passed; SIGINT returned 130 and saved consistent partial results.
- HTML generation and escaping were verified. Browser visual review was blocked
  by the browser tool's local-file URL policy, so visual layout is unverified.

Generated binaries, local reports, and scratch verification scripts are ignored
by .gitignore. The demo target is a correctness illustration, not a benchmark.

## 6. Bundled JMeter parity program (2026-10-08)

User priorities: improve creating/configuring tests, support insecure workload
HTTPS, cover bundled JMeter first, then add capabilities beyond that baseline.
The detailed inventory and gates live in [docs/jmeter-parity.md](docs/jmeter-parity.md)
and `internal/dashboard/assets/coverage.json`; the dashboard renders that catalog.

- [x] Guided builder, headers/body/status controls, graphical phases, pass criteria.
- [x] In-app user guide, first-test practice settings, contextual help, and troubleshooting.
- [x] Explicit insecure workload HTTPS across CLI, journeys and workers.
- [x] Visible TLS state in artifacts/imports and comparison conditions.
- [x] Sourced in-app capability comparison with honest current limitations.
- [ ] Finish HTTP authoring, data, correlation, assertions and recording.
- [ ] Execution controls, user groups/ramps, debugging and distributed users.
- [ ] Protocol adapters and integration fixtures.
- [ ] Java bridge, scripting, extensions, JMX/JTL compatibility and conformance.
- [ ] Beyond-parity protocols, result integrations and measured benchmark evidence.

This program supersedes the previous handoff ordering; it does not mark full
JMeter parity complete. Preserve Go's native path; optional Java adapters require
a separate supported runtime. Do not label grouped capability coverage as a
percentage of component/option compatibility or claim performance superiority.

Validation on 2026-10-08:

- `make check build` passed vet, race tests across all ten internal packages, and both builds.
- TLS fixtures cover verified defaults, explicit opt-in, HTTP/2, journeys, worker
  propagation, independent worker/telemetry verification, CLI false overrides,
  report labeling, bundle round-trips, and mismatched-bundle rejection.
- All seven example configurations still validate.
- Browser QA passed staged load with a zero-rate pause (5 attempts), users mode
  (6 attempts), and an edited POST test with a disabled error criterion
  (12 attempts). Each completed with zero request errors or missed demand.
- Invalid header drafts block validation; removing a phase with an empty rate
  recovers correctly. Removing the last phase returns to a single rate.
- Desktop and 320px checks showed no page overflow; coverage search/filtering
  and the 68-family catalog rendered correctly; browser warning/error logs were empty.
- QA data and builder screenshot are under ignored `work/`; the review dashboard
  uses `.gometer/` on localhost:8790. No external targets were exercised.

User-guide validation on 2026-10-09:

- Dashboard race tests, the dashboard binary build, JavaScript syntax, and diff
  whitespace checks passed.
- The practice preset planned and completed 10 local-demo requests with no
  errors or missed arrivals; all configured checks passed. Loading the preset
  and validating it did not start a run.
- Contextual help preserved draft edits and the displayed result. Topic links
  focused the relevant heading, and return links focused the matching field.
- Welcome-tip dismissal survived reload, restoring the tip worked, and the
  sidebar guide remained available. Troubleshooting disclosures opened correctly.
- Desktop and 320px guide/builder layouts had no horizontal page overflow;
  browser warning/error logs were empty.
- QA history is isolated under ignored `work/guides-qa/`. The idle preview on
  port 8790 was updated with its existing history, and its editor draft was
  preserved in `work/editor-draft-before-guides.json` before refreshing.

## 7. Documentation and adoption guidance (2026-10-09)

- [x] Concise README with runnable setup, capability boundaries, and three real app screenshots.
- [x] Documentation index, first-test guide, configuration/CLI references, and measurement definitions.
- [x] Architecture, operations, workspace backup/recovery, security, and contribution guidance.
- [x] Enterprise readiness gates separating implemented safeguards from outstanding release, security, licensing, and compatibility decisions.

Validated against the current development checkout:

- Both builds, vet, and race-enabled tests in all ten internal packages passed.
- All seven starter configurations and three complete JSON examples in the docs validated.
- Twenty Markdown files passed local link/anchor checks (158 references), and
  all 30 run flags appear in the CLI reference. Nine image references resolve.
- The rendered README loaded all three images; the expandable gallery and
  getting-started link worked. Images total about 315 KiB, use isolated local-demo
  data, and are documented as demonstrations rather than benchmarks.
- CLI smoke completed 40 requests; the screenshot baseline completed 800 requests.
  Both passed with no errors or missed arrivals.
- Cleanly stopped and archived the isolated screenshot workspace, restored it
  into a separate directory, reopened its completed 800-request history record,
  and exported its run bundle without sending traffic.

Documentation does not certify enterprise readiness. Enforced CI, independent
benchmark/security review, release provenance/support policies, and a maintainer
license decision remain adoption gates. No release, tag, or publication was changed.


### Native CSV datasets (15 October 2026)

- [x] Bounded UTF-8/quoted CSV parser shared by preview, validation, CLI and execution.
- [x] Portable source, explicit column mapping and ignored columns; no implicit filesystem paths.
- [x] Shared/per-user per-scenario cursors, recycling and normal stop-user EOF.
- [x] Canvas import/edit/remove with undo, source preview and contextual guidance.
- [x] CLI file flags, schema 11 consumption counters, reports and bundle persistence/validation.
- [ ] Next: guided JMX CSV source attachment and mapping with explicit compatibility review.
- [ ] Separate extension: CSV allocation in rate/distributed mode and remaining JMeter CSV options.

Native CSV EOF ends a user before another journey is admitted, without inventing
an HTTP error, drop or latency sample. Failure actions consume their current row.
Pending ramp starts continue after another user reaches EOF. Saved source includes
ignored columns; exported result aggregates contain no cell values.


CSV validation on 16 October 2026:

- `make check build` passed JavaScript syntax/model tests, vet, all race tests and both binaries.
- Parser fixtures cover BOM/UTF-8, quotes, multiline fields, delimiters, mapping collisions and size/row/column/cell bounds.
- Engine fixtures cover shared/per-user recycling and EOF, weighted scenarios, stable loop rows, failure policies, ramp continuation, duration and manual cancellation.
- Schema 11 bundles roundtrip through persistence/import; mismatched settings, counts and timelines are rejected without traffic. CLI validation and JSON/HTML output passed.
- Browser review loaded a synthetic file, rejected duplicate mappings, preserved cancel/undo and reopened saved source after restart. Per-user EOF completed 9 journeys; shared EOF completed 3, each with 3 users and 3 source rows, with zero errors or missed arrivals.
- Light/dark dialogs and a 320px viewport were inspected. No horizontal page/dialog overflow was observed; final browser warning/error logs were empty. Screenshots are in ignored `work/csv-qa/`.
- The final primary-button styling passed syntax/build checks and browser review. Local Markdown file links (176) resolve.
- Review dashboard: localhost:53240; isolated local demo: localhost:53241. No external targets were exercised. CSV source is embedded in saved configurations; no JMX CSV compatibility claim is made.


### Keyboard shortcuts (16 October 2026)

- [x] G then B/L/H/G/C navigates Build, Live, History, Guide and Coverage, with a two-second hint and heading focus.
- [x] Command/Control + Enter validates, + S saves, + Shift + Enter starts through the existing builder checks; + Z undoes canvas edits outside text fields.
- [x] Question-mark reference, button hints and modifier-key accessibility metadata; sidebar/guide/mobile More entry points.
- [x] Character-key opt-out persisted in this browser; typing, native selects, dialogs, IME composition and held-key repeats do not trigger character commands.
- [x] Inspector drafts settle like a button click before save, validate or start. Canvas shortcuts preserve text undo.

Validation: 23 UI/model tests, vet, the full race suite with sequential packages
(`GOFLAGS=-p=1 make check build`), and both binaries passed. The initial parallel
run missed short deadlines in two existing distributed fixtures; both passed in
isolation and in the sequential full run, without changing those tests or engine
behavior. Timing-fixture resilience remains a separate follow-up.

Browser review verified character-key persistence across reload, blocked dialog
navigation, ordinary typing, heading focus, save from an unfinished inspector
field, validation, canvas undo, and a keyboard-started local run with 3 successful
journeys and no errors/misses. Light/dark references and a 320px viewport were
inspected; no page/dialog horizontal overflow was observed. Mobile More closes
before the dialog and receives focus after Escape. Final browser warning/error
logs were empty. Later focus/Shift-key refinements passed syntax/build checks,
controller tests and the relevant browser check. Evidence is in ignored
`work/shortcuts-qa/`; the review dashboard is at localhost:53340 with its local
demo at localhost:53341. No commit or publication was performed for this increment.

### GoMeter rename (20 October 2026)

- [x] GoMeter branding in the dashboard, guides, standalone reports, and README media.
- [x] `gometer` and `gometer-demo` commands, module imports, Docker entrypoint, release packages, and deployment templates.
- [x] `.gometer/` workspace default, with `-data` for an explicitly selected directory.
- [x] `GOMETER_*` worker and telemetry settings, read directly without aliases.
- [x] Browser preferences, theme events, authentication headers/cookies, demo session cookies, and demo telemetry metrics use `gometer` identifiers.
- [x] Compatibility helper and workspace discovery fallback removed; configuration has one set of names.

Validation: 23 UI/model tests, vet, the full race suite with sequential packages
(`GOFLAGS=-p=1 make check`), and both executable builds passed. All ten bundled
JSON examples validate, both Kubernetes overlays render locally, and 212 local
Markdown links resolve. An audit of active source, tests, examples, and docs found
no remaining references to the previous application name.

Earlier check runs hit existing short-window timing failures in CSV startup,
phase recovery, and a distributed dashboard completion wait. The CSV test passed
in isolation; all packages subsequently passed the final standard check. No
engine behavior or timing-test assertions were changed for this rename. Logs are
in `/private/tmp/gometer-clean-rename-qa/` on the development machine.

An isolated local check confirmed the `.gometer/` default, no discovery of the
previous workspace directory, rejection of the previous worker environment alias,
and the new dashboard header, token metadata, download cookie, demo session cookie,
and metric names. It completed a 3-journey / 9-request dashboard run, a 4-request
CLI telemetry run, and a 4-request distributed run, all without errors. Evidence:
`/private/tmp/gometer-clean-rename-qa/check-321ljgdc/`. The local workspace folder
was renamed without changing its file contents; the review preview starts with
a fresh temporary workspace at localhost:53440 and its demo at localhost:53441.
The changes remain uncommitted.


### Application environment settings (21 October 2026)

- [x] Dashboard listen address, public origin, and workspace via `GOMETER_UI_*`.
- [x] Worker listen address, display name, and TLS certificate/key paths via environment settings.
- [x] Shared `GOMETER_CONFIG` file selection for `run` and `validate`.
- [x] Explicit flags override environment settings, which override built-in defaults; empty values remain explicit overrides.
- [x] Existing origin, authentication, and TLS validation retained; workload fields remain in JSON or explicit flags.
- [x] CLI help and operator guides document the variables, precedence, and restart behavior. Released-image deployment templates retain their compatible flags.

Validation: focused public-CLI and real dashboard/worker startup tests passed with
race detection. These include workspace persistence after restart, authenticated
worker control, verified TLS, invalid values, and flag overrides. The final
`GOFLAGS=-p=1 make check build` passed, including 23 UI/model tests, vet, the full
race suite, and both executables. All 233 local Markdown links resolved before
this roadmap entry. A built-executable smoke check verified environment-only
configuration selection and dashboard startup, documented workspace selection,
HTTP serving, help output, and graceful shutdown. No cluster deployment was run.
Standards review: 0 findings. Spec review: 0 findings.
