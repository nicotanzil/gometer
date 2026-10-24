# Shared team workspace implementation

Status: local implementation in progress, 24 October 2026. No deployment.

The user selected Kubernetes with PostgreSQL and existing company SSO through
Keycloak OIDC with LDAP User Federation. Shared workload credentials and sensitive data will use managed secret
references; resolved values must stay concealed from previews, copies and exports.
Preserve personal/CLI workflows and migrate explicitly with backup and
rollback. Do not reinterpret the current shared startup token as a user identity.
The reviewed interaction handoff is currently at
`/private/tmp/gometer-team-uiux/DESIGN-HANDOFF.md`.

## First slice

Implemented locally:

- Tests library/navigation; the existing builder is a child, and Runs retains
  current history. New test, Edit test, Use a copy and Resume draft are real.
- Persisted definition revisions, atomic stale-edit rejection, independent copy
  identity and source revision. Legacy records read as revision 1 without a
  startup rewrite. Failed persistence does not publish an advanced revision.
- Server-projected previews omit paths, query values, headers, bodies, variables
  and datasets. These are personal-mode previews, not a claim of team authorization.
- Current Tests switches block unfinished drafts. Multi-draft recovery and
  field reconciliation are still pending. Conflict saving retains local edits;
  Save editor as a new scenario preserves them independently.

## Remaining implementation sequence

1. PostgreSQL schema/migrations and persisted workspace membership; OIDC login,
   sessions, issuer/subject-based identity and server role checks. Explicitly
   bootstrap administrators; no browser-selected identity or implicit trust in
   proxy headers. Establish sensitive-data and destination policies before
   exposing current raw configuration/download paths to team members.
2. Team-attributed Tests library with creator/last editor, All/My filters,
   teammate search, related run identity and safe recovery/conflict workflows.
3. Durable submissions with actor-scoped idempotency, FIFO sequence and a single
   atomic workspace execution slot. Keep definition, draft, submission and chosen
   results independent. Queue wait must not enter engine measurements.
4. Dedicated executor using the current engine/coordinator. Recheck current
   membership and destination policy; preserve ordered immutable worker intent.
   Never skip selected workers, replay uncertain work or fall back locally.
5. Queue/Team UI and ownership-aware queued cancellation versus active Stop.
   Hold the slot during preparation, draining, collection, unknown execution and
   failed result persistence. Verify real restart and concurrency races.
6. Kubernetes manifests, explicit migration/backup/restore, and operational
   evidence. Local fixture results do not prove company SSO or cluster deployment.

No queue limits, secret/export policy, bootstrap identities or recovery override
policy are approved merely because examples appeared in the mockup. The user
authorized committing the completed milestones on 24 October 2026. Pushes,
publication and deployment remain outside the current authorization.

## Backend foundation slice — 24 October 2026

Implemented locally: isolated PostgreSQL repository/schema and explicit CLI
migrate/check/init commands; checksummed transactional migrations; persisted
issuer/subject membership and per-operation role checks; creator/editor metadata,
My tests/teammate search repository operations, conditional revisions, independent
copies and payload-free audit. Keycloak-compatible code exchange uses verified
HTTPS, RS256 ID tokens, state/nonce, S256 PKCE, exact audience/authorized-party and
expiry checks; `workspace oidc-check` validates discovery only.

The dashboard is not switched to this database and has no team login/session
adapter yet. No real Keycloak, LDAP or Kubernetes deployment was exercised.
Managed secret resolution and all queue/executor work remain pending. See
[the implementation boundary and setup guide](../team-workspace.md).

Source builds now use Go 1.27.3, matching the container toolchain. The new
PostgreSQL/OIDC dependencies use patched versions. On 24 October 2026,
`govulncheck` reported no known reachable vulnerabilities for this source on the
local macOS/ARM64 platform. That result is not a security audit or deployment
acceptance, and does not cover other build platforms.

Verification completed with the retained Go 1.27.3 toolchain and dependencies:
`make check build` passed JavaScript checks, vet, all Go race tests and both
binary builds; `make test-workspace` passed against its isolated PostgreSQL 18
fixture. No application database, live identity provider or shared dashboard
process was used. The completed foundation is being committed locally before
browser sign-in and session development.

## Next slice

Connect browser sign-in to the verified OIDC component with browser-bound login
transactions, durable server-side sessions, secure cookies, CSRF checks and
explicit logout. Recheck persisted membership on authenticated operations.
Keep personal routes outside team mode until managed secret references and
read/export/destination policies protect the shared API boundary. Follow with
the team Tests adapter, then durable submissions and the separate executor.
