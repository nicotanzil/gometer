# CLI reference

[Documentation](README.md) · [Project overview](../README.md)

Run commands from the source checkout with `./bin/gmeter`, or use the installed
binary's path. Flags use Go's command-line parser; specify booleans as
`-diagnostics=false`, rather than `-diagnostics false`. Options are placed after
the command and before any positional arguments; these commands accept no
positional arguments.

## Commands

| Command | Purpose |
| --- | --- |
| `gmeter help` | Show command overview |
| `gmeter version` | Print the binary's version |
| `gmeter validate -config path.json` | Parse and validate one configuration without network requests |
| `gmeter run [flags]` | Validate, execute a workload, and write configured reports |
| `gmeter ui [flags]` | Start the dashboard; loopback by default |
| `gmeter worker [flags]` | Start an authenticated rate-mode worker |

Each command accepts `-help`. Build with `make build` or the Go commands in
[getting started](getting-started.md).

## Configuration precedence

A run starts with defaults, loads `-config` if supplied, and applies explicitly
provided flags. Unspecified flags do not overwrite configured values.

- `-rate` selects rate mode unless `-mode` is explicit, and removes configured
  phases. It sets workload arrivals/s: journeys/s when scenarios are present.
- `-duration` alone does not replace configured phases. Their durations still
  determine the load window. To replace them, supply `-rate` as well.
- `-vus` changes the user count; it does not select users mode. When overriding
  a rate configuration, select `-mode users -rate 0` to clear phases/rate and
  use `-arrival-distribution constant` if it configured jitter.
- `-url` changes only the workload target, not `telemetry.url`.
- `-insecure=false` restores workload certificate verification even if JSON
  enabled insecure requests. `-workers ''` removes the worker pool.
- `-telemetry=false` disables configured telemetry. `-telemetry=true` does not
  create an exporter configuration. Diagnostics and telemetry are independent.

Use JSON for headers, success status lists, phases, journey steps, and criteria
that need `null` to disable them. See [configuration defaults](configuration.md).

## Run flags

