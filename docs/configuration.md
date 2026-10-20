# Configuration reference

[Documentation](README.md) · [Project overview](../README.md)

JSON files are strict: unknown fields, invalid values, and trailing documents
are rejected before sending traffic. `gometer validate` performs no requests.
Command-line flags override the corresponding scenario fields.

```json
{
  "name": "API stress test",
  "url": "http://127.0.0.1:8787/work",
  "method": "POST",
  "headers": { "Content-Type": "application/json" },
  "body": "{\"item\":\"example\"}",
  "mode": "rate",
  "max_in_flight": 64,
  "timeout": "1s",
  "stages": [
    { "name": "Baseline", "duration": "2s", "rate": 20 },
    { "name": "Overload", "duration": "3s", "rate": 300 },
    { "name": "Recovery", "duration": "2s", "rate": 20 }
  ],
  "thresholds": {
    "p95": "150ms",
    "max_error_percent": 1,
    "max_drop_percent": 1
  }
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `url` | required | Absolute HTTP or HTTPS URL |
| `name` | `HTTP test` | Label in reports |
| `method` | `GET` | HTTP method |
| `headers` | empty | Request headers; `Host` is supported |
| `body` | empty | Literal request body, up to 1 MiB |
| `mode` | `users` | `users` or `rate` |
| `duration` | `10s` for duration-only runs | Admission window; omitted or empty means no time limit when `iterations` is positive; ignored with stages |
| `iterations` | `0` | Per-user request or journey attempts, 1–1000000; users mode only. `0` means duration only |
| `ramp_up` | `0s` effective | Users mode: stagger starts at ramp × user index ÷ users; 0–24h |
| `startup_delay` | `0s` effective | Users mode: wait before the load duration starts; 0–24h |
| `on_error` | `next_iteration` | `next_iteration`, `continue`, `stop_user`, `stop_test`, or `stop_test_now`; nondefault actions require users mode; see below |
| `vus` | `10` | Concurrent users in users mode |
| `rate` | `0` | Integer arrivals/second in rate mode; use this or stages |
| `max_in_flight` | `100` | Maximum concurrent workload arrivals in rate mode |
| `insecure` | `false` | Skip certificate and hostname verification for workload HTTPS only; see [HTTPS](https.md) |
| `arrival_distribution` | `constant` | `constant` or seeded stratified `jitter`; rate mode only |
| `seed` | `1` | Seed for repeatable arrival jitter |
| `journey_timeout` | `30s` | Deadline for an entire journey, including verification and pauses |
| `journey_thresholds` | zero journey errors | Whole-journey criteria; see [journeys](scenarios.md) |
| `scenarios` | empty | Weighted multi-step journeys; rate and concurrency then refer to journeys |
| `variables` | empty | Initial template variables for journeys |
| `cookies` | `true` when omitted | Journey cookie jars; `false` disables cookie storage and automatic sending. Imported plans without a Cookie Manager use `false`; single-request tests do not use a jar |
| `timeout` | `5s` | Per-request timeout including connection and full response body |
| `think_time` | `0s` | Pause between each user's requests or journeys; users mode only |
| `max_response_bytes` | `10485760` | Response-body read cap; exceeding it fails the attempt |
| `diagnostics` | `true` | Generator resource sampling and HTTP transport timing; `false` disables both |
| `workers` | empty | Worker control origins for distributed rate-mode tests; empty runs locally |
| `telemetry` | absent | Opt-in selected scalar series from a Prometheus text exporter; see [telemetry.md](telemetry.md) |
| `success_statuses` | all `2xx` | Optional explicit list of successful status codes |
| `stages` | empty | Sequential constant-rate phases; rate `0` creates a pause |
| `thresholds.p95` | disabled | Maximum p95 full-response latency |
| `thresholds.max_error_percent` | `0` | Maximum percentage of completed requests that failed |
| `thresholds.max_drop_percent` | `0` | Maximum percentage of scheduled arrivals that were dropped |
| `thresholds.min_started_rps` | disabled | Minimum dispatched requests per second over the load window |

For journeys, see [application journeys](scenarios.md) for step templates,
cookie behavior, response assertions, weighted mixes, data rows, and thresholds.
The configured `rate` and concurrency cap apply to journeys when `scenarios`
is present. Request throughput is reported separately.

Setting an error or drop threshold to JSON `null` disables that check. No
completed requests, or an interrupted run, always fails. Use `run -help` for
available flags. `-out ''` and `-html ''` disable their respective artifacts.

## Configuration rules

- A configuration is one JSON object, at most 1 MiB. Comments and unknown fields
  are rejected. Omitted fields retain their defaults.
- Duration strings use Go duration syntax, such as `250ms`, `2s`, and `1m30s`.
  Load windows and request timeouts must be positive and no longer than 24 hours;
  total phase duration cannot exceed 24 hours.
- The parser accepts up to 100 phases. In rate mode, at least one phase must have
  a positive rate. These validation limits are not tested capacity guarantees.
- With `scenarios` present, put method and body on steps. Top-level headers and
  success statuses provide defaults. Steps stay on the base URL origin.
- Request error and drop checks default to zero tolerance. Set percentage checks
  to `null` to disable them. An omitted p95 is disabled. A blank p95 CLI override
  (`-p95 ''`) disables a configured p95 check.
- Explicit CLI flags take precedence over loaded fields. `-rate` removes phases;
  `-duration` alone does not. See [CLI precedence](cli.md#configuration-precedence).

Validate before starting:

```sh
./bin/gometer validate -config examples/smoke.json
```

Validation does not contact targets, workers, or telemetry exporters. It checks
syntax and executable configuration, not reachability or credentials at runtime.

## Related references

- [Application journeys](scenarios.md): steps, variables, inline data, sessions,
  assertions, weighted mixes, and whole-journey thresholds.
- [Distributed workers](workers.md): control origins, tokens, CA trust, quotas,
  leases, and incomplete results.
- [Target-server telemetry](telemetry.md): exporter selection and credentials.
- [Measurement definitions](measurements.md): arrival accounting and percentiles.

## Finite iterations per user

```json
{
  "url": "http://127.0.0.1:8787/work",
  "mode": "users",
  "vus": 10,
  "iterations": 5
}
```

Each user executes five attempts, for 50 total. With `scenarios`, an iteration is
one selected journey, including its nested steps and loops. A failed attempt
consumes an iteration; the next journey starts with fresh variables and the same
user's cookie jar. Single-request workloads have no cookie jar.

Omitting `duration` (or setting it to `""`) makes the run count-only. Supplying
`duration: "1m"` stops new iterations at the count or time limit, whichever comes
first. Existing duration-only configurations keep their behavior. Think time
separates attempts; there is no pause after the final iteration. Request and
journey deadlines still apply. **Stop** and process cancellation interrupt active
work. Count-only runs can exceed 24 hours in total; each configured timeout or
pause remains bounded by its own validation limits.

## User startup schedule

Try [the user ramp example](../examples/users-ramp.json) in the builder's example
menu, or run `./bin/gometer run -config examples/users-ramp.json` with the demo
server running. It makes 12 requests: four users, three attempts each.

With four users, `ramp_up: "4s"`, and `startup_delay: "2s"`, planned starts are
**2s, 3s, 4s, and 5s** from run start. The first user starts after the startup
wait; the last starts at `startup_delay + ramp_up × (vus - 1) / vus`. A single
user starts immediately after the wait regardless of ramp length. Finished
finite users are not replaced, so active users may be below the configured cap.

Startup waiting is outside `duration`. Ramp time is inside it. If duration is
shorter than the ramp, later users may never start; their future iterations are
not recorded as dropped arrivals. Count-only runs wait for every scheduled user
to finish unless canceled. A duration boundary stops new users/iterations and
lets admitted journeys drain. Stop cancels pending starts and active work.
Scheduling can be late; first-attempt start-delay measurements include lateness
relative to the planned user start. These controls do not apply to rate mode.

## Failure actions

In users mode, set `on_error` in JSON or choose **After a failed request** in
**Traffic options**. The action applies to request transport/status errors,
extraction and assertion failures, and request-template failures.

| Action | Current journey | Future iterations |
| --- | --- | --- |
| `next_iteration` (default) | Skip remaining steps | This user starts the next iteration after its configured pacing |
| `continue` | Attempt later steps and loop iterations; the journey remains failed | This user continues normally |
| `stop_user` | Skip remaining steps | End this user; other users and pending ramp starts continue |
| `stop_test` | End the failing journey; other admitted journeys finish under their deadlines | Stop all new users and iterations |
| `stop_test_now` | Cancel active requests and journey pauses | Stop all new users and iterations, including pending ramp starts |

Single-request tests have no remaining steps, so `continue` behaves like the
default. Every attempted request or journey consumes one outer iteration,
including failures. Finished or stopped users are not replaced.

Failed response verification publishes no newly extracted values; prior values
remain available. A dependent request can therefore fail template expansion
without sending HTTP traffic. Pauses after successful steps do not run after
failed steps. A journey deadline, cancellation, condition error, or flow safety
limit always aborts the current journey. `stop_user` also ends the user after a
journey-level error; manual cancellation is recorded as interruption instead.

A user stopped on error fails the mandatory `users_stopped_on_error` check,
even when request and journey error thresholds are disabled. This avoids passing
a run that silently abandoned future work. Once all users finish or stop, the
run ends early with `users_stopped`; a duration limit or manual Stop can end it
first. No errors are erased by choosing a different action.

Test-wide actions also apply to journey-level errors, such as a condition error
or journey deadline. An error-triggered test stop fails the mandatory
`test_stopped_on_error` check even with error thresholds disabled. The run is
partial: it records `test_stopped` or `test_stopped_now`, rather than pretending
that every configured iteration ran. The failing journey skips its remaining
steps in either policy. Graceful stopping lets other admitted journeys finish,
including later HTTP requests and pauses. Immediate stopping cancels workload
contexts; it cannot roll back a request the server already received.

During graceful drain the load duration and throughput denominator freeze at
admission closure; drain time remains in total wall time. Pressing **Stop** or
Ctrl+C can interrupt a graceful drain and is recorded as manual interruption,
while retaining the earlier error-stop flag. Diagnostics and telemetry retain
their existing bounded final collection behavior.

Rate/distributed workloads retain the default action. Restarting a user and
independent group stops are not implemented yet.
