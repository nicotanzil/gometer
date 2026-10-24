# CLI reference

[Documentation](README.md) · [Project overview](../README.md)

Run commands from the source checkout with `./bin/gometer`, or use the installed
binary's path. Flags use Go's command-line parser; specify booleans as
`-diagnostics=false`, rather than `-diagnostics false`. Options are placed after
the command and before any positional arguments; these commands accept no
positional arguments.

## Commands

| Command | Purpose |
| --- | --- |
| `gometer help` | Show command overview |
| `gometer version` | Print the binary's version |
| `gometer validate -config path.json` | Parse and validate one configuration without network requests |
| `gometer run [flags]` | Validate, execute a workload, and write configured reports |
| `gometer ui [flags]` | Start the dashboard; loopback by default |
| `gometer worker [flags]` | Start an authenticated rate-mode worker |
| `gometer workspace migrate` | Apply the PostgreSQL workspace schema explicitly |
| `gometer workspace check` | Check PostgreSQL schema compatibility without migrating |
| `gometer workspace init [flags]` | Bootstrap one workspace and its first administrator |
| `gometer workspace oidc-check [flags]` | Validate Keycloak discovery and TLS without signing in |

Each command accepts `-help`; startup options identify their environment variable.
Build with `make build` or the Go commands in
[getting started](getting-started.md).

## Configuration precedence

Application startup settings use this precedence: **explicit flag → environment
variable → built-in default**. An explicitly empty flag or variable is still a
value; settings that require a value reject it. Startup variables are
command-specific, so the dashboard and worker can use different listen addresses.
Shared credentials and CA settings retain the scopes listed in the environment
table below.

For `run` and `validate`, `-config` overrides `GOMETER_CONFIG`. A run starts with
defaults, loads that file if selected, and applies explicitly provided workload
flags. Unspecified flags do not overwrite configured values. Workload fields
remain in JSON or flags; startup variables do not change saved scenarios.

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
| `-config` | `GOMETER_CONFIG` or empty | Strict JSON configuration file |
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
| `-allow-http-workers` | `false` | Permit remote HTTP worker origins; also available to `validate` and `ui`; startup permission, never a workload field |
| `-workers` | empty | Comma-separated worker origins; rate mode only |
| `-worker-ca` | `GOMETER_WORKER_CA` or system roots | Additional PEM CA for worker HTTPS |
| `-telemetry` | `true` | Collect telemetry if configured; false disables it |
| `-telemetry-ca` | `GOMETER_TELEMETRY_CA` or system roots | Additional PEM CA for exporter HTTPS |
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
| `ui` | `-data` | `.gometer` | Saved scenarios and history directory |
| `ui` | `-allow-http-workers` | `false` | Permit remote HTTP worker control; show a startup warning |
| `worker` | `-listen` | `127.0.0.1:8801` | Explicit listen IP and port; non-loopback requires TLS or `-allow-http` |
| `worker` | `-allow-http` | `false` | Permit a non-loopback HTTP listener; token still required |
| `worker` | `-name` | hostname, or `worker` | Worker display name |
| `worker` | `-tls-cert` | empty | PEM worker server certificate |
| `worker` | `-tls-key` | empty | PEM private key for that certificate |

