# Distributed workers

[Documentation](README.md) · [Project overview](../README.md)

Authenticated worker processes and a coordinator support
**rate-mode** tests. Single requests, journeys, weighted mixes, seeded jitter,
and sequential phases use the same engine. Users mode currently runs locally.

## Local setup

Build with `make build`. Generate one token and securely provide the same value
to each worker and coordinator through `GOMETER_WORKER_TOKEN`. It must contain
32–512 ASCII characters without whitespace. You can generate a value once with
`openssl rand -hex 32`; do not generate different tokens for each process.
Worker-control tokens are never stored in configurations, reports, or exported
bundles. Supplied workload headers, bodies, and data remain part of saved
configuration; see [security](../SECURITY.md).

With that environment variable set in each terminal:

```sh
# Terminal 1: target service
./bin/gometer-demo

# Terminal 2: first worker
./bin/gometer worker -name worker-1 -listen 127.0.0.1:8801

# Terminal 3: second worker
./bin/gometer worker -name worker-2 -listen 127.0.0.1:8802

# Terminal 4: coordinator
./bin/gometer run -config examples/smoke.json \
  -workers http://127.0.0.1:8801,http://127.0.0.1:8802

# Or use the browser dashboard as the coordinator.
./bin/gometer ui
```

In the dashboard, enter worker origins in **Workers**, one per line, or load
**Distributed local smoke test**. Leave the field blank for local execution.
Validation contacts neither workers nor targets. Starting performs preflight
and reservation. The dashboard must inherit its token when it starts; changing
another shell's environment cannot update an already running process.

Worker origins can also be part of a configuration:

```json
{
  "name": "Fleet smoke",
  "url": "http://127.0.0.1:8787/work",
  "mode": "rate",
  "rate": 20,
  "duration": "2s",
  "max_in_flight": 16,
  "workers": ["http://127.0.0.1:8801", "http://127.0.0.1:8802"]
}
```

`-workers` overrides that list; `-workers ''` selects local execution. A pool has
1–32 unique origins and needs at least one concurrency slot per worker. The
target must be reachable from every worker. A target on `127.0.0.1` refers to
each worker's own machine. Multiple processes on one machine verify coordination;
they are not a scaling benchmark.

## Remote control

Workers bind an explicit IP. Loopback is the default. Non-loopback listeners
require a TLS certificate and private key. Control always requires the token;
browser-origin control requests are rejected.

```sh
./bin/gometer worker -name worker-1 -listen 0.0.0.0:8801 \
  -tls-cert /secure/worker.pem -tls-key /secure/worker-key.pem

./bin/gometer run -config scenario.json -workers https://worker.example:8801 \
  -worker-ca /secure/worker-ca.pem
```

Certificates must verify for the origin's hostname/IP. System certificate roots
are the default; `-worker-ca` adds a PEM CA. `GOMETER_WORKER_CA` sets the default
for the CLI and dashboard. Certificate verification cannot be disabled. HTTP
control is allowed only on literal loopback IPs. Authorization headers never
follow redirects. Worker control connects directly; target requests retain their
usual proxy environment support.

Only connect to workers you operate and trust. The token permits reserving,
starting, canceling, and reading jobs. Network controls and certificate/token
distribution remain your setup's responsibility. Each worker receives the target
configuration, including headers, bodies, and data rows.

For Kubernetes, use a fixed-replica StatefulSet and a headless Service so each
worker has a stable per-pod DNS name. Enter each worker's HTTPS pod DNS name in
the coordinator's Workers field. Do not point multiple worker entries at one
load-balanced Service: the coordinator treats each origin as a distinct worker.
Mount the shared control token and TLS keypair from Secrets, and mount the CA
certificate in the dashboard pod for `GOMETER_WORKER_CA`. Restrict worker ingress
to the dashboard and worker egress to the intended test targets.

## Shared schedule and capacity

The rate is a **fleet total**. Global arrival ordinal `k` belongs to worker
`k % worker_count`, continuing across phases. Workers calculate timing from the
original rate and ordinal; they do not each replay the full test. Pause phases
have no slots. Fractional-phase counts, weighted selection, journey IDs, data-row
ordinals, and seeded jitter remain part of the same offered schedule. All steps
of a journey stay with its worker and session.

