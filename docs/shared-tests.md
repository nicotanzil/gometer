# Shared test documents

[Documentation](README.md) · [Team setup](team-workspace.md) · [Managed secrets](managed-secrets.md)

Status: backend implementation, 25 October 2026. Shared create, read, update,
copy and export APIs are implemented. The shared editor, durable queue, executor
and resolution of mounted secret values during execution remain pending. These
APIs never start a test or read a secret file.

## Public and concealed fields

All enabled workspace members may read and export deliberately public fields.
Operators and administrators may create, update and copy shared tests; viewers
cannot. Every write rechecks current membership in the database and records
server-assigned attribution and a metadata-only audit event in the same
transaction. A session's old role does not grant lasting write permission.

A versioned document separates `public_config` from `secret_bindings`:

```json
{
  "schema_version": 1,
  "public_config": {
    "name": "Checkout smoke",
    "url": "https://checkout.example",
    "headers": {"Authorization": ""},
    "mode": "rate",
    "rate": 1,
    "duration": "1s"
  },
  "secret_bindings": [
    {
      "path": "/headers/Authorization",
      "reference_id": "11111111111111111111111111111111",
      "revision": 1
    }
  ]
}
```

The ID is a placeholder: use an enabled reference registered in the same
workspace. The bound field must exist and contain an empty string. A binding has
only a JSON path, a reference ID and its exact revision. It cannot contain a
credential, source filename or mount path. For this format the mounted value
will represent the complete field, including an authentication scheme if needed;
no prefix, concatenation or transformation is implied.

Placing a value in `public_config` explicitly declares it public to the workspace.
This includes URLs, query values, bodies, variables, inline data and CSV content.
Use bindings for sensitive values. Literal `Authorization`, `Proxy-Authorization`,
`Cookie` and `X-Api-Key` headers are rejected; those headers require bindings.
This small header rule is not a general credential detector: custom header names,
passwords inside JSON bodies and dataset values require the author's correct
classification. Labels and names are public too.

Bindings currently support request bodies, request headers except `Host`, global
and scenario variables, and individual inline data cells. Request fields inside
branches and loops are supported. Array indices are zero-based without leading
zeros; escape `~` as `~0` and `/` as `~1` in a map key. Binding paths cannot target
URLs, labels, load settings, telemetry, workers, arbitrary structural fields or
whole CSV files. Use managed inline data cells for sensitive dataset fields in
this milestone; do not import sensitive CSV content into the public section.
Unsupported bindings fail explicitly.

Documents are limited to 1 MiB and 1,024 bindings. Unknown properties, trailing
JSON, duplicate keys (including case variants), missing/invalid destinations,
nonempty bound fields and ambiguous header names are rejected. Public workload
settings must also pass native configuration validation. Errors do not reflect
submitted values.

## API and revision handling

These routes belong to `gometer workspace serve`, not the personal dashboard.
They require its session and configured workspace; clients cannot supply an
actor or workspace selector.

| Route | Result |
| --- | --- |
| `POST /api/tests` | Create from a shared document; returns 201, metadata, document, ETag and Location |
| `GET /api/tests/{id}/document` | Read metadata plus the public document |
| `PUT /api/tests/{id}/document` | Replace a shared document using its ETag |
| `POST /api/tests/{id}/copy` | Independent copy of the exact test revision; no request body |
| `GET /api/tests/{id}/export` | Download the document, containing public fields and reference IDs only |

The existing list and metadata routes remain available. JSON writes require
`Content-Type: application/json`. All writes require the exact browser Origin
and `X-Gometer-CSRF` from `GET /api/session`. Update and copy require an exact
`If-Match: "test/{id}/{revision}"`; missing preconditions return 428, malformed
or weak ETags return 400, and stale test/reference revisions return 409.

Create, update and copy check every referenced catalog record in the same
workspace transaction. Disabled references return 403; missing references return
404. A reference retarget cannot silently move a saved test to a different
credential. Reading an old document remains safe and preserves its pinned
reference revision; an operator must explicitly select the current reference
revision before saving a replacement.

Copies receive a new ID, creator and revision 1, with source-test/source-revision
provenance. They retain reference IDs and revisions, never credential bytes.
Exports contain the document rather than actor/session data or private source
keys. They can be submitted to the creation API, but reference IDs are local to a
workspace: export/import does not provision or remap secrets in another workspace.

Legacy native definitions have no public classification. The migration leaves
that state intact: list/metadata access is available, while document, export,
shared update and shared copy refuse legacy records. An operator must review
fields and create a separate shared document explicitly. A raw repository update
withdraws any prior public document to prevent stale public/private versions.
Personal JSON files are never imported automatically.

## Destination and execution trust

The selected team model allows any valid workload destination reachable by the
executor; there is no application-level administrator destination allowlist.
Existing native URL validation and same-origin journey rules still apply. The
service does not probe targets during saving, copying or exporting.

Operators allowed to execute secret-backed tests must be trusted to choose where
they send credentials. An operator could select a server they control and recover
credentials there. Concealment in GoMeter APIs and exports cannot prevent that.
Executor network reachability, infrastructure egress rules and role assignment
are operational trust boundaries. This policy does not grant viewers execution
permission or relax browser/session or cross-workspace checks.

The executor must resolve bindings only after rechecking current membership,
reference state and the frozen submission. It must validate the resulting native
configuration, preserve secrecy across diagnostics, extraction/assertion output,
errors and results, and keep resolved values out of saved documents. None of
those execution guarantees is claimed by this authoring milestone.

## Verification

`make check build` checks source, race-enabled Go tests, document validation and
binaries. `make test-workspace` exercises disposable PostgreSQL, local OIDC,
authorization, source concealment, conditional edits, races, audit rollback and
migration preservation. With `GOMETER_TEAM_BROWSER=1` and the browser dependencies
in [Contributing](../CONTRIBUTING.md), the fixture additionally exercises creation,
read, update, copy, export and stale/CSRF rejection from a real signed-in Chromium
page. These are backend/API checks; they do not establish a completed editor,
executor, company LDAP integration or live Kubernetes deployment.