Defaults below apply without a configuration. An explicitly loaded value takes
precedence over an unspecified flag.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-config` | empty | Strict JSON configuration file |
| `-csv` | empty | Embed a CSV file in an existing local users-mode journey; also available to `validate` |
| `-csv-scenario` | only journey | Select the journey by name; required with multiple journeys |
| `-csv-columns` | header names | JSON object mapping CSV headers to variables; an empty value ignores a column |
| `-csv-sharing` | `shared` | `shared` or `per_user`; requires `-csv` |
| `-csv-eof` | `recycle` | `recycle` or normal `stop_user`; requires `-csv` |
| `-csv-delimiter` | comma | One CSV delimiter character; requires `-csv` |
| `-url` | required unless configured | Workload HTTP(S) target |
| `-name` | `HTTP test` | Report label |
| `-method` | `GET` | HTTP method for a single-request workload |
| `-body` | empty | Literal request body; configure headers in JSON |
| `-mode` | `users` | `users` or `rate` |
| `-duration` | `10s` | Admission window; configured phases take precedence |
| `-iterations` | `0` | Iterations per user, users mode only. A positive explicit flag clears duration unless `-duration` is also supplied, including when overriding a file |
| `-ramp-up` | `0s` effective | Stagger virtual-user starts; users mode only; 0–24h |
| `-startup-delay` | `0s` effective | Wait before the load window; users mode only; 0–24h |
| `-on-error` | `next_iteration` effective | Users mode: `next_iteration`, `continue`, `stop_user`, `stop_test`, or `stop_test_now`; an explicit empty string resets the default |
| `-vus` | `10` | Concurrent users in users mode |
| `-rate` | `0` | Workload arrivals/s; explicitly setting it removes phases |
| `-max-in-flight` | `100` | Concurrent requests or whole journeys in rate mode |
| `-arrival-distribution` | `constant` | `constant` or stratified `jitter`; rate mode only |
| `-seed` | `1` | Repeatable arrival-jitter seed |
| `-timeout` | `5s` | Request timeout, including response body |
| `-think-time` | `0s` effective | Pause between a user's iterations |
| `-max-response-bytes` | `10485760` | Per-response read cap; exceeding it fails the attempt |
| `-insecure` | `false` | Skip workload HTTPS certificate/hostname checks |
| `-p95` | disabled | Maximum request p95, such as `200ms`; `''` disables it |
| `-max-error-percent` | `0` | Maximum failed/completed request percentage |
| `-max-drop-percent` | `0` | Maximum missed/scheduled workload percentage |
| `-min-started-rps` | disabled | Minimum dispatched HTTP attempts/s over the load window |
| `-journey-timeout` | `30s` | Whole-journey deadline, including pauses |
| `-journey-p95` | disabled | Maximum whole-journey p95; requires scenarios |
| `-diagnostics` | `true` | Generator resources and HTTP transport timing |
| `-workers` | empty | Comma-separated worker origins; rate mode only |
| `-worker-ca` | `GMETER_WORKER_CA` or system roots | Additional PEM CA for worker HTTPS |
| `-telemetry` | `true` | Collect telemetry if configured; false disables it |
| `-telemetry-ca` | `GMETER_TELEMETRY_CA` or system roots | Additional PEM CA for exporter HTTPS |
| `-quiet` | `false` | Disable progress messages; final summary still prints |
| `-out` | timestamped path under `results/` | JSON result path; `''` disables it |
| `-html` | timestamped path under `results/` | Standalone HTML path; `''` disables it |

Output paths, the input configuration path and the CSV input path must be distinct.
See [CSV datasets](datasets.md) for file requirements and examples. Explicit report
paths replace prior files atomically. Result JSON is not a reusable configuration
or an importable dashboard run bundle.

## Dashboard and worker flags

| Command | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `ui` | `-listen` | `127.0.0.1:8790` | Explicit IP and port; non-loopback requires `-public-origin` |
| `ui` | `-public-origin` | empty | Exact HTTPS origin for a trusted private ingress; no path prefixes |
| `ui` | `-data` | `.gmeter` | Saved scenarios and history directory |
| `worker` | `-listen` | `127.0.0.1:8801` | Explicit listen IP and port; non-loopback requires TLS |
| `worker` | `-name` | hostname, or `worker` | Worker display name |
| `worker` | `-tls-cert` | empty | PEM worker server certificate |
| `worker` | `-tls-key` | empty | PEM private key for that certificate |

Each dashboard process needs its own data directory. Workers accept one reserved
or active job at a time. See [dashboard](dashboard.md) and [workers](workers.md).

## Environment

| Variable | Read by | Purpose |
| --- | --- | --- |
| `GMETER_WORKER_TOKEN` | Worker, CLI coordinator, dashboard | Shared worker-control token; 32–512 printable ASCII characters without whitespace |
| `GMETER_WORKER_CA` | CLI coordinator, dashboard | Additional PEM CA file for worker HTTPS |
| `GMETER_TELEMETRY_TOKEN` | CLI, dashboard | Exporter bearer token when `bearer_auth` is enabled |
| `GMETER_TELEMETRY_CA` | CLI, dashboard | Additional PEM CA file for exporter HTTPS |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | Workload transport | Go's standard proxy environment behavior |

The dashboard captures credential/CA environment settings when it starts.
Worker control and telemetry connect directly; workload proxy settings do not
apply to them. Worker and telemetry tokens are not persisted in reports or
configuration. Supplied workload credentials in headers/bodies are part of saved
configuration and bundles; see [security](../SECURITY.md).

## Exit codes

| Code | Meaning | Recommended interpretation |
| --- | --- | --- |
| `0` | Completed and passed | Evaluate the configured checks and measurement coverage |
| `1` | Checks failed or worker results incomplete | Keep reports and inspect failed checks |
| `2` | Invalid input, setup/control failure, or output error | Inspect stderr; do not treat it as an application performance verdict |
| `130` | Interrupted | Partial reports were attempted; do not treat them as a completed baseline |

An output failure can happen after traffic. Reports are attempted even when
acceptance checks fail. User interruption returns 130 even if an artifact could
not be written. Run the compiled binary in automation: `go run` does not preserve
all executable exit codes.

## CI usage

Provide a reachable test service, validate the configuration, run the built
binary, and retain the output even on a failed check. This POSIX shell example
uses the local demo, which must already be running:

```sh
./bin/gmeter validate -config examples/smoke.json || exit $?

gmeter_exit=0
./bin/gmeter run -config examples/smoke.json -quiet \
  -out results/ci.json -html results/ci.html || gmeter_exit=$?

# Configure your CI runner to retain results/ even when this step fails.
printf 'gmeter exit code: %s\n' "$gmeter_exit"
exit "$gmeter_exit"
```

Run validation as a required step that stops on failure; CI runners differ in
shell error behavior. Use a unique results directory per run and avoid parallel
jobs writing the same artifacts or workspace. Record the binary version, source
revision, configuration, target version, environment, and generator resources.
See [operations](operations.md) for retention and [readiness](readiness.md) for
release acceptance gates.

For a finite run with 10 users making five attempts each:

```sh
./bin/gmeter run -url http://127.0.0.1:8787/work -vus 10 -iterations 5
```

Add `-duration 1m` to stop at either limit. One iteration means one request or,
with a journey configuration, one selected journey attempt. Errors consume an
iteration. The summary and artifacts report the stop reason.

Stagger four users across a four-second ramp after waiting two seconds:

```sh
./bin/gmeter run -url http://127.0.0.1:8787/work -vus 4 -iterations 3 \
  -startup-delay 2s -ramp-up 4s
```

Starts are planned at 2, 3, 4, and 5 seconds from run start. Add `-duration 10s`
to cap admission at 10 seconds after the startup wait. Explicit `0s` overrides
disable configured ramps or startup delays. The load-rate denominator excludes
startup waiting; full elapsed time includes startup, load, drain, and collection.
