# Run your first test

[Documentation](README.md) · [Project overview](../README.md)

This guide uses the included demo on your own machine. Build the executables,
start the target, then choose the dashboard or CLI. Opening the dashboard alone
does not start the demo or send test traffic.

## 1. Build from source

Prerequisites: a source checkout and Go 1.27.3 or newer. Go modules download the
PostgreSQL and OIDC dependencies. Make is optional; there is no frontend installation
or asset build step. A compiled GoMeter binary needs neither Go nor Node.js.

From the repository root on macOS or Linux:

```sh
go build -o bin/gometer ./cmd/gometer
go build -o bin/gometer-demo ./cmd/gometer-demo
./bin/gometer version
```

Or run `make build` to build both. On Windows, use PowerShell:

```powershell
go build -o bin/gometer.exe ./cmd/gometer
go build -o bin/gometer-demo.exe ./cmd/gometer-demo
.\bin\gometer.exe version
```

The following shell examples use macOS/Linux paths. On Windows, invoke
`.\bin\gometer.exe` and `.\bin\gometer-demo.exe` instead.

## 2. Start a local target

In a separate terminal, leave this process running:

```sh
./bin/gometer-demo
```

The demo listens on `http://127.0.0.1:8787`. Its `/work` endpoint uses a bounded
pool of 8 concurrent slots, with 50ms of simulated work per admitted request.
These defaults explain the examples; they are not performance guarantees.

## 3. Follow the dashboard walkthrough

In another terminal:

```sh
./bin/gometer ui
```

1. Open the printed URL, normally `http://127.0.0.1:8790`.
2. Choose **User guide → Your first test**.
3. Expand **Load the practice configuration**, read the draft-replacement notice,
   then choose **Load practice test into builder**.
4. Review the target and preview: GET to the local demo, 5 requests/s, 2 seconds,
   concurrency cap 2, and 10 planned arrivals.
5. Choose **Validate**. This checks settings without contacting the target.
6. Choose **Start run** when ready to send traffic. The app opens **Live run**.

The practice configuration also sets a 1s timeout, a 200ms request-p95 limit,
and zero allowed errors or missed arrivals. A healthy default demo normally
completes all 10 requests and passes these checks. Scheduling and latency depend
on your machine; read the actual result rather than assuming a pass.

![First-test walkthrough in the GoMeter app](images/user-guide.jpg)

## 4. Understand and keep the result

Look at the verdict and individual checks, then the four main measurements:

| Measurement | What to check |
| --- | --- |
| HTTP attempts / second | Whether actual started traffic matches your intended load |
| Request latency · p95 | Whether measured attempts finished within your chosen latency limit |
| Request errors | How many completed attempts failed, and why |
| Missed workload demand | How much scheduled work the generator could not start |

Admitted work can finish after the load window; this is drain. **Stop run**
cancels active work and saves a partial result. Closing the browser leaves a run
active while the dashboard process continues.

Use **Save scenario** in the builder to keep reusable settings. Finished runs are
saved automatically in history. Result JSON and HTML are reports; **Run + timeline**
is the portable bundle that can be imported back into history. Review files before
sharing: bundles contain the supplied configuration, including headers and bodies.

Read [measurement definitions](measurements.md) for exact populations and units.

## CLI alternative

Keep the demo running, then use a supplied configuration:

```sh
./bin/gometer validate -config examples/smoke.json
./bin/gometer run -config examples/smoke.json
```

This starter differs from the walkthrough: it offers 20 requests/s for 2 seconds,
with a concurrency cap of 16, a 150ms p95 limit, and at least 19 started requests/s.
It plans 40 arrivals. JSON and HTML report paths are printed after the run.

For explicit artifact paths:

```sh
./bin/gometer run -config examples/smoke.json \
  -out results/first-run.json -html results/first-run.html
```

Explicit paths replace previous artifacts at those paths. Use unique names when
retaining multiple runs. See [CLI reference](cli.md) for overrides and exit codes.

## If something goes wrong

| Symptom | Next step |
| --- | --- |
| Connection refused on port 8787 | Start `gometer-demo` in another terminal and check its printed address |
| Dashboard port is already in use | Reuse the existing dashboard or start `gometer ui -listen 127.0.0.1:8791 -data work/another-workspace` |
| The dashboard says to use its printed URL | Use the exact loopback IP and port; `localhost` is not interchangeable with `127.0.0.1` for its host check |
| Validation passes but the run fails | Validation does not check reachability. Read the run's errors and individual acceptance checks |
| The stress starter returns exit 1 | It deliberately overloads the default demo; failed error checks are expected |

Multiple dashboard processes need separate data directories. Each browser tab
should be reloaded before downloading if another local dashboard replaced its
session cookie. The [operations guide](operations.md) covers recovery and retention.

## Next steps

Increase one variable at a time and save a baseline before stressing your own test
environment. Use the [dashboard guide](dashboard.md) for comparisons, the
[journey guide](scenarios.md) for login and checkout flows, and the
[security model](../SECURITY.md) before adding credentials or remote workers.
