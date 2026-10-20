# Import a JMeter test plan

[Documentation](README.md) · [Dashboard](dashboard.md) · [Coverage program](jmeter-parity.md)

**Build → Import JMeter JMX** opens a guided review of an existing `.jmx` file.
This first converter targets JMeter **5.6.3**, XML format **1.2**, and a limited
set of HTTP plans. It converts eligible plans into native GoMeter journeys. It
does not run JMeter or provide full JMX compatibility or round-trip export.

![JMX compatibility review, runtime policies, and editable property inputs](images/jmx-import.jpg)

## Walk through an import

1. **Choose file.** Select or drop a JMX file up to 1 MiB. **Review sample plan**
   loads the bundled [HTTP fixture](../examples/jmx/http-smoke.jmx) without
   reading a local file. Its target is the local demo on port 8787.
2. **Review compatibility.** Select a component to inspect its source path and
   mapping. Supply any required `__P` property values and press **Apply values**.
   Review and accept the GoMeter runtime policies. Acceptance does not remove
   component blockers.
3. **Preview journey.** Eligible plans show the mapped flow canvas. Select nodes
   to inspect requests and loops. A blocked plan shows its source hierarchy,
   clearly marked **Not runnable**, without a partial executable configuration.
4. **Save.** Give the import a name. **Save scenario** is available only after
   all blockers and missing inputs are resolved and runtime policies accepted.
   **Save migration draft** preserves work that cannot yet run.

Importing, reviewing, previewing, and saving send **no workload requests**.
They do not replace your builder draft. **Open in journey builder** explicitly
loads the saved scenario; **Start run** is a separate action that sends traffic.
Start the bundled demo with `./bin/gometer-demo` before running the sample.

## What this converter accepts

| Source feature | Mapping and limits |
| --- | --- |
| Standard Thread Group | Exactly one enabled group; finite outer iterations 1–1000000, or infinite iterations with scheduler duration 1–86400 seconds; optional duration with finite iterations (first limit wins); 1–100000 users; ramp and scheduler startup delay 0–86400 seconds; delayed thread creation off; **Continue**, **Start Next Thread Loop**, **Stop Thread**, **Stop Test**, or **Stop Test Now** on error; retained user state |
| HTTP Request | One HTTP/HTTPS origin; explicit Follow Redirects off; keep-alive; default or HttpClient4 implementation; UTF-8; relative paths or literal absolute URLs |
| Request content | Methods and paths, scoped headers, one raw body argument, constant encoded GET arguments; variable references in paths, headers and raw bodies |
| HTTP Request Defaults | Scoped scalar defaults flattened into requests; arguments on defaults are blocked |
| Variables and properties | Static user-defined variables resolved in declaration order; `${name}` becomes `{{name}}` in request templates; literal `__P(name)` inputs and `__P(name,default)` defaults |
| Simple Controller | Flattens its ordered children while retaining scoped settings |
| Loop Controller | Fixed 1–1000 iterations; native nesting and worst-case execution limits also apply |
| Response Assertion | One literal body **Substring** pattern, or one literal response-code **Equals** pattern; parent scope only; non-2xx code assertions require Ignore Status |
| Cookie Manager | Optional standard policy covering the whole group; no preloaded cookies, resets, or custom implementation. Without one, imported journeys explicitly disable cookies |
| Disabled components | Disabled subtrees do not execute; their XML remains in the retained original source |
| Result listeners | Visible notice: GoMeter reports replace JMeter listeners; no JTL or configured listener file is written |

Unknown enabled components, properties, incompatible XML property types, and
unsupported scopes block conversion. Among the current blockers are unbounded
thread groups, multiple groups, delayed thread creation, unknown failure actions,
CSV files, extractors, timers, If/While controllers, scripts, plugins, redirects,
uploads, form encoding, custom HTTP timeouts, authentication managers, proxies,
and cross-origin flows. Native GoMeter supports some related features, including
branches, while loops and extraction; that does not imply a reviewed JMX mapping.

The bounds above are validation limits, not demonstrated generator capacity.
The component/property allowlist and tests live in `internal/jmx`.

## Runtime policies you must review

