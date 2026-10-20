# gmeter

**HTTP load testing with visible offered demand.**

Build tests in a local browser dashboard or run them from the CLI. gmeter measures
planned arrivals, delivered traffic, response errors, and missed demand separately,
so a fast response time cannot hide load the generator never sent.

Written in Go. The compiled binary embeds the dashboard and starter configurations;
using it requires no Java, Node.js, or external frontend assets.

[Get started](docs/getting-started.md) · [Documentation](docs/README.md) ·
[CLI reference](docs/cli.md) · [Roadmap](ROADMAP.md)

## Start locally

From a source checkout, install **Go 1.24 or newer** and build both executables:

```sh
go build -o bin/gmeter ./cmd/gmeter
go build -o bin/gmeter-demo ./cmd/gmeter-demo
```

On Windows, build with `bin/gmeter.exe` and `bin/gmeter-demo.exe` instead.
If you have Make installed, `make build` builds both executables.

Start the demo in one terminal:

```sh
./bin/gmeter-demo
```

Start the dashboard in another:

```sh
./bin/gmeter ui
```

Open the URL printed by gmeter, normally **http://127.0.0.1:8790**, and choose
**User guide → Your first test**. Loading its practice configuration and validating
it send no workload traffic. **Start run** sends the requests. Saved scenarios and
run history live in `.gmeter/` by default.

