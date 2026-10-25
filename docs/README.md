# GoMeter documentation

[Project overview](../README.md)

Start with a small local test, learn what its measurements mean, then add journeys,
workers, and server telemetry as needed. These guides describe the current source
checkout. Planned capabilities are identified separately in the roadmap.

## Learn the workflow

| Guide | What you will learn |
| --- | --- |
| [Getting started](getting-started.md) | Build, start the demo and dashboard, run a first test, and check its result |
| [Browser dashboard](dashboard.md) | Create tests, save scenarios, inspect live results, compare runs, and exchange files |
| [Measurement definitions](measurements.md) | Offered vs delivered demand, request/journey units, percentiles, and verdicts |
| [CLI reference](cli.md) | Commands, flags, configuration precedence, environment, exit codes, and CI |

The app also has a **User guide** in its sidebar, with a practice configuration,
contextual explanations, and troubleshooting.

## Configure a workload

| Guide | Scope |
| --- | --- |
| [Configuration reference](configuration.md) | Request settings, defaults, traffic phases, and acceptance checks |
| [Application journeys](scenarios.md) | Sessions, data rows, templates, extraction, assertions, and weighted mixes |
| [CSV datasets](datasets.md) | Import files, map variables, share rows and stop or recycle at EOF |
| [JMeter JMX import](jmx-import.md) | Guided compatibility review, supported HTTP mappings, and non-runnable migration drafts |
| [HTTPS](https.md) | Verified defaults, opt-in insecure workload requests, and separate trust settings |
| [Distributed workers](workers.md) | Named catalog, connection checks, tokens, TLS, shared schedules, quotas, and unknown traffic |
| [Team backend foundations](team-workspace.md) | PostgreSQL setup, separate Keycloak sign-in/session service, LDAP boundary and pending team Tests work |
| [Team Tests editor](team-editor.md) | Shared authoring, managed references, private drafts and conflict recovery |
| [Shared test documents](shared-tests.md) | Explicit public fields, managed bindings, safe authoring/copy/export and executor trust |
| [Managed secrets and team metadata](managed-secrets.md) | Reference catalog, mounted-file boundary, API access and pending execution integration |
| [Kubernetes deployment](kubernetes.md) | Dashboard-only or worker-fleet setup, startup variables, NGINX TLS, Secrets, and persistent state |
| [Generator diagnostics](diagnostics.md) | Process resources, connection reuse, HTTP timings, and instrumentation limits |
| [Target-server telemetry](telemetry.md) | Selected exporter series, authentication, gaps, counter resets, and coverage |

Starter files are in [examples/](../examples/). The dashboard embeds the same files;
its binary does not need the source tree at runtime.

## Operate and contribute

- [Operations](operations.md): run planning, artifacts, retention, workspace backup,
  shutdown, recovery, and troubleshooting.
- Private Kubernetes exposure uses a fixed HTTPS origin and an authenticated,
  allowlisted VPN/ingress; see [operations](operations.md) and [workers](workers.md).
- [Security](../SECURITY.md): trust boundaries, data handling, and reporting a concern.
- [Enterprise readiness](readiness.md): existing safeguards and outstanding adoption gates.
- [Architecture](architecture.md): components and execution/data flows.
- [Contributing](../CONTRIBUTING.md): development setup, meaningful verification,
  documentation, and review expectations.
- [JMeter coverage](jmeter-parity.md) and [roadmap](../ROADMAP.md): supported features,
  partial coverage, and delivery gates.

## App tour

**Create a test.** Set requests, traffic, and criteria, then review the planned demand.

![Test builder with workload configuration and demand preview](images/test-builder.jpg)

**Read a result.** Inspect measured traffic and checks before increasing the load.

![Completed demo run with traffic, latency, request errors, and missed demand](images/run-results.jpg)

**Learn in the app.** Follow the first-test walkthrough and return to the relevant field.

![User guide with the first-test walkthrough](images/user-guide.jpg)

All images use local demonstration data. [Capture and update instructions](images/README.md).
