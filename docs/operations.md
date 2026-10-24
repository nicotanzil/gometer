# Operations guide

[Documentation](README.md) · [Project overview](../README.md)

Use GoMeter as a bounded experiment: choose the target and acceptance criteria,
record the environment, run the workload, retain evidence, and inspect what was
actually delivered. The dashboard is single-user and listens on loopback by
default. Distributed load uses the separate [worker control plane](workers.md).

## Plan a repeatable run

Record the binary version, source revision, configuration, target build, generator
machine/OS, network route, TLS/proxy settings, and whether diagnostics/telemetry
are enabled. Retain these alongside results. Save a baseline before changing
load and change one variable at a time.

Choose a rate and concurrency cap appropriate to the test environment. As a
rough mean-occupancy estimate, 100 arrivals/s with a 50ms response consumes about
5 concurrent slots. Variation, scheduling delay, and journey duration can require
more room; this estimate does not guarantee zero drops. Review actual in-flight
work, capacity/scheduler misses, and arrival delay.

Configuration validation limits are input bounds, not tested capacity ratings.
Response capture, histograms, diagnostics, phases, scenarios, and concurrency
all consume resources. Start small, observe the generator and target separately,
and establish capacity for your own workload. There is no universal supported
RPS or concurrency claim.

## Run and stop

| Action | Behavior |
| --- | --- |
| Normal admission end | New work stops; admitted requests/journeys drain under their deadlines |
| Close or reload browser | Dashboard-owned run continues while its process remains alive |
| Dashboard Stop run | Cancels admission and active requests; attempts to save an interrupted result |
| CLI Ctrl+C / SIGTERM | Cancels active work, attempts partial reports, and returns 130 |
| Dashboard Ctrl+C / SIGTERM | Cancels its active run and waits for final persistence before exiting |
| Worker coordinator loss | Worker cancels when its last renewed 10-second lease expires |

Allow time for cancellation, drain, final telemetry collection, and result
persistence. Force termination can lose final results. Worker jobs are held in
memory, and traffic is never automatically reassigned or replayed after failure.
Missing worker results mean unknown traffic and fail completion checks.

## Worker transport settings

Use verified HTTPS for remote workers by default. Optional plaintext control
requires `GOMETER_WORKER_ALLOW_HTTP=true` on workers and
`GOMETER_ALLOW_HTTP_WORKERS=true` on the dashboard/CLI coordinator, with the same
shared worker token on both sides. These are startup settings; saved tests and
imports cannot grant permission. Both processes warn that the token and test
configuration travel without application TLS. Record that transport choice with
the experiment and restrict the network accordingly.

