# Managed secret references and team metadata APIs

[Documentation](README.md) · [Team setup](team-workspace.md) · [Security](../SECURITY.md)

Status: local implementation, 25 October 2026. The reference catalog, mounted-file
reader and authenticated metadata APIs are implemented. Explicitly public test
documents support managed bindings, authoring, copy and export through the
[shared Tests API](shared-tests.md). Credential resolution during execution,
the durable queue and executor remain pending. The [shared editor](team-editor.md)
uses reference selectors and never loads mounted values. This milestone does not make secret placeholders in
personal configurations executable.

## What a reference represents

A reference has a stable ID, public name, revision, enabled state and timestamps.
It points to a private mounted-file key. PostgreSQL stores the mapping; it does
not store the resolved credential. Responses omit the file key for every role,
including administrators. Audit events retain actor, operation, ID, revision and
time, without the source key or value.

Use a descriptive public name such as **Checkout API credential**. Do not put a
password or token in that name: labels and author names are shared metadata, not
a secret-detection system. `source_key` means a filename such as `checkout-token`,
never the credential text or an absolute path.

All enabled members may discover reference metadata. Only current workspace
administrators may create, rename, retarget, enable or disable references.
Operators and viewers cannot change the catalog. The database checks the current
role inside the mutation transaction and commits its audit record atomically.

Each workspace has at most 200 references, including disabled records. Names are
unique within that workspace without case distinctions. Disabling retires a
reference; deletion is not exposed. Renaming preserves its ID. Every update
advances its revision, and an update based on an old revision fails with 409.
A source-descriptor lookup requires the exact revision and rejects disabled
references, stale revisions, viewers and other workspaces.

## Team API surface

These routes belong to `gometer workspace serve`, not `gometer ui`. They require
its secure session cookie and enforce its configured workspace. A request cannot
select an actor or another workspace through query parameters or JSON fields.

| Route | Access and result |
| --- | --- |
| `GET /api/tests?scope=all&search=checkout` | Any enabled member; metadata only |
| `GET /api/tests?scope=mine` | Creator is the current authenticated member |
| `GET /api/tests/{id}` | Metadata and an ETag; configuration is never selected |
| `GET /api/secret-references` | Reference metadata, including enabled state |
| `GET /api/secret-references/{id}` | One reference's metadata and ETag |
| `POST /api/secret-references` | Admin only; create a reference |
| `PUT /api/secret-references/{id}` | Admin only; conditional update |

Tests search matches the test name, creator or last editor. The list response is
`{"tests": [...]}`. Reference lists use `{"references": [...]}`. Neither returns
workload URLs, headers, bodies, datasets, variables, source filenames or secret
bytes. Test author attribution contains identity and display name, not invented
current-role or disabled-state values.

The personal dashboard's raw configuration APIs are not mounted here. Only
explicitly classified [shared documents](shared-tests.md) may be authored, read,
copied or exported. Legacy configurations remain private. Execution is not
available on this service.

## Register a reference

Sign in as a workspace administrator. From that origin, clients obtain the CSRF
token from `GET /api/session`. Mutations require the exact browser origin, JSON
content type and `X-Gometer-CSRF`. Session credentials and CSRF tokens belong in
the current browser/process, not checked-in scripts or logs.

This same-origin browser example supplies a filename, not its secret value:

```js
const session = await (await fetch('/api/session')).json();
const headers = {
  'Content-Type': 'application/json',
  'X-Gometer-CSRF': session.csrf_token,
};
const created = await fetch('/api/secret-references', {
  method: 'POST',
  headers,
  body: JSON.stringify({
    name: 'Checkout API credential',
    source_key: 'checkout-token',
  }),
});
if (!created.ok) throw new Error(`Reference not saved (${created.status})`);
const etag = created.headers.get('ETag');
const reference = await created.json();
```

Creation returns 201, an ETag and the metadata resource's Location. New references
are enabled. Unknown fields such as `value` or `password`, oversized bodies and
trailing JSON are rejected without reflecting their contents. The web service
never opens a source file to validate or probe it; creating a reference does not
prove that a mounted file exists.

To disable the reference without retrieving its private source:

```js
const updated = await fetch(`/api/secret-references/${reference.id}`, {
  method: 'PUT',
  headers: {...headers, 'If-Match': etag},
  body: JSON.stringify({name: reference.name, enabled: false}),
});
```

`name` and `enabled` are required on update. An omitted or empty `source_key`
retains the existing mapping; a new valid filename explicitly retargets it.
Missing `If-Match` returns 428, invalid/weak ETags return 400, and a stale revision
or duplicate name returns 409. Fetch current metadata before reconciling a
conflict. An exact source mapping cannot be erased; disable it instead.

## Mounted-file reader boundary

The selected source is a Kubernetes Secret volume mounted read-only. The reader
library opens an operator-selected base directory and an explicit workspace
subdirectory. Example layout, using a placeholder workspace ID:

```text
/run/gometer/workload-secrets/
  11111111111111111111111111111111/
    checkout-token -> ..data/checkout-token
    ..data -> ..projected-version
    ..projected-version/
      checkout-token
```

The workspace directory must be a real directory, not a symlink to another
workspace. Individual file symlinks may follow Kubernetes's projected-volume
layout within that directory. Root-confined filesystem access rejects escaping
symlinks, traversal, absolute source paths and sibling workspace access.
The base and workspace directories must be operator-owned and read-only to the
application. Future executor code must derive the workspace from authorized
submission data, not browser-selected paths.

Keys use `[A-Za-z0-9_-][A-Za-z0-9_.-]{0,127}`; dot-prefixed projection control
entries and nested paths are rejected. Reads require a nonempty regular file,
cap the result at 64 KiB, preserve exact bytes including newlines, and return
sanitized errors. Values are not cached. A subsequent read observes the current
projected file after rotation; that is not an immutable secret-version snapshot.

Kubernetes updates Secret-volume projections eventually, while `subPath` mounts
do not receive automatic Secret updates. Use a whole-directory projection for
rotation; see the [official Secret-volume documentation](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod).
Local symlink fixtures verify the reader behavior, not a running cluster's timing.

The reader is an internal executor building block. `workspace serve` has no secret
mount setting, no file-read endpoint and no Kubernetes API credentials. The
executor and dispatch-time policy checks are not connected yet. Persisted
reference bindings do not cause file reads during authoring or export. Keep
workload secret mounts away from the browser service when deploying separate
components. Report generation and engine execution must not receive
resolved credentials until their concealment rules are implemented and tested.

## Verification

```sh
make check build
make test-workspace
GOMETER_TEAM_BROWSER=1 make test-workspace
```

The last command needs the Playwright/Chrome settings described in
[Contributing](../CONTRIBUTING.md). Unit fixtures cover projected-file rotation,
workspace confinement and invalid/oversized values. PostgreSQL and HTTP tests
cover role and workspace isolation, metadata disclosure, audit rollback,
conditional-write races and upgrades that preserve definitions and sessions.
The browser fixture exercises the authenticated metadata APIs, reference
creation, CSRF rejection and stale-update handling without reading real secrets
or sending target traffic. Real Kubernetes mounts and company SSO remain separate
integration acceptance steps.

Verified locally on 25 October 2026: project checks and builds, the PostgreSQL
and HTTP suite, Chromium acceptance and a source vulnerability scan. Mounted-
file rotation and confinement fixtures also passed on Linux/ARM64 in an isolated
container. These results do not prove real Kubernetes projection timing or
secret-backed engine execution.