For a multi-step workflow, load **Authenticated checkout journeys** to open the
**Journey canvas**. Edit requests and response checks, trace extracted variables,
reorder steps, and build executable nested branches and loops visually. See the [canvas guide](docs/dashboard.md#edit-a-journey-on-the-flow-canvas).

![Journey canvas with execution order, extracted-data links, and a request inspector](docs/images/journey-canvas.png)

Load **Branches and loops** to explore a nested example. [Flow syntax and limits](docs/scenarios.md#branches-and-loops).

![Executable branches and loops on the journey canvas](docs/images/journey-flow.png)

For prepared input, import a [CSV dataset](docs/datasets.md) under **Journey settings & data**. Map columns to variables, choose shared or per-user rows, and recycle or finish users normally at EOF. This increment supports local users-mode journeys.

Coming from JMeter? Choose **Import JMeter JMX** in the builder. Review component
compatibility, supply properties, preview the mapped journey, then save a native
scenario or a separate migration draft. This first converter accepts a limited
set of JMeter 5.6.3 HTTP plans; unsupported behavior blocks runnable conversion.
See the [JMX import guide](docs/jmx-import.md) for the exact scope and runtime differences.

<details>
<summary>JMeter import compatibility review</summary>

![Guided JMX component review with runtime policies and property inputs](docs/images/jmx-import.jpg)

</details>

Prefer the CLI? With the demo still running:

```sh
./bin/gmeter validate -config examples/smoke.json
./bin/gmeter run -config examples/smoke.json
```

The smoke example plans 40 arrivals over two seconds. A healthy default demo
normally passes; inspect the output and checks on your machine. JSON and
self-contained HTML reports are written under `results/`. See the
[step-by-step guide](docs/getting-started.md) for setup, expected results, and fixes.

## See the app

Watch delivered traffic, latency, errors, and missed arrivals, then inspect the
final acceptance checks. Generator diagnostics and optional server telemetry
provide additional context.

![gmeter walkthrough: validate a local test, watch live measurements, and review completed results](docs/images/walkthrough.gif)

<details>
<summary>Completed run screenshot</summary>

![gmeter completed local demo run showing measurements, checks, and traffic charts](docs/images/run-results.jpg)

</details>

<details>
<summary>Test builder and new-user walkthrough</summary>

Create a test with request settings, traffic phases, and explicit pass criteria.
The preview explains what the configuration plans to send before you start it.

![gmeter test builder with request, traffic, pass criteria, and planned demand](docs/images/test-builder.jpg)

The in-app **User guide** explains a first local test, settings, results, and
troubleshooting. Contextual help returns to the builder without replacing a draft.

![gmeter user guide with first-test instructions and practice settings](docs/images/user-guide.jpg)

</details>

Screenshots and the recording use the bundled local demo. Their measurements
illustrate the app; they are not benchmarks. [Capture notes](docs/images/README.md).

## What you can do today

| Capability | Current support |
| --- | --- |
| HTTP workloads | HTTP/1.1 and HTTPS with HTTP/2 negotiation; methods, headers, bodies, status checks, pooled connections |
| Traffic models | Concurrent users with duration/iteration limits, ramp-up and startup delay; fixed arrival rate, sequential rate phases, zero-rate pauses, seeded jitter |
| Failure actions | Users can start the next iteration, continue the journey while retaining errors, stop only the failed user, or stop the test gracefully/immediately; [behavior and limits](docs/configuration.md#failure-actions) |
| Application journeys | Nested if/else, fixed-count and bounded while loops, isolated cookies, inline data, variables, extraction, assertions, weighted scenario mixes |
| Journey canvas | Visual request/control editing, path changes, dependency links, pauses, undo, structure preview, and observed request/decision/iteration counts |
| Acceptance checks | Request and journey latency, error rates, missed demand, minimum started throughput |
| Authoring | Guided builder, starter configurations, Advanced JSON, saved scenarios, first-test guide |
| JMeter migration | Guided JMX analysis, limited HTTP conversion, property inputs, original source retention, and separate migration drafts |
| Results | Live charts, history, two-run comparison, JSON/HTML reports, portable run bundles |
| Generator diagnostics | Process resources, arrival delay, connection reuse, and HTTP transport timings |
| Target-server telemetry | Selected scalar gauges/counters from a Prometheus text exporter, with explicit gaps and resets |
| Distributed execution | Authenticated workers, shared rate schedule, fixed concurrency quotas, incomplete-result detection |

Full bundled JMeter parity is an active [development program](docs/jmeter-parity.md).
Distributed users, browser JavaScript execution, and non-HTTP protocol adapters
are not available. Recording and more authentication
options remain on the roadmap. No benchmark establishes a performance advantage
against JMeter.

## Understand the measurements

- **Offered demand:** scheduled arrivals are accounted for as started work or
  capacity/scheduler misses. Missed work has no response-latency sample.
- **Requests and journeys:** a three-step journey can produce three HTTP attempts.
  Journey arrival targets and request throughput use different units.
- **Response latency:** includes the full response body and completed failed
  attempts. Percentiles are bounded-histogram estimates.
- **A pass:** the run completed and its configured checks passed. Review errors,
  missed demand, latency, and execution conditions together.

Read the [measurement definitions](docs/measurements.md) before using results for
capacity decisions or comparisons.

## Automate a test

Run the compiled binary in CI and retain its reports even when checks fail:

```sh
./bin/gmeter run -config examples/smoke.json -quiet \
  -out results/ci.json -html results/ci.html
```

| Exit code | Meaning |
| --- | --- |
| `0` | Completed; all acceptance checks passed |
| `1` | Acceptance checks failed, including incomplete distributed results |
| `2` | Input, setup/control, or report output error |
| `130` | Interrupted; partial reports attempted |

The example needs the demo running. Stress and telemetry examples intentionally
overload it and normally return `1`. See [CLI and CI usage](docs/cli.md#ci-usage)
for preserving the exit code and artifacts.

## Documentation

| Task | Guide |
| --- | --- |
| Run your first test | [Getting started](docs/getting-started.md) |
| Create, save, compare, and export tests | [Browser dashboard](docs/dashboard.md) |
| Configure requests, traffic, and checks | [Configuration reference](docs/configuration.md) |
| Script tests and use environment options | [CLI reference](docs/cli.md) |
| Build authenticated multi-step workflows | [Application journeys](docs/scenarios.md) |
| Interpret latency, demand, and verdicts | [Measurements](docs/measurements.md) and [diagnostics](docs/diagnostics.md) |
| Run worker fleets and collect server metrics | [Workers](docs/workers.md) and [telemetry](docs/telemetry.md) |
| Deploy a private dashboard and worker pods | [Kubernetes](docs/kubernetes.md) |
| Operate, back up, and troubleshoot | [Operations](docs/operations.md) |
| Review security and adoption requirements | [Security](SECURITY.md), [HTTPS](docs/https.md), and [enterprise readiness](docs/readiness.md) |
| Contribute or understand the code | [Contributing](CONTRIBUTING.md) and [architecture](docs/architecture.md) |

## Project status

Active development. The dashboard is a single-user application that listens on
loopback by default; optional remote access requires a private authenticated
ingress. Remote workers use a separate authenticated control plane. Enterprise adoption should
follow the documented [readiness gates](docs/readiness.md), including release,
security, operational, and independent measurement validation.

The repository currently has no LICENSE file. Selecting a license is an open
maintainer decision, tracked in the readiness guide.

To check a development checkout, run `make check build` (JavaScript checks, vet,
race-enabled tests, and both builds). See [CONTRIBUTING.md](CONTRIBUTING.md) for prerequisites and equivalent commands.
