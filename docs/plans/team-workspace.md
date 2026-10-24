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
process was used. The completed foundation is committed as `282f9a2`.

## Browser sign-in slice — 24 October 2026

Implemented locally, after commit `282f9a2`: `workspace serve` with a separate
sign-in screen and session-only HTTP boundary; browser-bound AES-GCM login
records consumed atomically from PostgreSQL; hashed durable session tokens;
secure host-only cookies; current membership checks; CSRF-protected local logout
and transactional audit. Disabling a member deletes their sessions. Migration
002 adds session tables without rewriting definitions or migration 001.

Verified: the PostgreSQL/Docker fixture suite, including real environment-based
CLI startup/shutdown, two-instance callback/session restoration, race/replay and
wrong-browser/state checks, role changes, unknown/disabled-account denial,
credential-safe output, audit rollback and upgrade preservation. Chromium
completed TLS-provider sign-in, secure-cookie checks, reload and sign-out; light
and dark layouts passed at four widths. The CLI test environment now preserves
only the guarded fixture URL, and the runner rejects silently skipped required
PostgreSQL cases. Project checks/builds and the local vulnerability scan passed.

The new sign-in slice is uncommitted. No real company Keycloak, LDAP or Kubernetes
acceptance was run. Personal dashboard APIs are not exposed by this service.
Local logout does not end Keycloak SSO; Keycloak/LDAP-only revocation is observed
at the next sign-in or local expiry. No refresh tokens are stored.

## Next slice

Define and enforce managed secret references and read/export/destination policy
before connecting team Tests APIs. Keep actor attribution server-assigned and
recheck roles at every mutation. Follow with team Tests navigation and conflict
recovery, then durable submissions and the separate serial executor.