Concurrency is divided evenly into fixed quotas, with remainder slots assigned
to the first workers in list order. A cap of 5 across two workers becomes 3 and
2. Free capacity elsewhere cannot absorb a worker's missed arrivals. There is no
backlog, work stealing, or automatic load retry. Capacity and scheduler misses
remain distinct.

The coordinator probes each worker three times and estimates clock offset using
the shortest round trip. A best probe over 500 ms is rejected before target
traffic. All workers must pass preflight and reserve a job before committing a
shared start about three seconds ahead. Workers convert that timestamp to their
local monotonic clock. Probe round trips and offsets are reported. This cannot
remove asymmetric delay, clock changes between probe and start, or OS jitter;
it is not precise clock synchronization.

## Availability and cancellation

A worker accepts one reserved or active job. Uncommitted reservations expire
after 30 seconds. Repeating a commit with the same timestamp does not replay
traffic; a different commit for that job is rejected.

Polling about twice per second renews a **10-second lease**. If the coordinator
disappears, its worker cancels admission and active requests when that lease
expires. Ctrl+C, dashboard Stop, and shutdown also cancel worker jobs and attempt
to collect drained partial results. Results stay in memory until another
reservation; worker restart loses them.

Preflight/reservation failure sends no target traffic and returns a control/input
error. Failure after commit cancels the other jobs and creates a failed report.
Three consecutive control failures mark a worker unreachable. Cleanup is bounded;
an unreachable worker can continue until its last renewed lease expires.

Missing final results mean **unknown traffic**. Global counts and latency
populations include only workers with final results. No requests or measured
drops are invented. Reports show assigned arrivals, worker status, and planned
arrivals that remain unreported. That remainder can include future arrivals
excluded by cancellation and traffic whose outcome is unknown; it is not a new
measured drop category.

Incomplete results always fail `run_completed` and `worker_results_complete`,
even if received workers have zero errors or drops. Exit 1 covers failed checks
or incomplete control completion, exit 2 covers preflight/input/output errors,
and user interruption returns 130. Load is never automatically reassigned.

## Measurements and output

Global histograms merge bucket counts, minima/maxima, and sums. Means and
percentiles are recomputed; worker p95 values are never averaged. Thresholds run
once on the global request, journey, and scenario populations. Local worker
checks cannot override that verdict. Raw states are validated for bounded shape,
count equations, histogram consistency, and phase/scenario/step accounting.

Throughput uses the shared admission window. Total wall time also includes
target drain and result collection. Live values come from different observation
times and can be stale; they are the latest received measurements. Final results
replace them after available workers finish.

CPU, heap, GC, and platform metadata remain per worker process. There is no fleet
CPU percentage or heap peak. Request/phase in-flight high-water marks sum worker
peaks as an upper bound, since those peaks can occur at different times. HTTP
populations keep the rules in [diagnostics.md](diagnostics.md).

Sequential distributed reports use **schema 4**, with `distribution` and worker
summaries, or **schema 5** with target-server telemetry. Branch/loop flows use
**schema 6** in either case. Sequential local reports use schema 3 or schema 5
with telemetry. The dashboard imports schemas 2–11 and
preserves worker lists/results across restart and export. Timeline points retain
compact worker counters within the 900-point limit. Control responses are capped
at 32 MiB; bundles retain the 16 MiB limit. Raw histogram transfer states are
excluded from user reports and bundles.

Optional [target-server telemetry](telemetry.md) is collected once by the
coordinator near shared admission and after worker drain/collection. Workers
receive neither the telemetry configuration nor its exporter credentials. The
endpoint must be reachable from the coordinator. Failed/missing scrapes stay
unknown and do not change the load checks; worker loss still fails the run.

This first version does not implement distributed users mode, worker discovery,
heterogeneous quotas, dynamic balancing, durable worker recovery, per-worker
resource timeline charts, or precise clock synchronization. Independent scaling
and measurement benchmarks remain ahead.

Journeys with branches or loops use result schema 6, including worker and
telemetry results. Existing sequential configurations retain their earlier schemas.
Use current binaries on all workers; older workers reject new flow fields.
See [branches and loops](scenarios.md#branches-and-loops).
