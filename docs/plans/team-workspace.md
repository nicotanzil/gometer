# Shared team workspace implementation

Status: local implementation in progress, 25 October 2026. Release held until the
full team workspace is complete. No deployment.

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
authorized committing the completed milestones on 24 October 2026. On 25 October
2026, the user chose to finish the full team workspace before releasing. Do not
create or push a release tag for the foundations alone. Deployment remains
outside the current authorization.

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

The sign-in slice is committed as `efdd821`. No real company Keycloak, LDAP or Kubernetes
acceptance was run. Personal dashboard APIs are not exposed by this service.
Local logout does not end Keycloak SSO; Keycloak/LDAP-only revocation is observed
at the next sign-in or local expiry. No refresh tokens are stored.

## Secret references and metadata API slice — 25 October 2026

The user selected Kubernetes-mounted secret files for the first source. Implemented
locally: migration 003 and a workspace-scoped reference catalog with stable IDs,
public names, revisions, enable/disable state and metadata-only audit. Current
admins may create/update through CSRF-protected APIs; all enabled members may
read reference metadata. Responses omit source filenames and values. Empty or
omitted update keys retain the hidden source. ETag preconditions prevent stale
retargeting. Source-descriptor lookup rejects viewers, disabled references and
stale revisions.

The mounted-file reader confines access to an explicit workspace child directory,
rejects escaping symlinks and invalid/non-regular/empty/oversized files, and bounds
values at 64 KiB. Projected-volume rotation is read fresh. The HTTP service does
not open mounts, accept credential values, resolve test placeholders or contact
the Kubernetes API. No execution or dispatch claims follow from this reader.

Authenticated Tests GET APIs now expose metadata, creator/editor attribution,
All/My filtering and teammate search. They never select raw configuration.
Configuration authoring, copy, export, download and execution routes remain closed.
See [the guide](../managed-secrets.md) for request schemas and remaining policy work.

Verified locally: full project checks/builds; PostgreSQL upgrade preservation,
current-role and workspace checks, revision races and audit rollback; HTTP
concealment and invalid-input cases; Chromium same-origin metadata requests,
reference creation/update, CSRF/stale-write rejection, sign-in/reload/logout and
responsive themes. The source vulnerability scan reported no known reachable
vulnerabilities. The mounted-file rotation and confinement fixtures also passed on Linux/ARM64
in an isolated container. The completed secret-reference and shared-document milestones are included in
the next implementation commit. No real cluster projection,
company Keycloak/LDAP, executor or secret-backed workload was exercised.

## Approved public fields and execution policy — 25 October 2026

The user approved read/export of explicitly public fields for all enabled members,
with managed references for concealed values. The user chose any destination
reachable by the executor, not an administrator-maintained application allowlist.
Preserve native URL/same-origin journey validation and current-role checks. This
choice trusts operators with credential destinations: API concealment cannot
prevent an operator from recovering a credential at a target they control.

## Shared document backend slice — 25 October 2026

Implemented locally: migration 004, explicit `public_config` plus typed reference
bindings, current-role authoring APIs, conditional saves/copies, public reads and
exports. Legacy definitions stay unclassified/private across migration. Binding
changes require current enabled references at exact revisions; source keys and
credential values are never selected by authoring/read/export paths. Raw native
repository updates withdraw any old public document. See
[shared documents](../shared-tests.md) for limits and the exact API surface.

Verified locally: `make check build`, disposable PostgreSQL migration, current-role,
revision-race, audit rollback and size-boundary checks, plus signed-in Chromium
create/read/edit/copy/export and CSRF/stale-write rejection. These APIs do not read
mounts or dispatch workloads.
The shared editor, secret-backed execution and the full team workspace are not
complete. No release tag or push is authorized before that work is finished.

## Next slice

Connect the team Tests UI and draft/conflict recovery to the shared document APIs,
then build durable submissions and the separate serial executor. Resolve mounted
credentials only inside execution and keep them out of errors, results, captures,
logs, saved definitions and recovered drafts.

## Team release gates — 25 October 2026

The completed foundations are not a complete shared workspace. Keep version
0.9.0 unpublished until the remaining product scope is implemented and verified.
The release workflow publishes executable archives, a GitHub Release and
container images when a version tag is pushed.

- [x] PostgreSQL migrations, explicit bootstrap, membership and current-role checks.
- [x] Browser sign-in, durable sessions, CSRF checks and local sign-out.
- [x] Metadata-only Tests APIs, secret-reference catalog and confined file reader.
- [x] Resolve public-field/export and destination policies (any reachable destination).
- [ ] Complete managed secrets: persisted bindings and public read/copy/export are
  implemented; execution, logs, errors, results and recovered drafts remain pending.
- [ ] Implement shared Tests authoring and safe revision-conflict/draft recovery.
- [ ] Implement durable, idempotent FIFO submissions and one atomic workspace slot.
- [ ] Execute through a dedicated executor with current authorization, destination
  checks and immutable worker selection; no silent fallback or uncertain replay.
- [ ] Implement Queue/Team views, ownership-aware cancellation and Stop; retain the
  slot through draining, unknown execution and failed result persistence.
- [ ] Add team Kubernetes examples and verify migration, backup, restore, executor
  restart and concurrent submissions using disposable infrastructure.
- [ ] Complete browser acceptance for shared authoring, queue, results and roles.
- [ ] Confirm company Keycloak/LDAP and cluster acceptance separately with the
  operator; local fixtures do not establish deployment readiness.
- [ ] Validate the exact release commit, packages and checksums before tagging;
  exclude unrelated prototypes and plans from release commits.

The release workflow now requires `make test-workspace` before packaging or
publishing, in addition to `make check`. This runs a disposable PostgreSQL fixture
and rejects silently skipped required database/CLI tests. It does not replace
the pending browser, executor, cluster or company SSO acceptance gates above.
