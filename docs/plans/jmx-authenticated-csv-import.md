# Draft: import authenticated, CSV-driven JMeter plans

Date: 23 October 2026

Deliver support for the supplied plan in small, independently verified changes.
The first release targets local execution of one setup group followed by one
standard users-mode group. Each milestone must preserve existing native tests
and previously supported imports.

## Target behavior

The supplied XML declares JMeter 5.3 and describes this sequence:

1. Run one setup user for one iteration. POST a client-credentials form to an
   authentication origin using its scoped Basic authorization header.
2. Extract `access_token` into `token`, then publish it as `accessToken`.
3. After setup completes, run 15 users against the application origin for 300
   seconds. Resolve the bearer header from the shared property. Preserve the
   configured Host header, redirect setting and Continue failure action.
4. Consume the attached CSV using the declared variable name `value`, shared
   rows and recycling. Explicit variable names and `ignoreFirstLine=false`
   make the first record data; the source also disables quoted-data parsing.

The CSV contents were not supplied. Its filename is an attachment requirement,
not permission to open that path on the dashboard server. Attaching the actual
file and checking its encoding and structure is part of final acceptance.

The diagnosis reproduced nine blocked components. Additional probes exposed
form-POST and multiple-origin restrictions when setup was moved into the main
group. Moving it there would also repeat authentication during the workload,
so that is not an acceptable automatic conversion.

