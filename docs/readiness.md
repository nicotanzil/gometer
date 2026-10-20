# Enterprise readiness

[Documentation](README.md) · [Project overview](../README.md)

A professional project needs verifiable behavior, clear operating boundaries, and
an accountable release process. This page separates safeguards in the current
implementation from adoption gates that still need evidence or owner decisions.
It is not a certification or a claim that every enterprise requirement is met.

## Current foundation and remaining gates

| Area | Implemented foundation | Remaining adoption gate |
| --- | --- | --- |
| Measurement correctness | Explicit demand accounting, bounded histograms, request/journey separation, worker-state validation | Independent repeatability, accuracy, instrumentation-overhead, and comparative benchmarks for supported workloads |
| Verification | Vet, race-enabled regression fixtures, example validation, executable builds | Required CI/branch checks, a documented supported-platform matrix, and evidence tied to the exact release revision |
| Security boundaries | Loopback dashboard by default; optional private-ingress origin checks, strict parsing, default TLS verification, authenticated remote workers, separate exporter credentials | Threat-model/security review, a working private reporting channel, supported-version policy, and remediation ownership |
| Team access | Local single-user account boundary | An authenticated team service would need a separately designed login/SSO, RBAC, audit, and isolation model |
| Recovery | Atomic workspace/report writes, graceful cancellation, worker leases, explicit abandoned/incomplete results | Practiced backup/restore, retention ownership, durable worker recovery requirements, and failure drills |
| Release integrity | Version can be injected at build time; native builds | Release from a verified source snapshot; documented artifact checksums/provenance, signing policy, and release ownership |
| Compatibility | Versioned result schemas, import consistency checks, documented limitations | Explicit compatibility/migration policy and conformance fixtures for supported external formats/protocols |
| Operational support | Runbooks, exit codes, diagnostics, telemetry coverage | Owned deployment/resource limits, escalation path, support expectations, and workload-specific capacity evidence |
| Licensing | License status is explicit in the contribution guide | Maintainer-selected license and distribution/contribution policy; no LICENSE file currently exists |

The current repository does not contain a CI workflow. The `make check` target
provides local verification; running it once is not equivalent to enforced release
checks. Screenshots and the bundled demo demonstrate behavior, not production
capacity or certification.

## Adoption checklist

Before approving a release for your organization's performance pipeline:

- Identify the exact source revision and binary version. Build and test the same
  source snapshot used for release artifacts; do not combine a tag with unrelated
  working-tree changes.
- Run vet, race-enabled tests, builds, and affected examples on the supported
  platforms. Archive the outcomes with release evidence.
- Validate the real workload against a controlled target. Repeat it, verify
  offered/delivered accounting, and establish generator resource headroom with
  diagnostics and telemetry costs accounted for.
- Review network access, workload credentials, worker tokens/TLS, exporter trust,
  artifact sensitivity, retention, and private vulnerability reporting.
- Perform backup/restore and interruption/worker-loss exercises. Confirm how
  incomplete, abandoned, and missing-telemetry states affect pipeline decisions.
- Assign release, security-response, maintenance, and operational owners. Record
  license and compatibility decisions and publish the supported scope.

These are release gates, not a request to disable current safeguards or expose
the local dashboard. Different organizations may require additional controls.

## Product scope

Bundled JMeter parity is a roadmap objective. The
[capability inventory](jmeter-parity.md) labels available, partial, and missing
families; it is not a component-by-component compatibility percentage. Distributed
users, additional protocol adapters, recording, JMX/JTL compatibility, extension
APIs, and statistical regression verdicts remain development work.

Track implementation and validated milestones in [ROADMAP.md](../ROADMAP.md).
Operational procedures are in [operations](operations.md), and security boundaries
and reporting are in [SECURITY.md](../SECURITY.md).
