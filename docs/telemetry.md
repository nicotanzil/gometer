# Target-server telemetry

[Documentation](README.md) · [Project overview](../README.md)

GoMeter can scrape selected scalar series from a server's Prometheus
text exporter while a test runs. This is opt-in and separate from generator
diagnostics. The CLI, dashboard, and distributed coordinator use the same
collector. A Prometheus server is not required.

Start the rebuilt `./bin/gometer-demo`, then run:

```sh
./bin/gometer run -config examples/telemetry.json
```

The example has baseline, overload, and recovery phases. Its intentional HTTP
503 responses normally produce exit 1. Server metrics show sampled active work,
capacity, Go heap, CPU time, and completed/rejected work counters. The demo's
`/metrics` requests do not count as workload requests. This illustrates behavior;
it is not a performance benchmark.

## Configuration

Add a `telemetry` object to any local users/rate scenario or distributed rate
scenario. Omit it, set it to `null`, or use `-telemetry=false` to disable collection.
`-diagnostics=false` does not disable explicitly configured server telemetry.

```json
{
  "name": "API with server metrics",
  "url": "http://127.0.0.1:8787/work",
  "mode": "rate",
  "rate": 20,
  "duration": "5s",
  "telemetry": {
    "url": "http://127.0.0.1:8787/metrics",
    "interval": "1s",
    "timeout": "1s",
    "metrics": [
      { "name": "gometer_demo_active_work", "kind": "gauge", "unit": "slots", "title": "Active server work" },
      { "name": "gometer_demo_work_requests_total", "labels": { "outcome": "rejected" }, "kind": "counter", "unit": "requests" }
    ]
  }
}
```

| Field | Default / limit | Meaning |
| --- | --- | --- |
| `url` | Required, at most 2048 bytes | HTTP(S) exporter endpoint without URL credentials, query, or fragment |
| `interval` | `1s`; 250ms–1m | Intended cadence; busy collection skips missed ticks |
| `timeout` | `1s`; 100ms–5s | Deadline for one complete scrape |
| `bearer_auth` | `false` | Send the private environment token to this exporter |
| `metrics` | 1–16 unique selectors | Scalar series to observe |
| `metrics[].name` | Required, at most 128 bytes | Legacy ASCII Prometheus metric name |
| `metrics[].kind` | Required | `gauge` or `counter` |
| `metrics[].labels` | Empty; at most 16 entries | Exact complete label set, not a subset or aggregation |
| `metrics[].unit` | Empty; at most 24 bytes | Display label; `bytes` displays as MiB |
| `metrics[].title` | Metric name; at most 80 bytes | Display name |

Label names use legacy ASCII syntax and are at most 128 bytes; values are UTF-8
and at most 256 bytes. Titles and units must contain no control characters.
Empty labels select only a series with no labels. There is no wildcard, PromQL,
label filtering, automatic series discovery, or aggregation across instances.
Exporter `TYPE` metadata must agree with the configured kind when present,
except for `untyped`. A `_bucket`, `_sum`, or `_count` series can be selected as a
scalar with the appropriate kind; full histogram and summary interpretation is
not implemented.

The accepted response is HTTP 200, uncompressed `text/plain`, with absent or
`0.0.4` version and absent or UTF-8 charset. The body must be UTF-8 with a final
newline. Bodies are capped at 1 MiB, headers at 32 KiB, and lines at 64 KiB.
Quoted UTF-8 metric names, OpenMetrics, exemplars, and explicit exporter
timestamps are unsupported. Timestamped selected values are unknown, since
the collector does not establish their freshness. Unselected metric families
are ignored. Malformed selected text fails the scrape; missing, duplicate,
non-finite, excessive (absolute value above `1e100`), negative-counter, or
type-mismatched readings are explicitly marked unknown.