Native requests currently enforce one origin, and every request stops at a
redirect. Native CSV requires a header row; native extraction accepts scalar
JSON Pointers. These are implementation constraints to address explicitly.
See [import coverage](../jmx-import.md), [CSV behavior](../datasets.md), and
[journey variables](../scenarios.md#variables-and-data).

## Milestones

### 1. Establish the compatibility contract and reference fixtures

- Create a sanitized JMX fixture with local authentication and application
  endpoints, dummy credentials, and a small attached CSV. Keep private source
  files and live endpoints out of the repository.
- Lock down the original rejection and distinguish root blockers from errors
  caused by a parent component being unsupported.
- Record request order, form bytes, headers, extraction, property scope, CSV
  consumption and redirect behavior against JMeter 5.3 and 5.6.3.
- Verify the bare `access_token` selector and the nested property expression
  exactly as supplied. If the source behaves differently from its apparent
  intent, report that finding and require an explicit correction.
- Specify setup failure, missing-token and cancellation behavior. Recommended
  native policy: a missing required token prevents load admission. If this
  differs from the source's Continue behavior, show the difference for review;
  do not silently change the source contract.

Acceptance: a repeatable local fixture captures the intended successful flow
and the failure cases. Remaining semantic differences have explicit decisions.
The [Apache component reference](https://jmeter.apache.org/usermanual/component_reference.html)
and [property-function reference](https://jmeter.apache.org/usermanual/functions.html#__property)
guide these checks; execution against the pinned versions establishes evidence.

### 2. Add guided JMX CSV attachment and mapping

- Identify `CSVDataSet` as an external input the user must attach. Match the
  attachment to its component; never resolve the embedded server path.
- Reuse the native CSV preview, column mapping, limits and embedded storage.
- Add reviewed support for explicit variable names and headerless input.
  Preserve the first data record, delimiter, quoting mode, shared cursor and
  recycle behavior. Blank source encoding needs an explicit encoding decision.
- Preserve the submitted source and conversion decisions. Keep unsupported
  dialects, short-row behavior and EOF policies blocked with precise reasons.
- Round-trip attached data through migration drafts, saved scenarios and
  portable bundles, including combined configuration size limits.

Acceptance: a three-row headerless fixture produces the expected values and
recycles without losing row one. Shared consumption is checked with multiple
users, without assuming a deterministic assignment order between threads.
Analyze, preview and save send no target traffic.

### 3. Map token extraction and form requests

- Convert the supported simple scalar JSON selector into the existing JSON
  Pointer extractor. Handle missing values and defaults according to the
  reviewed contract.
- The supplied extractor uses match number zero. Prove that the accepted scalar
  selector yields one result; do not silently map random selection over multiple
  matches to the first result. Broader JSONPath remains a separate increment.
- Map named URL-encoded form arguments to request bodies, preserving parameter
  order, duplicate names, encoding flags and equals separators. Reuse existing
  raw-body and template support where it preserves the source bytes.
- Verify spaces, plus signs, percent signs, Unicode and reserved characters;
  reject unsupported combinations rather than silently re-encoding them.

Acceptance: the local authentication fixture observes the expected form and
scoped headers. Successful extraction provides the expected token; missing or
ambiguous extraction follows the reviewed failure contract. Each new mapping
has a focused converter regression test and an executed-request check.

### 4. Add a native setup stage and run-scoped exports

- Add an optional bounded setup stage with its own origin, request settings and
  timeout. Run it once before starting workload users or their load clock.
- Reuse the request and extraction machinery through a stage-aware execution
  interface. Keep ordinary journeys restricted to their workload origin; this
  plan only needs a separate authentication origin for setup.
- Publish named, immutable run-scoped exports after setup. Keep property names
  distinct from per-user variables, define precedence, and make every admitted
  user observe the same exported token.
- Recognize only a tested, declarative subset of the supplied
  `__setProperty(..., ${token})` script and corresponding `__property(...)`
  reads. Parse and translate that pattern; do not execute BeanShell or add a
  general script interpreter. Other scripts remain clearly blocked.
- Propagate cancellation through setup. Show setup progress and its own result,
  with a mandatory completion check and explicit stopping reason.
- Keep setup requests, elapsed time and latency separate from workload counts,
  rates and thresholds. Exclude runtime-extracted token values from persisted
  results, exports and logs; retain only export names and status.
- Carry the new configuration/result fields through validation, CLI, dashboard,
  saved history, JSON/HTML reporting and versioned bundle validation. Older
  configurations and bundles retain their current behavior.

Acceptance: exactly one token request precedes all workload requests, including
with 15 users. Main requests carry Bearer authorization, never the setup Basic
header. Slow setup does not shorten the 300-second load window or dilute its
throughput. Failed or canceled setup follows the chosen admission policy and
does not leak values into another run.

### 5. Implement bounded, explicit redirect following

- Keep redirect following opt-in so existing native configurations retain their
  behavior. Map Follow Redirects and Auto Redirects as distinct source settings.
- Define hop limits, loop detection, relative locations, cookies, method/body
  replay and origin changes. Test 301/302/303/307/308 behavior against JMeter.
- Scope Authorization and Host headers to their intended destination. Any
  credential-forwarding difference needs explicit review during import.
- Keep logical HTTP attempts separate from redirect hops. Document full-chain
  latency and byte accounting and expose additional hop diagnostics separately.
  JMeter's Follow Redirects produces sub-samples, so identical result-record
  counts must not be assumed. See the
  [HTTP Request reference](https://jmeter.apache.org/usermanual/component_reference.html#HTTP_Request).
- Bound the whole chain by the request deadline and response limits; include
  intermediate body consumption and cancellation in those bounds.

Acceptance: two counted local origins confirm the actual hop sequence, methods,
headers, cookies, errors and body replay. Loops and excessive chains fail
predictably. No-follow behavior and existing request accounting remain intact.

### 6. Assemble the guided conversion and version support

- Map the setup stage, exported property, workload and CSV attachment together.
  Explain the resulting stages and decisions in the compatibility preview.
- Admit supported JMeter 5.3 fixtures alongside the existing 5.6.3 baseline only
  after their conformance checks pass. Version metadata must not override
  component/property validation; unvalidated versions keep a clear policy.
- Report conversion status per capability: supported, attachment needed,
  decision required, or unsupported. Collapse cascading errors beneath their
  root cause so users can see what to fix first.
- Reanalyze source, attachments and accepted decisions on the server before
  saving. Keep incomplete migrations non-runnable and retain original source.
- Confirm the existing Host-header override is preserved. Result listeners
  continue to map to GoMeter reporting with an explicit notice.

Acceptance: the sanitized supplied-plan fixture becomes runnable after its CSV
is attached and documented differences are accepted. Changing a client-side
report cannot bypass a blocker. Draft reopening, save/reuse, export/import and
unsupported-script rejection all remain correct.

### 7. Complete conformance, documentation and release checks

- Run a short deterministic version first: one authentication POST plus a known
  number of application GETs, independently counted at the local servers.
- Run the representative 15-user, 300-second workload. Check setup ordering,
  shared-token consistency, duration boundaries, cancellation and request-count
  reconciliation. Do not claim identical throughput or latency across engines.
- Compare JMeter and GoMeter on both success and failure fixtures. Document
  accepted differences, including redirect result records and missing tokens.
- Verify save/reload, migration drafts, old bundles, CLI reports, dashboard
  results, themes and narrow layouts. Run the repository's required build,
  static, race and browser checks.
- Update import documentation and feature coverage only for proven support.
  Report the supported subset explicitly.

Acceptance: independently captured traffic matches the approved contract, all
required checks pass, and the attached production-shaped CSV has been validated
without running the user's live targets.

## Scope and build coordination

This draft does not promise arbitrary BeanShell, full JSONPath, independent
user groups, teardown, distributed users or automatic token refresh. Those
remain separate increments. If the token expires during a run, preserve the
source behavior and report failures; do not add hidden refresh traffic.

Start with milestone 1, then take milestones 2 and 3 as small converter changes.
Milestones 4 and 5 are separate runtime changes. Milestones 6 and 7 integrate
and prove the complete path. A partially completed milestone must not make the
original plan appear fully supported.

Before implementation, agree file ownership with the active build agents:

| Area | Main implementation locations |
| --- | --- |
| Parsing and conversion | `internal/jmx`, sanitized `examples/jmx` fixtures |
| CSV and native configuration | `internal/config/csv.go`, `config.go`, `scenario.go` |
| Setup execution and redirects | `internal/engine`, focused transport/session tests |
| Import attachments and persistence | `internal/dashboard/jmx.go`, `store.go` |
| Guided review and preview | `internal/dashboard/assets/jmx.js`, `jmx.css` |
| Setup visibility and portable results | Engine result types, dashboard validation, `internal/report`, result UI |

The shared dashboard, worker catalog and existing result visualization already
have local changes. Use narrow edits and isolated test workspaces. Do not mix
this work with the separate form-layout prototype.

## Current status

Planning only. The earlier diagnosis ran the actual importer and controlled
source variants; no JMeter reference run, new importer feature or runtime change
has been implemented for this plan yet. The milestone acceptance checks above
are proposed work, not completed validation.