Restart between runs to change these settings. To return to HTTPS, configure a
valid worker certificate/key pair and coordinator CA trust, change each worker
origin to `https://`, and remove both opt-ins (or set their flags to false).
A bad certificate/key pair fails startup; HTTPS requests never downgrade to HTTP.
See [worker setup](workers.md#optional-remote-http-control) and the
[optional Kubernetes overlay](kubernetes.md#optional-http-worker-fleet).

## Workspace ownership and retention

```sh
./bin/gometer ui -listen 127.0.0.1:8790 -data /path/to/private/workspace
```

A service manager can supply the same settings as `GOMETER_UI_LISTEN` and
`GOMETER_UI_DATA`, leaving its command as `gometer ui`. Use
`GOMETER_UI_PUBLIC_ORIGIN` for a trusted HTTPS ingress. Explicit flags override
these variables; remove a flag when the environment should control that option.
Settings are read at process startup, so restart the service after changing them.
See [environment configuration](cli.md#environment) for all supported settings.


Use one dashboard process per data directory. There is no cross-process workspace
lock or coordination; sharing a directory can overwrite state or mark another
process's in-progress records abandoned. The API allows one active run per
dashboard, while separately launched CLI processes are independent.

New workspace directories use 0700 and JSON records use 0600 on systems that
support these modes. Existing directory permissions are not tightened. On other
platforms, configure equivalent account and filesystem access controls.
The data is not encrypted at rest.

The worker catalog is stored in the same workspace under `workers/` and is
included in a complete workspace backup. Browsers share the catalog through one
dashboard process. Dated connection observations are not readiness guarantees;
checks are manual and never send workload. Catalog changes do not rewrite saved
scenarios, active configurations, or run identities. See the
[worker catalog guide](workers.md#reusable-worker-catalog).

## Private ingress access

The dashboard listens on loopback by default. A cluster deployment can opt into
a non-loopback listener behind a TLS-terminating private ingress by setting an
exact HTTPS origin:

```sh
./bin/gometer ui -listen 0.0.0.0:8790 \
  -public-origin https://gometer.example.internal \
  -data /data
```

The ingress must preserve the browser's `Host` and `Origin` headers and route the
root path without a prefix. It must enforce VPN access or authentication plus an
allowlist. The origin setting validates request boundaries; it does not provide
user authentication. Keep one dashboard replica and one private persistent volume
for its data directory. Do not share that directory between pods.

| Item | Current bound / retention |
| --- | --- |
| Configuration import | 1 MiB |
| JMX source import | 1 MiB; original source and explicit properties retained on save |
| Migration drafts | 50 non-runnable drafts, stored separately from scenarios |
| Dashboard run record or imported bundle | 16 MiB |
| Saved scenarios | 200 |
| Catalog workers | 200, saved under workers/ in the workspace; at most 32 selected per run |
| Saved runs | 1000 |
| Dashboard workload timeline | Latest 900 points, roughly 15 minutes at its normal cadence |
| Telemetry timeline | Latest 900 scrape observations, independently retained |
| Temporary configuration exports | Up to 16 for five minutes, in process memory |
| Worker results | In memory until another reservation; lost on worker restart |

Final aggregates cover the whole run even when earlier timeline points have
expired. There is no automatic age-based archive or retention service. Export
needed runs and scenarios before removing history entries; deletion in the app
has no recovery bin. Coordinate retention with your artifact storage policy.

## Back up and verify recovery

Stop the dashboard cleanly before copying its complete workspace. Store archives
with restricted access because saved configurations may contain workload secrets.
For the default `.gometer` workspace on a POSIX shell:

```sh
umask 077
tar -czf "gometer-workspace-$(date +%Y%m%d-%H%M%S).tar.gz" .gometer
```

To verify a backup, extract it into a new private directory, then start a separate
dashboard pointing at the extracted `.gometer` directory:

```sh
./bin/gometer ui -listen 127.0.0.1:8793 -data /path/to/restored/.gometer
```

Confirm the expected saved scenarios and run counts, inspect a completed result,
and export a run bundle. Recovery verification should not start workload traffic.
Keep the original workspace until the restored copy is verified.

Records left `running` or `stopping` by an unclean process exit are marked
`abandoned` on next startup; they are not resumed and cannot be compared.
Malformed saved files fail workspace startup. Preserve the affected directory,
inspect the error, and recover from a verified copy rather than editing evidence
in place. If final persistence fails, export the still-visible result before
restarting; new runs remain blocked until the process is restarted.

## Artifacts and CI

Default CLI reports use timestamped paths under `results/`. Explicit `-out` and
`-html` paths replace older files atomically. Ensure separate jobs do not share
those paths. The CLI checks output paths before traffic, but disk or permission
failures can still occur afterward. Inspect exit 2 and stderr, and verify required
artifacts exist before treating a run as retained evidence.

Configure CI to keep reports on pass, failed checks, and interruption where
possible. Do not swallow the original exit code. Record a configuration revision
and environment with each run; the result schema does not embed the full
configuration or all provenance. See [CLI CI usage](cli.md#ci-usage).

Workload headers/bodies are omitted from result JSON and HTML but present in
editor exports and run bundles. URLs, selected telemetry values, and environment
metadata can still be sensitive. Apply [artifact access rules](../SECURITY.md).

## Troubleshooting

| Symptom | Investigation |
| --- | --- |
| Validate succeeds but requests fail | Validation sends no traffic. Check the target from the generator/each worker, then inspect transport errors and timeouts |
| HTTPS certificate failure | Check certificate trust and hostname. Workload insecure is explicit; worker and telemetry verification remain independent |
| Fast p95 with missed demand | Missed arrivals have no latency sample. Inspect concurrency, capacity/scheduler drops, start delay, and generator resources |
| Started HTTP rate differs from journey target | Targets count whole journeys; request throughput counts logical HTTP attempts |
| Start run disabled | Find the active dashboard run in Live run; wait for completion or stop it intentionally |
| Worker unavailable after commit | Keep the incomplete report, inspect leases/control connectivity, and account for unknown worker traffic |
| Telemetry blank or incomplete | Verify exact full-label selectors, exporter format/trust/authentication, scrape errors, and counter resets; gaps are not zeros |
| Download asks for reload | Preserve unsaved editor changes, then reload. Restarted or other local dashboard sessions can replace the download cookie |
| Restored workspace will not start | Preserve files and read the startup error; inspect record shape/size and recover from a known-good copy |

For UI-specific steps, use **User guide → Troubleshooting** and the
[dashboard guide](dashboard.md). For enterprise adoption, review the
[readiness gates](readiness.md).