Defaults in the table are built-in values; environment settings override them.
See the [environment table](#environment) for the variable mapped to each flag.

Each dashboard process needs its own data directory. Workers accept one reserved
or active job at a time. See [dashboard](dashboard.md) and [workers](workers.md).

## Workspace administration

These commands provide the team backend foundation. They do not enable browser
SSO, change dashboard storage, import personal JSON files, or execute workloads.
See [team workspace setup](team-workspace.md) for the current implementation boundary.

`migrate`, `check` and `init` require `GOMETER_DATABASE_URL`. The database URL is
environment-only to keep credentials out of command arguments. Remote PostgreSQL
connections require verified TLS for every configured host. Run `migrate` before
`check` or `init`; migration history and checksums are validated.

| Command | Flag | Environment / default | Purpose |
| --- | --- | --- | --- |
| `init` | `-id` | `GOMETER_WORKSPACE_ID` / generated | Workspace ID: 32 lowercase hexadecimal characters |
| `init` | `-name` | `GOMETER_WORKSPACE_NAME` / `Team workspace` | Workspace display name |
| `init`, `oidc-check` | `-issuer` | `GOMETER_OIDC_ISSUER` / required | Exact HTTPS Keycloak realm issuer |
| `init` | `-subject` | required flag | Initial administrator's exact OIDC subject, not LDAP username |
| `init` | `-display-name` | required flag | Initial administrator's display name |
| `oidc-check` | `-client-id` | `GOMETER_OIDC_CLIENT_ID` / required | Confidential OIDC client ID |
| `oidc-check` | `-redirect-uri` | `GOMETER_OIDC_REDIRECT_URI` / required | Exact registered HTTPS callback URI; browser adapter is pending |
| `oidc-check` | `-oidc-ca` | `GOMETER_OIDC_CA` / system trust | Additional PEM CA certificate file |

`init` is an explicit database-owner operation. It records the supplied identity;
it does not authenticate it. Reusing an initialized workspace ID is rejected.
Without an ID, each invocation creates another workspace.

`oidc-check` requires the environment-only `GOMETER_OIDC_CLIENT_SECRET`, but no
database. Successful discovery does not verify client credentials, callback
registration, LDAP federation or an actual sign-in. LDAP configuration remains in
Keycloak. Non-secret flags override environment settings, including empty values.

## Environment

These startup variables describe the current source. The released `0.6.0` image
predates the new `GOMETER_UI_*`, worker listen/name/TLS, and `GOMETER_CONFIG`
settings; build a current-source image for environment-based startup. The current
prefix is `GOMETER_*`; the older `GMETER_*` names are not aliases.

| Variable | Read by | Purpose |
| --- | --- | --- |
| `GOMETER_UI_LISTEN` | Dashboard | `-listen`; default `127.0.0.1:8790` |
| `GOMETER_UI_PUBLIC_ORIGIN` | Dashboard | `-public-origin`; default empty; exact HTTPS origin for a private ingress |
| `GOMETER_UI_DATA` | Dashboard | `-data`; default `.gometer`; relative paths use the process working directory |
| `GOMETER_WORKER_LISTEN` | Worker | `-listen`; default `127.0.0.1:8801` |
| `GOMETER_WORKER_ALLOW_HTTP` | Worker | `-allow-http`; default `false`; permit a non-loopback HTTP listener |
| `GOMETER_ALLOW_HTTP_WORKERS` | Run, validate, dashboard | `-allow-http-workers`; default `false`; independently permit remote HTTP worker origins |
| `GOMETER_WORKER_NAME` | Worker | `-name`; default hostname, or `worker` |
| `GOMETER_WORKER_TLS_CERT` | Worker | `-tls-cert`; PEM server certificate path |
| `GOMETER_WORKER_TLS_KEY` | Worker | `-tls-key`; PEM private key path |
| `GOMETER_CONFIG` | Run, validate | `-config`; JSON workload configuration path |
| `GOMETER_WORKER_TOKEN` | Worker, CLI coordinator, dashboard | Shared worker-control token; 32–512 printable ASCII characters without whitespace |
| `GOMETER_WORKER_CA` | CLI coordinator, dashboard | Additional PEM CA file for worker HTTPS |
| `GOMETER_TELEMETRY_TOKEN` | CLI, dashboard | Exporter bearer token when `bearer_auth` is enabled |
| `GOMETER_TELEMETRY_CA` | CLI, dashboard | Additional PEM CA file for exporter HTTPS |
| `GOMETER_DATABASE_URL` | Workspace migrate/check/init | PostgreSQL connection URL; environment-only, never printed |
| `GOMETER_WORKSPACE_ID` | Workspace init | Existing empty workspace ID, or generate one when omitted |
| `GOMETER_WORKSPACE_NAME` | Workspace init | Workspace name; default `Team workspace` |
| `GOMETER_OIDC_ISSUER` | Workspace init/oidc-check | Exact HTTPS Keycloak realm issuer |
| `GOMETER_OIDC_CLIENT_ID` | Workspace oidc-check | Confidential client ID |
| `GOMETER_OIDC_CLIENT_SECRET` | Workspace oidc-check | Server-side credential; environment-only |
| `GOMETER_OIDC_REDIRECT_URI` | Workspace oidc-check | Exact future HTTPS callback URI |
| `GOMETER_OIDC_CA` | Workspace oidc-check | Optional additional PEM CA for Keycloak TLS |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | Workload transport | Go's standard proxy environment behavior |

Start the dashboard without configuration flags:

```sh
GOMETER_UI_LISTEN=127.0.0.1:8792 \
GOMETER_UI_DATA=/path/to/private/workspace \
./bin/gometer ui
```

Choose a configuration file once for validation and execution:

```sh
export GOMETER_CONFIG=examples/smoke.json
./bin/gometer validate
./bin/gometer run
```

Explicit flags win even when an environment value is invalid. Use `-config ''`
to clear an inherited file selection for `run`; `validate` requires a nonempty
configuration path. GoMeter reads process environment variables; it does not
load `.env` files or expand shell variables inside their values. Existing
listener, public-origin, and TLS checks also apply to environment settings.
Boolean variables accept Go boolean values such as `true` or `false`; empty or
malformed values fail startup unless an explicit flag overrides them. Set
`-allow-http=false` or `-allow-http-workers=false` to disable an inherited opt-in.

Remote HTTP control needs independent worker and coordinator permission. It sends
the worker token and test configuration without application TLS. The dashboard
shows a startup-permission warning and labels HTTP worker configurations/results.
Saved/imported scenarios and run bundles cannot enable these startup permissions.
Use a current-source build for the HTTP opt-ins; earlier releases lack them. See
[optional HTTP control](workers.md#optional-remote-http-control) for setup.

`GOMETER_UI_PUBLIC_ORIGIN` configures the browser origin behind an ingress; the
dashboard itself serves HTTP. It does not need a server certificate when NGINX
terminates HTTPS. Worker token/CA settings can be omitted until remote workers
are used. `GOMETER_CONFIG` does not load a dashboard scenario, and no environment
variable registers worker URLs; configure those in the builder or scenario.
See [Kubernetes deployment](kubernetes.md) for container settings, mounted PEM
paths, and dashboard-only versus worker-fleet templates.

Startup environment settings are read when the command starts; changing them
requires a restart. The dashboard also captures credential/CA settings at startup.
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
./bin/gometer validate -config examples/smoke.json || exit $?

gometer_exit=0
./bin/gometer run -config examples/smoke.json -quiet \
  -out results/ci.json -html results/ci.html || gometer_exit=$?

# Configure your CI runner to retain results/ even when this step fails.
printf 'gometer exit code: %s\n' "$gometer_exit"
exit "$gometer_exit"
```

Run validation as a required step that stops on failure; CI runners differ in
shell error behavior. Use a unique results directory per run and avoid parallel
jobs writing the same artifacts or workspace. Record the binary version, source
revision, configuration, target version, environment, and generator resources.
See [operations](operations.md) for retention and [readiness](readiness.md) for
release acceptance gates.

For a finite run with 10 users making five attempts each:

```sh
./bin/gometer run -url http://127.0.0.1:8787/work -vus 10 -iterations 5
```

Add `-duration 1m` to stop at either limit. One iteration means one request or,
with a journey configuration, one selected journey attempt. Errors consume an
iteration. The summary and artifacts report the stop reason.

Stagger four users across a four-second ramp after waiting two seconds:

```sh
./bin/gometer run -url http://127.0.0.1:8787/work -vus 4 -iterations 3 \
  -startup-delay 2s -ramp-up 4s
```

Starts are planned at 2, 3, 4, and 5 seconds from run start. Add `-duration 10s`
to cap admission at 10 seconds after the startup wait. Explicit `0s` overrides
disable configured ramps or startup delays. The load-rate denominator excludes
startup waiting; full elapsed time includes startup, load, drain, and collection.

## Workspace and settings

The application is **GoMeter**; executables are `gometer` and `gometer-demo`.
Dashboard workspaces default to `.gometer/`. `-data` selects a different path.
Worker and telemetry configuration uses the `GOMETER_*` variables listed above.
Browser preferences, authentication headers and cookies, demo session cookies,
and demo telemetry metrics use the `gometer` name.
