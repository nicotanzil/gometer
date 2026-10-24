# Security model and reporting

[Project overview](README.md) · [Documentation](docs/README.md)

This document describes the current implementation and its operating boundaries.
GoMeter is a local single-user tool with optional authenticated workers. It has
not been independently security-audited and does not provide a multi-tenant
service security model or a security certification.

## Trust boundaries

| Connection or storage | Current protection | Operator responsibility |
| --- | --- | --- |
| Dashboard | Loopback by default; optional remote listener requires an exact HTTPS public origin, Host/Origin checks, startup API token, and restrictive content security policy | Keep loopback for local use. For remote access, require a VPN or authenticated, allowlisted private ingress and keep the data directory private |
| Workload HTTPS | System certificate roots and hostname verification by default | Choose trusted targets and protect supplied workload credentials |
| Insecure workload HTTPS | Explicit opt-in, saved and labeled in results | Use only where skipping target identity checks is appropriate; it applies to all workload requests in that run |
| Worker control | Shared token; verified HTTPS by default; remote HTTP requires independent worker/coordinator startup opt-ins; control redirects rejected | Restrict worker network access, distribute credentials securely, and operate only trusted workers |
| Target-server telemetry | Separate client, verified HTTPS, optional environment bearer token, bounded parsing | Scope exporter access and select the intended endpoint and metrics |
| Local workspace and reports | Atomic writes; new workspace directories 0700 and files 0600 on permission-based filesystems | Protect the account, existing directories, downloaded files, backups, and CI artifact access |

Local API tokens prevent cross-site request use; they do not isolate the dashboard
from processes running under the same local account. The dashboard has no user login,
RBAC, SSO, tenant isolation, audit log, or encryption at rest. The optional
`-public-origin` setting preserves exact Host/Origin checks behind a TLS-terminating
ingress; it does not authenticate users. Remote access must be restricted by a VPN
or an ingress that enforces authentication and an allowlist. Do not expose the
dashboard directly to the public internet. Use the separately secured worker
interface for remote load generation.

The worker token authorizes reserving, starting, stopping, and reading jobs.
A worker receives workload configuration, including headers, bodies, and data.
Possession of the token permits instructing that worker to send network traffic;
network and egress controls remain necessary in your environment.

With separate HTTP opt-ins (`GOMETER_WORKER_ALLOW_HTTP` on the worker and
`GOMETER_ALLOW_HTTP_WORKERS` on the coordinator), the worker token and
test configuration travel without application TLS. Network observers can obtain
the token and supplied workload secrets; a network attacker can alter control
traffic and results. HTTPS ingress for the dashboard does not protect this hop.
Restrict plaintext control to an explicitly accepted, protected network. These
permissions default to false and cannot be granted by a scenario or imported
bundle. Invalid TLS configuration never triggers HTTP fallback. Worker results
are trusted measurements subject to consistency validation, not attested data.

See [worker setup](docs/workers.md) and [HTTPS options](docs/https.md) for trust
configuration. Workload `insecure` does not disable worker or telemetry verification.
Workload custom CA, client-certificate, and selectable TLS-version options remain
planned; do not infer support from the control-plane CA flags.

## Team backend implementation boundary

The optional PostgreSQL repository and Keycloak-compatible OIDC verifier are
backend foundations. They are not connected to dashboard routes or browser
sessions. The `workspace` administration commands do not turn the current
personal dashboard into an authenticated team service. Do not expose existing
raw-configuration or download endpoints to teammates on the assumption that
repository role checks protect those endpoints.

The foundation verifies remote database TLS and OIDC transport, uses exact
issuer/subject identities and current persisted memberships, and commits
repository mutations with metadata-only audit events. Managed secret references,
allowed destinations, browser session/CSRF protection, and the shared executor
remain pending. LDAP federation stays in Keycloak. See
[team backend foundations](docs/team-workspace.md) for setup and verification limits.

## Credentials and exported data

| Artifact | Data that may be sensitive |
| --- | --- |
| Saved scenarios, editor JSON, workspace runs, run bundles | Supplied headers, bodies, data rows, variables, target URLs, and selected telemetry metadata |
| Result JSON and HTML | Target URLs, scenario/step labels, measured errors, environment/resource metadata, and selected telemetry values |
| Dashboard in the browser | Editable workload configuration and displayed results |
| JMX migration drafts and imported saved scenarios | Retained original XML and explicitly supplied property values, including any credentials in the source |
| Worker process | Workload configuration and current job results in memory |

Result reports omit configured request headers and bodies. Individual response
bodies are not retained in reports; assertion/extraction captures are bounded
and temporary. These rules are not general-purpose redaction: sensitive values
can still be placed in URLs, names, configuration, or telemetry selections.

`GOMETER_WORKER_TOKEN` and `GOMETER_TELEMETRY_TOKEN` are private runtime options;
their values do not enter saved configuration, reports, or browser payloads.
Use your environment's secret-injection mechanism. Do not commit credentials,
workspace files, private keys, or unreviewed artifacts. Downloads and copied
archives inherit the destination's security controls, not workspace file modes.

Prefer headers to URL query parameters for workload authentication. Workload URL
userinfo is rejected. Review and sanitize artifacts before sharing them.

## Input and execution controls

Configuration parsing rejects unknown fields and trailing JSON, and validation
sends no requests. A valid configuration can still target a sensitive or expensive
endpoint when started. Inspect imported configurations before execution.

[JMX analysis](docs/jmx-import.md) is bounded and rejects DTDs/external entities.
It does not evaluate scripts, read referenced files or local JMeter properties,
or contact targets. Unknown execution semantics block runnable conversion.
Blocked drafts have no native configuration and remain outside the runnable
Scenario library. Conversion reanalyzes the original XML on the server.

Body limits, bounded histograms, bounded correlation/session data, worker leases,
and parser limits constrain specific resources. They do not guarantee the
process cannot exhaust resources at an extreme configured workload. Use bounded
rates, timeouts, concurrency, and external resource limits appropriate to the
[operating environment](docs/operations.md).

## Reporting a security concern

Do not include secrets, private target addresses, or an exploit that exposes
other users in a public issue. Use GitHub's **Report a vulnerability** option in
the repository's **Security** tab if the maintainer has enabled private reporting.
If it is unavailable, request a private contact method from the maintainer using
a public issue that contains no sensitive technical details.

A useful private report includes the affected source revision or binary version,
a minimal sanitized reproduction, impact, operating system, and relevant
configuration. No response-time SLA or long-term security maintenance window is
currently published. An adoption release needs an owner, a working private
reporting channel, and a supported-version policy; see [readiness](docs/readiness.md).