These rules implement a bounded subset of the official
[Prometheus text format](https://prometheus.io/docs/instrumenting/exposition_formats/).
The [metric types documentation](https://prometheus.io/docs/concepts/metric_types/)
describes gauge and counter semantics.

## Credentials and transport

For authenticated exporters, set `bearer_auth: true` and provide
`GOMETER_TELEMETRY_TOKEN` to the CLI or dashboard process before startup. Tokens
must contain 1–4096 ASCII characters without whitespace. The token is a private
runtime option and is never stored in configurations, reports, workspace files,
or browser payloads. Workload headers, cookies, and worker credentials are not
used for exporter requests.

Authenticated remote exporters require HTTPS. Plaintext bearer authentication
is allowed only for literal loopback IPs, including `127.0.0.1` and `::1`.
TLS verifies system roots and the endpoint hostname. `-telemetry-ca /path/ca.pem`
adds a CA for the CLI; `GOMETER_TELEMETRY_CA` sets the CLI default and dashboard
startup setting. Certificate verification cannot be disabled. Redirects are
not followed and responses are not decompressed. Telemetry connects directly;
proxy environment variables continue to apply to workload requests only.

`gometer validate`, configuration saves, and bundle imports perform no network
requests and need no token or CA file. Starting a run checks local credential/CA
setup before workload traffic. Changing `-url` overrides only the workload URL;
edit `telemetry.url` separately when changing the exporter.

## Observation window and accounting

An asynchronous first scrape starts near workload admission. Periodic requests
never overlap or catch up in bursts. After admitted work drains, collection
cancels a pending periodic request and attempts one final scrape with its own
bounded timeout. This final observation is also attempted after user cancellation
if sampling began. Cancellation before the shared start records no observations
and performs no final scrape. Total wall time includes final collection; the
workload's load window, counts, latency populations, and throughput denominator
remain unchanged.

Each recorded observation includes its completion timestamp, elapsed time since
run start, scrape duration, values, and per-series issues. Timestamps describe
when collection finished, not an instrument's exact measurement time. Chart
positions and counter rates use elapsed time; UTC wall timestamps can move when
the system clock adjusts. Gauge
min/max are sampled extrema; means are unweighted sample means. Zeros are valid
measurements. Missing readings display as a dash and break the chart line.

Counter rate is the nonnegative difference divided by elapsed seconds between
two consecutive valid observations of that series. A falling counter records a
reset and leaves that interval's rate unknown. A missing reading breaks the
continuity; the next valid reading starts a new baseline. `observed_increase`
sums only known continuous differences. `mean_rate` divides that increase by
`covered_seconds`, not by the load window. No eligible pairs means neither an
increase nor a rate is available. Hidden resets and spikes between scrapes
cannot be detected, and the first scrape can miss early workload activity.
Exporter counters can include other traffic; their increases are not counts
of this test's requests.

`attempts` counts recorded scrape outcomes, including failures. `failed_scrapes`
counts request/body/format failures; `incomplete_scrapes` counts successful parses
with at least one unknown selected value. `canceled_scrapes` separately counts
a periodic request discarded during stopping; it is not an exporter outage or
a recorded observation. Complete scrape coverage requires at least one recorded
outcome and no failed/incomplete scrapes. It does not assert continuous server
visibility or server health. All series have `samples + missing = attempts`.

Exporter outages and missing readings do not alter load acceptance checks or
their exit code. They remain visible as incomplete telemetry. There are no
automatic target-health thresholds or saturation verdicts. Scraping adds observer
load to both exporter and coordinator; its overhead has not been benchmarked.

## Dashboard, workers, and portable reports

In **Target-server telemetry**, enter the endpoint, interval, timeout, selected
metrics JSON, and authentication setting. A blank endpoint disables collection.
The **Server telemetry: baseline to recovery** starter selects the demo's metrics.
The live panel shows the latest scrape and a selected gauge or counter-rate
chart. Finished views add whole-run statistics, resets, gaps, and coverage.

Distributed tests scrape once from the coordinator using its clock. Workers
receive neither telemetry configuration nor exporter credentials. The endpoint
must be reachable from the coordinator; `127.0.0.1` refers to that machine.
Telemetry observations do not establish per-worker target coverage.

Configured telemetry produces **schema 5** results for sequential tests, locally
or with workers. Branch/loop flows use **schema 6**, preserving telemetry.
Sequential runs without telemetry retain local schema 3 or distributed schema 4. The dashboard
accepts schemas 2–11 and validates observation shape, numeric bounds, time windows,
configuration, and accounting without contacting the exporter. Imports cannot
verify the original measurements. JSON, HTML, workspace persistence, and run
bundles preserve final telemetry summaries. Comparison flags differing telemetry
settings and remains descriptive.

The collector retains the latest 900 full scrape observations independently of
the dashboard's 900 workload points. Whole-run statistics retain all recorded
outcomes. Live dashboard polling can skip intermediate scrapes; the finished
chart uses the retained collector timeline. At 250ms, that timeline covers about
225 seconds. The existing 16 MiB bundle limit still applies.

Journeys with branches or loops use result schema 6, including worker and
telemetry results. Existing sequential configurations retain their earlier schemas.
Use current binaries on all workers; older workers reject new flow fields.
See [branches and loops](scenarios.md#branches-and-loops).