Converted plans use GoMeter's 5-second **total** request timeout, 30-second
whole-journey deadline, 10 MiB response limit, bounded loops, and aggregated
measurements. Admitted journeys drain after the load window. Finite Thread Group
loops map to native per-user `iterations`, independently of any inner Loop
Controller. Without a scheduler, finite plans have no implicit duration limit.
With a scheduler, the first count/time limit closes admission. JMeter checks its
duration between samplers; GoMeter drains the admitted journey instead. See the
[Apache Thread Group reference](https://jmeter.apache.org/usermanual/component_reference.html#Thread_Group)
and [5.6.3 loop implementation](https://github.com/apache/jmeter/blob/rel/v5.6.3/src/core/src/main/java/org/apache/jmeter/control/LoopController.java). HTTPS certificates
are verified. JMeter connection implementations, default headers, cookie edge
cases, timing statistics, and listener output can differ. This is an explicit
conversion to GoMeter behavior, not a JMeter runtime emulator.

Explicit source settings without a supported mapping remain blockers even when
you accept these policies. After conversion, you may edit the native scenario
in the builder, including its timeouts and HTTPS setting. Those later edits do
not rewrite the retained original XML.

## Drafts, original source, and storage

**Migration drafts** appears below the Scenario library. Drafts are separate,
non-runnable records, limited to 50 per workspace. Reopening reanalyzes the source
using the current converter. To fix a source blocker, edit a copy in JMeter and
choose the revised file; accepting the runtime review cannot bypass a blocker.

Saved scenarios retain their original JMX and explicitly supplied property
values. **Review original JMX** in the Scenario library opens that original
conversion, which may differ from subsequent builder edits. Native configuration
JSON exports contain the converted configuration, not this source provenance.

Records live in `<workspace>/migrations/` and `<workspace>/scenarios/`, under the
directory selected by `gometer ui -data` (default `.gometer`). Files are written
atomically with owner-only permissions. Workspace data is **not encrypted at
rest**. XML, bodies, headers, properties, and backups may contain credentials;
protect and retain them as carefully as native test configurations.

The parser rejects DTDs, external entities, namespaces, malformed component
pairs, excessive structure, and multiple documents. It never opens referenced
files, loads local JMeter properties, evaluates scripts, or contacts targets.
Limits include 500 components, 20000 XML elements, XML depth 64, and 64 explicit
properties. Server-side conversion reanalyzes the XML instead of trusting a
client-supplied compatibility report or configuration.

## Compatibility evidence

Tests cover accepted and blocked mappings, malformed XML, property resolution,
draft persistence, source retention, owner-only files, API authorization, and
rejection of forged runnable configurations. A counted local HTTP target checks
methods, URLs, headers, payloads, loop request counts, and absence of implicit
cookies. These checks do not establish equivalence with every JMeter plan;
cross-engine conformance remains part of the [parity program](jmeter-parity.md).

Source semantics are grounded in Apache's [component reference](https://jmeter.apache.org/usermanual/component_reference.html)
and [test-plan documentation](https://jmeter.apache.org/usermanual/test_plan.html).
The bundled fixture is authored for this converter; it is not a claim that all
JMeter HTTP plans can be imported.

## User ramp and startup conversion

`ThreadGroup.ramp_time` maps to `ramp_up`; scheduler `ThreadGroup.delay` maps to
`startup_delay`. Literal `__P` inputs work for both. Without the scheduler,
startup delay is inactive and ignored, including unresolved property references.
The importer records that decision and retains the original value in the XML.

JMeter 5.6.3's default thread creation rounds its per-user interval to a whole
millisecond using float32 arithmetic. The converter preserves that interval by
adjusting the native ramp and explaining the adjustment. For three users over
one second, the interval is 333ms and the native ramp becomes 999ms. If the
rounded interval is zero, starts are simultaneous. A rounded native ramp over
24h remains blocked. Native hand-authored ramps use Go duration precision.

Source references: [Thread Group schedule and rounding](https://github.com/apache/jmeter/blob/rel/v5.6.3/src/core/src/main/java/org/apache/jmeter/threads/ThreadGroup.java)
and [startup before ramp delay](https://github.com/apache/jmeter/blob/rel/v5.6.3/src/core/src/main/java/org/apache/jmeter/threads/JMeterThread.java).
JMeter's **Delay Thread creation until needed** option uses a different adaptive
ramp scheduler and remains blocked. GoMeter creates user work at scheduled starts
and uses one absolute group clock. JVM thread allocation, startup overhead,
sampler-boundary stopping, and OS scheduling are not emulated. Review the runtime
policies before converting; these mappings do not establish cross-engine timing
equivalence under load.

## Failure-action conversion

The explicit Thread Group `on_sample_error` setting maps as follows:

| JMeter action | Native `on_error` |
| --- | --- |
| Start Next Thread Loop (`startnextloop`) | Default `next_iteration` |
| Continue (`continue`) | `continue` |
| Stop Thread (`stopthread`) | `stop_user` |
| Stop Test (`stoptest`) | `stop_test` |
| Stop Test Now (`stoptestnow`) | `stop_test_now` |

Unknown or missing actions remain blockers. The
[Apache Thread Group reference](https://jmeter.apache.org/usermanual/component_reference.html#Thread_Group)
defines the source actions. Review the converter's explanation and native
[failure contract](configuration.md#failure-actions): failed verification does
not publish extracted values, whole-journey deadlines and safety errors abort
execution, and any user stopped on error fails a mandatory check. These are
explicit conversion policies; cross-engine equivalence has not been established.

The converter explicitly explains the test-wide stopping difference. JMeter
stops gracefully at the end of current samplers; GoMeter ends the failing journey
and drains other already-admitted journeys, including their later requests and
pauses. This may send additional HTTP requests after the triggering failure.
Accepting runtime policies accepts that native conversion contract. See the
[5.6.3 stopping implementation](https://github.com/apache/jmeter/blob/rel/v5.6.3/src/core/src/main/java/org/apache/jmeter/threads/JMeterThread.java).
Immediate stopping cancels GoMeter workload contexts and retains partial results;
it cannot undo a server-side effect. Neither conversion has cross-engine timing
or performance equivalence evidence.
