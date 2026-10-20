# Generator diagnostics and HTTP timing

[Documentation](README.md) · [Project overview](../README.md)

Diagnostics are enabled by default in the CLI and dashboard. They augment
request and journey results without changing their count equations, thresholds,
or latency definitions.

```sh
./bin/gometer run -config examples/smoke.json
./bin/gometer run -config examples/smoke.json -diagnostics=false
```

Set `"diagnostics": false` in a configuration to disable both resource sampling
and HTTP tracing. An explicit CLI flag overrides the configuration. The dashboard
form and JSON editor share this setting. Schema 3 results omit diagnostic fields
when disabled. The dashboard also accepts schema 2 bundles, which contain no
diagnostics.

In distributed runs, HTTP histograms merge across workers, while resource
summaries stay per worker process. No fleet CPU average or memory peak is
invented. See [workers.md](workers.md) for that measurement contract.

Target-server telemetry is a separate opt-in collector; disabling diagnostics
does not disable configured exporter scrapes. Exporter observations never enter
HTTP timing/count populations. See [telemetry.md](telemetry.md).

## Generator process measurements

The sampler reads the current GoMeter process at run start, about once per second,
and after admitted work drains and optional final telemetry collection finishes. It also runs for quiet CLI tests. Setup before
the initial sample and report writing after the final sample are excluded.

| Measurement | Definition |
| --- | --- |
| CPU seconds | Process user plus system CPU consumed since the initial sample |
| Interval CPU % | CPU seconds since the previous sample / wall seconds × 100 |
| Average CPU % | CPU seconds over the whole sampled window / window seconds × 100 |
| Peak CPU % | Largest sampled interval percentage; 100% means one core |
| Go heap bytes | `runtime.MemStats.HeapAlloc`; includes objects awaiting GC |
| Go runtime bytes | `runtime.MemStats.Sys`; memory obtained by the Go runtime |
| Process peak RSS | OS resident-memory high-water mark over the process lifetime |
| Goroutines | Current process goroutine count |
| GC cycles and pause time | Cumulative differences from the initial sample |
| Logical CPUs / GOMAXPROCS | Runtime CPU count / configured Go execution parallelism |

CPU percentages can exceed 100% when several cores are active. They are not
normalized to machine capacity and do not account for container CPU quotas.
The final interval can be very short when drain finishes just after a periodic
sample; its percentage is sensitive to small amounts of work and sampler cost.
CPU and peak RSS are available on macOS and Linux through `getrusage`. On other
platforms, or when the OS query fails, availability flags are false and the UI
shows **Unavailable**. Go runtime measurements remain available.

Heap, runtime memory, and goroutine peaks are maxima of the samples. Brief spikes
between samples can be missed. Peak RSS is a process lifetime value, so a later
dashboard run can inherit an earlier run's peak. Neither Go heap nor runtime
memory is current RSS. All measurements include unrelated work in the same
process, such as dashboard polling and retained history. Separate CLI processes
provide clearer isolation for future resource benchmarks.

The generator panel also shows the existing workload arrival start-delay p95,
capacity misses, and scheduler misses. These signals can help investigate pacing
problems; no automatic saturation verdict is made. They do not measure target
CPU, target memory, or target processing time.

## Observed HTTP intervals

Each row includes its own sample count, mean, p50, p95, and maximum. Durations are
stored in the same bounded logarithmic histograms as request latency. A row with
zero samples displays a dash, not a measured zero.

| Interval | Start → end |
| --- | --- |
| Connection acquisition | `GetConn` → `GotConn`; includes pool waiting and new connection setup when they occur |
| DNS lookup | `DNSStart` → `DNSDone` |
| TCP connect | `ConnectStart` → matching `ConnectDone` |
| TLS handshake | `TLSHandshakeStart` → `TLSHandshakeDone` |
| Request write | `GotConn` → `WroteRequest` |
| First header byte | Logical attempt start → first `GotFirstResponseByte` |
| Response body read | Client returns response headers → body read completes or fails |

These intervals **overlap** and have different populations. Do not add their
means or quantiles to reconstruct request latency. Reused connections normally
have no DNS, TCP, or TLS event; literal-IP targets normally have no DNS lookup.
The first header byte can be an informational response and is not server
processing time. Body reading excludes journey assertions and extraction.

Completed failed DNS, TCP, TLS, write, and body-read events are included. A
timeout before headers has no first-byte or body-read sample. Pending events at
attempt completion count as unfinished without receiving a duration. Trace
callbacks that arrive after network completion are ignored, so canceled dials
cannot change an already finalized report. Only up to 16 simultaneous pending
dials per attempt retain timestamps; unmatched or excess events are counted.

Connections report acquisition requests, acquired and reused connections, idle
reuses, DNS/dial/TLS failures, write completions and failures, response protocols,
and body-read failures. A response-size limit failure is a failed body read.
Exceeding the smaller correlation capture limit after reading the whole body
does not turn that completed network read into a body-read failure.

Go's transport can retry certain attempts or try more than one address. Event
counts can therefore exceed logical request attempts. HTTP/2 reuse can represent
several simultaneous streams on one connection. Proxies can add setup work;
these timings describe the transport events that Go exposes.

## Output, overhead, and limits

JSON includes `generator` summaries and optional `http` aggregates on the run,
each dispatch phase, and each journey step. Journey-only aggregates have no HTTP
timings. The HTML report and dashboard display overall diagnostics. Dashboard
timeline points include resource samples; the existing 900-point retention bound
still applies. Final aggregates cover the entire run.

Tracing adds callbacks, locks, and histogram work. Each request collector that
observes a transport event allocates seven histograms, roughly 224 KiB of bucket
storage. Unvisited phases, skipped steps, and journey-only collectors allocate
none of these HTTP histograms. Pending trace data is bounded per active attempt;
individual response timings are not retained. Resource sampling uses
`runtime.ReadMemStats`, which also has an observation cost.

Instrumentation overhead has not yet been benchmarked. Use the disabled setting
to measure it under an identical workload. Comparisons flag differing diagnostic
instrumentation and generator environments when known, but they do not establish
a statistically valid regression or superiority over another tool.
