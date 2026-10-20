package jmx

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gometer/internal/config"
	"gometer/internal/correlation"
)

type Options struct {
	Properties    map[string]string `json:"properties,omitempty"`
	AcceptRuntime bool              `json:"accept_runtime"`
}
type Component struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	Status  string   `json:"status"`
	Details []string `json:"details"`
}
type Input struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Supplied bool   `json:"supplied"`
}
type Preview struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Children []Preview `json:"children,omitempty"`
}
type Report struct {
	Version       int            `json:"version"`
	SourceVersion string         `json:"source_version"`
	Name          string         `json:"name"`
	Ready         bool           `json:"ready"`
	Blockers      int            `json:"blockers"`
	MissingInputs int            `json:"missing_inputs"`
	Components    []Component    `json:"components"`
	Inputs        []Input        `json:"inputs"`
	Preview       []Preview      `json:"preview"`
	Config        *config.Config `json:"config,omitempty"`
}
type analyzer struct {
	report    Report
	options   Options
	cfg       config.Config
	variables map[string]string
	inputs    map[string]Input
	base      *url.URL
	groups    []*tree
	names     map[string]int
}

// Analyze returns a runnable configuration only after every execution blocker,
// missing property, and required runtime review is resolved. A blocked preview
// is a source tree, never a partially executable configuration.
func Analyze(r io.Reader, options Options) (Report, error) {
	root, err := parse(r)
	if err != nil {
		return Report{}, err
	}
	if len(options.Properties) > 64 {
		return Report{}, fmt.Errorf("at most 64 JMeter properties may be supplied")
	}
	for k, v := range options.Properties {
		if len(k) < 1 || len(k) > 128 || len(v) > 8192 {
			return Report{}, fmt.Errorf("invalid property name or value length")
		}
	}
	n := 0
	nodes, err := buildTree(root.children[0], "", &n)
	if err != nil {
		return Report{}, err
	}
	if len(nodes) != 1 || nodes[0].e.kind != "TestPlan" {
		return Report{}, fmt.Errorf("JMX must contain exactly one TestPlan")
	}
	a := &analyzer{report: Report{Version: 1, SourceVersion: root.attrs["jmeter"], Components: []Component{}, Inputs: []Input{}}, options: options, cfg: config.Defaults(), variables: map[string]string{}, inputs: map[string]Input{}, names: map[string]int{}}
	a.cfg.Name = nodes[0].e.attrs["testname"]
	if root.attrs["jmeter"] != "5.6.3" {
		a.global("source-version", "JMeter version", "blocked", "The validated import baseline is JMeter 5.6.3. This plan declares a different or missing version; review and re-save it in that baseline before importing.")
	}
	if a.cfg.Name == "" {
		a.cfg.Name = "Imported JMeter plan"
	}
	a.report.Name = a.cfg.Name
	// JMeter has no implicit Cookie Manager; the native engine historically did.
	cookies := false
	a.cfg.Cookies = &cookies
	a.inventory(nodes, "")
	a.collectVariables(nodes, false)
	if len(a.groups) != 1 {
		a.global("thread-groups", "Thread groups", "blocked", "Exactly one enabled standard ThreadGroup is supported. Independent groups, setup, teardown, and plugin groups need engine support.")
	}
	if len(a.groups) == 1 {
		group := a.groups[0]
		a.group(group)
		scope := scope{defaults: map[string]string{}, headers: map[string]string{}}
		scope = a.scope(nodes[0], scope)
		a.cfg.Scenarios = []config.Scenario{{Name: a.unique(group.e.attrs["testname"]), Variables: a.variables, Steps: a.sequence(group, scope)}}
	}
	status := "review"
	if options.AcceptRuntime {
		status = "mapped"
	}
	a.global("runtime", "GoMeter runtime policies", status, "Review required: GoMeter uses a 5s total request timeout, 30s journey deadline, 10 MiB response limit, verified HTTPS, bounded loops and aggregates, and drains admitted journeys after the load window. GoMeter uses one absolute group clock and creates user work at scheduled starts; JVM thread allocation is not preserved. JMeter listeners are replaced by GoMeter reports. Connection implementations, default headers, cookie edge cases, and timing statistics can differ. This is conversion to GoMeter behavior, not a JMeter runtime emulator.")
	if a.cfg.HasTestStopPolicy() {
		policy := "Stop Test Now cancels native workload contexts, including active HTTP and journey pauses; partial results are retained and a mandatory check fails. It cannot roll back server-side effects."
		if a.cfg.ErrorAction() == config.StopTest {
			policy = "Stop Test ends the failing journey and drains other admitted journeys, including later requests and pauses. JMeter drains current samplers instead; conversion may therefore send additional requests after the failure. A mandatory check fails."
		}
		component := &a.report.Components[len(a.report.Components)-1]
		component.Details = append(component.Details, policy)
	}
	for _, input := range a.inputs {
		a.report.Inputs = append(a.report.Inputs, input)
		if !input.Supplied {
			a.report.MissingInputs++
		}
	}
	sort.Slice(a.report.Inputs, func(i, j int) bool { return a.report.Inputs[i].Name < a.report.Inputs[j].Name })
	if a.base != nil {
		a.cfg.URL = a.base.String()
	}
	if a.report.MissingInputs == 0 && a.report.Blockers == 0 {
		if _, err := a.cfg.Validate(); err != nil {
			a.global("native-validation", "Converted configuration", "blocked", "Native validation: "+err.Error())
		} else {
			data, _ := json.Marshal(a.cfg)
			if len(data) > config.MaxConfigBytes {
				a.global("native-size", "Converted configuration", "blocked", "Converted configuration exceeds 1 MiB.")
			}
		}
	}
	a.report.Preview = a.preview(nodes)
	a.report.Ready = a.report.Blockers == 0 && a.report.MissingInputs == 0 && options.AcceptRuntime
	if a.report.Ready {
		a.report.Config = &a.cfg
	}
	return a.report, nil
}

func (a *analyzer) global(id, name, status, detail string) {
	a.report.Components = append(a.report.Components, Component{ID: id, Name: name, Kind: "Compatibility", Path: name, Status: status, Details: []string{detail}})
	if status == "blocked" {
		a.report.Blockers++
	}
}
func (a *analyzer) issue(t *tree, status, detail string) {
	if t.index < 0 {
		return
	}
	c := &a.report.Components[t.index]
	if c.Status == "ignored" {
		return
	}
	if status == "blocked" && c.Status != "blocked" {
		a.report.Blockers++
		c.Status = "blocked"
	} else if c.Status != "blocked" && status == "input" {
		c.Status = "input"
	}
	c.Details = append(c.Details, detail)
}
func (a *analyzer) inventory(nodes []*tree, parent string) {
	for _, t := range nodes {
		disabledHere := t.e.attrs["enabled"] == "false"
		name := t.e.attrs["testname"]
		if name == "" {
			name = t.e.kind
		}
		status := "mapped"
		detail := "Detected enabled component. Mapping details and constraints appear below; execution requires a complete compatible plan."
		if disabledHere {
			status = "ignored"
			detail = "Disabled component and its subtree are not executed."
		}
		t.index = len(a.report.Components)
		a.report.Components = append(a.report.Components, Component{ID: t.id, Name: name, Kind: t.e.kind, Path: t.path, Status: status, Details: []string{detail}})
		if disabledHere {
			continue
		}
		if t.e.attrs["enabled"] != "" && t.e.attrs["enabled"] != "true" {
			a.issue(t, "blocked", "Invalid enabled attribute.")
		}
		if c := t.e.attrs["testclass"]; c != "" && c != t.e.kind {
			a.issue(t, "blocked", "The testclass differs from the XML component type.")
		}
		container := parent == "ThreadGroup" || parent == "GenericController" || parent == "LoopController"
		switch t.e.kind {
		case "TestPlan":
			if parent != "" {
				a.issue(t, "blocked", "A TestPlan must be the document's top-level component.")
			}
		case "ThreadGroup":
			if parent != "TestPlan" {
				a.issue(t, "blocked", "Thread groups must be direct children of the TestPlan.")
			}
		case "HTTPSamplerProxy", "GenericController", "LoopController":
			if !container {
				a.issue(t, "blocked", "This executable component is outside a supported execution container.")
			}
		default:
			if !container && parent != "TestPlan" && parent != "HTTPSamplerProxy" {
				a.issue(t, "blocked", "Nested configuration or listener components need a supported scope mapping.")
			}
		}
		switch t.e.kind {
		case "TestPlan", "ThreadGroup", "GenericController", "LoopController", "HTTPSamplerProxy", "ConfigTestElement", "HeaderManager", "Arguments", "CookieManager", "ResponseAssertion":
		case "ResultCollector":
			a.report.Components[t.index].Status = "notice"
			a.report.Components[t.index].Details = []string{"JMeter listener files and display settings are replaced by GoMeter reports; no JTL file is written. Review runtime policies."}
		default:
			a.issue(t, "blocked", "This component has no supported mapping. It is retained in the preview and migration draft; scripts and referenced files are never executed or opened.")
		}
		if t.e.kind == "ThreadGroup" {
			a.groups = append(a.groups, t)
		}
		if t.e.kind == "TestPlan" {
			if t.e.value("TestPlan.functional_mode") == "true" {
				a.issue(t, "blocked", "Functional testing mode and response recording need a supported mapping.")
			}
			if t.e.value("TestPlan.user_define_classpath") != "" {
				a.issue(t, "blocked", "Additional Java classpaths are not loaded.")
			}
		}
		if t.e.kind == "ConfigTestElement" && t.e.attrs["guiclass"] != "HttpDefaultsGui" {
			a.issue(t, "blocked", "Only HTTP Request Defaults configuration elements are supported.")
		}
		a.checkProperties(t)
		a.inventory(t.children, t.e.kind)
	}
}

var allowed = map[string]string{
	"TestPlan":          "TestPlan.functional_mode TestPlan.serialize_threadgroups TestPlan.tearDown_on_shutdown TestPlan.user_defined_variables TestPlan.user_define_classpath Arguments.arguments Argument.name Argument.value Argument.metadata",
	"ThreadGroup":       "ThreadGroup.on_sample_error ThreadGroup.num_threads ThreadGroup.ramp_time ThreadGroup.scheduler ThreadGroup.duration ThreadGroup.delay ThreadGroup.same_user_on_next_iteration ThreadGroup.delayedStart ThreadGroup.start_time ThreadGroup.end_time ThreadGroup.main_controller LoopController.continue_forever LoopController.loops",
	"GenericController": "",
	"LoopController":    "LoopController.continue_forever LoopController.loops",
	"ConfigTestElement": "HTTPSampler.domain HTTPSampler.port HTTPSampler.protocol HTTPSampler.path HTTPSampler.method HTTPSampler.connect_timeout HTTPSampler.response_timeout HTTPSampler.contentEncoding HTTPSampler.concurrentPool HTTPSampler.implementation HTTPSampler.image_parser HTTPSampler.embedded_url_re HTTPSampler.proxyHost HTTPSampler.proxyPort HTTPSampler.proxyUser HTTPSampler.proxyPass HTTPSampler.sourceIp HTTPSampler.sourceType HTTPSampler.use_keepalive HTTPSampler.follow_redirects HTTPSampler.auto_redirects HTTPSampler.DO_MULTIPART_POST HTTPSampler.BROWSER_COMPATIBLE_MULTIPART HTTPsampler.Arguments Arguments.arguments HTTPArgument.always_encode HTTPArgument.use_equals HTTPArgument.content_type Argument.name Argument.value Argument.metadata HTTPsampler.Files",
	"HTTPSamplerProxy":  "HTTPSampler.domain HTTPSampler.port HTTPSampler.protocol HTTPSampler.path HTTPSampler.method HTTPSampler.connect_timeout HTTPSampler.response_timeout HTTPSampler.contentEncoding HTTPSampler.concurrentPool HTTPSampler.implementation HTTPSampler.image_parser HTTPSampler.embedded_url_re HTTPSampler.proxyHost HTTPSampler.proxyPort HTTPSampler.proxyUser HTTPSampler.proxyPass HTTPSampler.sourceIp HTTPSampler.sourceType HTTPSampler.use_keepalive HTTPSampler.follow_redirects HTTPSampler.auto_redirects HTTPSampler.DO_MULTIPART_POST HTTPSampler.BROWSER_COMPATIBLE_MULTIPART HTTPSampler.postBodyRaw HTTPSampler.monitor HTTPsampler.Arguments Arguments.arguments HTTPArgument.always_encode HTTPArgument.use_equals HTTPArgument.content_type Argument.name Argument.value Argument.metadata HTTPsampler.Files",
	"HeaderManager":     "HeaderManager.headers Header.name Header.value",
	"Arguments":         "Arguments.arguments Argument.name Argument.value Argument.metadata",
	"CookieManager":     "CookieManager.cookies CookieManager.clearEachIteration CookieManager.policy CookieManager.implementation CookieManager.controlledByThreadGroup",
	"ResponseAssertion": "Asserion.test_strings Assertion.test_field Assertion.test_type Assertion.assume_success Assertion.scope Assertion.variable",
}

func (a *analyzer) checkProperties(t *tree) {
	list, ok := allowed[t.e.kind]
	if !ok {
		return
	}
	names := map[string]bool{"TestPlan.comments": true}
	for _, name := range strings.Fields(list) {
		names[name] = true
	}
	var visit func(*element)
	visit = func(e *element) {
		seen := map[string]bool{}
		for _, p := range e.children {
			if p.kind != "stringProp" && p.kind != "boolProp" && p.kind != "intProp" && p.kind != "longProp" && p.kind != "elementProp" && p.kind != "collectionProp" {
				a.issue(t, "blocked", "Unsupported XML property element: "+p.kind)
				continue
			}
			name := p.attrs["name"]
			structured := map[string]string{
				"TestPlan.user_defined_variables": "elementProp", "HTTPsampler.Arguments": "elementProp", "ThreadGroup.main_controller": "elementProp",
				"Arguments.arguments": "collectionProp", "HeaderManager.headers": "collectionProp", "CookieManager.cookies": "collectionProp", "HTTPsampler.Files": "collectionProp", "Asserion.test_strings": "collectionProp",
			}
			if e.kind == "collectionProp" {
				want := "elementProp"
				if e.attrs["name"] == "Asserion.test_strings" {
					want = "stringProp"
				}
				if p.kind != want {
					a.issue(t, "blocked", "Unexpected collection member type: "+p.kind)
				}
			} else if want := structured[name]; want != "" {
				if p.kind != want {
					a.issue(t, "blocked", "Property must use "+want+": "+name)
				}
			} else if p.kind == "elementProp" || p.kind == "collectionProp" {
				a.issue(t, "blocked", "Unexpected structured property: "+name)
			}
			if p.kind != "elementProp" && p.kind != "collectionProp" && len(p.children) > 0 {
				a.issue(t, "blocked", "Scalar properties cannot contain nested properties: "+name)
			}
			boolNames := " TestPlan.functional_mode TestPlan.serialize_threadgroups TestPlan.tearDown_on_shutdown ThreadGroup.scheduler ThreadGroup.same_user_on_next_iteration ThreadGroup.delayedStart LoopController.continue_forever HTTPSampler.use_keepalive HTTPSampler.follow_redirects HTTPSampler.auto_redirects HTTPSampler.DO_MULTIPART_POST HTTPSampler.BROWSER_COMPATIBLE_MULTIPART HTTPSampler.postBodyRaw HTTPSampler.image_parser HTTPSampler.monitor HTTPArgument.always_encode HTTPArgument.use_equals CookieManager.clearEachIteration CookieManager.controlledByThreadGroup Assertion.assume_success "
			if (p.kind == "boolProp" || strings.Contains(boolNames, " "+name+" ")) && p.text != "true" && p.text != "false" {
				a.issue(t, "blocked", "Boolean property must be explicitly true or false: "+name)
			}
			// Collection members have user-supplied names; their nested properties
			// still pass the component-specific allowlist below.
			if e.kind != "collectionProp" && !names[name] {
				a.issue(t, "blocked", "Unsupported property: "+name)
			}
			if e.kind != "collectionProp" && seen[name] {
				a.issue(t, "blocked", "Duplicate property: "+name)
			}
			seen[name] = true
			if p.kind == "elementProp" {
				typeName := p.attrs["elementType"]
				valid := typeName == "Arguments" || typeName == "Argument" || typeName == "HTTPArgument" || typeName == "Header" || typeName == "LoopController" || typeName == "Cookie"
				if !valid {
					a.issue(t, "blocked", "Unsupported property element type: "+typeName)
				}
				expected := ""
				switch name {
				case "TestPlan.user_defined_variables", "HTTPsampler.Arguments":
					expected = "Arguments"
				case "ThreadGroup.main_controller":
					expected = "LoopController"
				}
				if e.kind == "collectionProp" {
					switch e.attrs["name"] {
					case "HeaderManager.headers":
						expected = "Header"
					case "CookieManager.cookies":
						expected = "Cookie"
					case "Arguments.arguments":
						expected = "Argument"
						if t.e.kind == "HTTPSamplerProxy" || t.e.kind == "ConfigTestElement" {
							expected = "HTTPArgument"
						}
					}
				}
				if expected != "" && typeName != expected {
					a.issue(t, "blocked", "Property element type must be "+expected+": "+name)
				}
			}
			visit(p)
		}
	}
	visit(t.e)
}

var reference = regexp.MustCompile(`\$\{([^{}]+)\}`)
var property = regexp.MustCompile(`^__P\(([A-Za-z0-9_.-]+)(?:,([^(){}]*))?\)$`)

func (a *analyzer) resolve(t *tree, s string, vars bool) string {
	if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		a.issue(t, "blocked", "Literal GoMeter template delimiters in JMX need manual review.")
	}
	result := reference.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if p := property.FindStringSubmatch(name); p != nil {
			value, supplied := a.options.Properties[p[1]]
			if !supplied && strings.Contains(name, ",") {
				value = p[2]
				supplied = true
			}
			input := Input{Name: p[1], Value: value, Supplied: supplied}
			if existing, ok := a.inputs[p[1]]; ok && !existing.Supplied {
				input.Supplied = false
			}
			a.inputs[p[1]] = input
			if !supplied {
				a.issue(t, "input", "Supply JMeter property "+p[1]+". No local JMeter properties or environment values are read.")
				return "gometer-missing-property"
			}
			if strings.Contains(value, "${") || strings.Contains(value, "{{") || strings.Contains(value, "}}") {
				a.issue(t, "blocked", "Property values must be literal strings without template delimiters.")
			}
			return value
		}
		if !correlation.NameValid(name) || strings.HasPrefix(name, "__") {
			a.issue(t, "blocked", "Unsupported function or variable expression: "+match)
			return "gometer-unsupported-expression"
		}
		if vars {
			if value, ok := a.variables[name]; ok {
				return value
			}
			a.issue(t, "blocked", "Undefined variable: "+name)
			return "gometer-undefined-variable"
		}
		return "{{" + name + "}}"
	})
	if strings.Contains(result, "${") {
		a.issue(t, "blocked", "Nested or malformed JMeter expressions are not supported.")
	}
	return result
}
func (a *analyzer) collectVariables(nodes []*tree, disabled bool) {
	for _, t := range nodes {
		if disabled || t.e.attrs["enabled"] == "false" {
			continue
		}
		var containers []*element
		if t.e.kind == "Arguments" {
			containers = append(containers, t.e)
		}
		if t.e.kind == "TestPlan" {
			if p := t.e.prop("TestPlan.user_defined_variables"); p != nil {
				containers = append(containers, p)
			}
		}
		for _, container := range containers {
			for _, e := range container.descendants("elementProp") {
				name, value := e.value("Argument.name"), e.value("Argument.value")
				if !correlation.NameValid(name) || name == "user_id" || name == "iteration" || name == "journey_id" {
					a.issue(t, "blocked", "Invalid or reserved variable name: "+name)
					continue
				}
				if _, exists := a.variables[name]; exists {
					a.issue(t, "blocked", "Repeated variable declarations require manual review: "+name)
					continue
				}
				if strings.Contains(value, "${") {
					value = a.resolve(t, value, true)
				}
				a.variables[name] = value
				a.issue(t, "mapped", "Initial variable: "+name+". Resolved once at import; request references remain editable GoMeter templates.")
			}
		}
		a.collectVariables(t.children, false)
	}
}
func (a *analyzer) unique(name string) string {
	if name == "" {
		name = "Imported step"
	}
	a.names[name]++
	if a.names[name] > 1 {
		return fmt.Sprintf("%s (%d)", name, a.names[name])
	}
	return name
}
func (a *analyzer) group(t *tree) {
	v, err := strconv.Atoi(a.resolve(t, t.e.value("ThreadGroup.num_threads"), true))
	if err != nil || v < 1 || v > 100000 {
		a.issue(t, "blocked", "Thread count must be a literal integer between 1 and 100000.")
	} else {
		a.cfg.VUs = v
	}
	controller := t.e.prop("ThreadGroup.main_controller")
	loops := 0
	if controller != nil {
		loops, err = strconv.Atoi(a.resolve(t, controller.value("LoopController.loops"), true))
	}
	if controller == nil || err != nil || (loops != -1 && (loops < 1 || loops > 1000000)) || controller.value("LoopController.continue_forever") == "true" {
		a.issue(t, "blocked", "Thread-group iterations must be 1–1000000, or -1 with a duration limit. Custom continue-forever controllers require manual review.")
	} else if loops > 0 {
		a.cfg.Iterations = loops
	}
	if t.e.value("ThreadGroup.scheduler") == "true" {
		seconds, err := strconv.Atoi(a.resolve(t, t.e.value("ThreadGroup.duration"), true))
		if err != nil || seconds < 1 || seconds > 86400 {
			a.issue(t, "blocked", "Thread-group duration must be 1–86400 seconds.")
		} else {
			a.cfg.Duration = (time.Duration(seconds) * time.Second).String()
		}
	} else {
		a.cfg.Duration = ""
		if a.cfg.Iterations == 0 {
			a.issue(t, "blocked", "A finite iteration count or scheduler duration is required. Unbounded execution is not imported.")
		}
	}
	for _, setting := range []struct {
		name   string
		target *string
	}{
		{"ThreadGroup.ramp_time", &a.cfg.RampUp},
		{"ThreadGroup.delay", &a.cfg.StartupDelay},
	} {
		value := t.e.value(setting.name)
		if value == "" {
			continue
		}
		if setting.name == "ThreadGroup.delay" && t.e.value("ThreadGroup.scheduler") != "true" {
			a.issue(t, "mapped", "Startup delay is inactive without the scheduler, as in JMeter. Its original value remains in the retained source.")
			continue
		}
		seconds, err := strconv.Atoi(a.resolve(t, value, true))
		if err != nil || seconds < 0 || seconds > 86400 {
			a.issue(t, "blocked", setting.name+": must be 0–86400 seconds.")
		} else if seconds > 0 {
			d := time.Duration(seconds) * time.Second
			if setting.name == "ThreadGroup.ramp_time" {
				// JMeter 5.6.3 rounds the float32 per-thread interval to ms.
				intervalMS := int64(math.Floor(float64(float32(seconds)*1000/float32(a.cfg.VUs)) + .5))
				d = time.Duration(intervalMS) * time.Millisecond * time.Duration(a.cfg.VUs)
				if d > 24*time.Hour {
					a.issue(t, "blocked", "Rounded JMeter user ramp exceeds the native 24h limit.")
					continue
				}
				if d != time.Duration(seconds)*time.Second {
					a.issue(t, "mapped", fmt.Sprintf("JMeter rounds the user-start interval to %dms. The native ramp is %s to preserve that interval, including zero when all users start together.", intervalMS, d))
				}
			}
			if d > 0 {
				*setting.target = d.String()
			}
		}
	}
	switch a.resolve(t, t.e.value("ThreadGroup.on_sample_error"), true) {
	case "startnextloop":
	case "continue":
		a.cfg.OnError = config.ContinueJourney
	case "stopthread":
		a.cfg.OnError = config.StopUser
	case "stoptest":
		a.cfg.OnError = config.StopTest
	case "stoptestnow":
		a.cfg.OnError = config.StopTestNow
	default:
		a.issue(t, "blocked", "Use an explicit Continue, Start Next Thread Loop, Stop Thread, Stop Test, or Stop Test Now failure action.")
	}
	if value := t.e.value("ThreadGroup.same_user_on_next_iteration"); value == "false" {
		a.issue(t, "blocked", "Resetting user state between iterations needs engine support.")
	}
	if t.e.value("ThreadGroup.delayedStart") == "true" {
		a.issue(t, "blocked", "JMeter delayed thread creation uses a different ramp scheduler and still needs a reviewed mapping.")
	}
	if controller != nil && controller.attrs["elementType"] != "LoopController" {
		a.issue(t, "blocked", "Custom thread-group controllers are not supported.")
	}
	if a.report.Components[t.index].Status != "blocked" {
		stop := "for " + a.cfg.Duration
		if a.cfg.Iterations > 0 {
			stop = fmt.Sprintf("for %d journey iteration(s) per user", a.cfg.Iterations)
			if a.cfg.Duration != "" {
				stop += " or " + a.cfg.Duration + ", whichever comes first"
			}
		}
		if a.cfg.RampUp != "" || a.cfg.StartupDelay != "" {
			stop += fmt.Sprintf("; startup delay %s; user ramp %s (first user after startup, remaining starts at ramp × index / users)", defaultZero(a.cfg.StartupDelay), defaultZero(a.cfg.RampUp))
		}
		behavior := "A failed request or assertion stops the current journey and consumes one iteration."
		if a.cfg.OnError == config.ContinueJourney {
			behavior = "Failed requests/assertions remain errors; execution continues with the next request or loop body iteration. Failed verification does not publish extracted values."
		}
		if a.cfg.OnError == config.StopUser {
			behavior = "A failed request or assertion stops that user's current journey and all its future iterations. Other users continue. A stopped user fails the mandatory users_stopped_on_error check."
		}
		if a.cfg.OnError == config.StopTest {
			behavior = "A failure ends the failing journey and stops all new users/iterations. Other admitted journeys finish under their deadlines, including later requests and pauses. This is broader than JMeter's current-sampler graceful stop. The error-triggered test stop fails a mandatory check."
		}
		if a.cfg.OnError == config.StopTestNow {
			behavior = "A failure cancels active workload requests, journey pauses and pending user starts. Partial results are retained and a mandatory check fails. This is a native error-triggered stop, not a manual interruption or a server-side rollback."
		}
		a.issue(t, "mapped", fmt.Sprintf("Converted workload: %d concurrent user(s) %s. %s Admitted journeys drain under GoMeter's runtime policies.", a.cfg.VUs, stop, behavior))
	}
}

func defaultZero(value string) string {
	if value == "" {
		return "0s"
	}
	return value
}

type scope struct {
	defaults   map[string]string
	headers    map[string]string
	assertions []*tree
	cookies    bool
}

func clone(values map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		out[k] = v
	}
	return out
}
func (a *analyzer) scope(parent *tree, in scope) scope {
	out := scope{defaults: clone(in.defaults), headers: clone(in.headers), assertions: append([]*tree{}, in.assertions...), cookies: in.cookies}
	defaults, headers, cookies := 0, 0, 0
	for _, t := range parent.children {
		if t.e.attrs["enabled"] == "false" {
			continue
		}
		switch t.e.kind {
		case "ConfigTestElement":
			defaults++
			if defaults > 1 {
				a.issue(t, "blocked", "Multiple defaults at one scope need manual review.")
			}
			for _, p := range t.e.children {
				if p.kind == "stringProp" || p.kind == "boolProp" {
					if p.text != "" {
						out.defaults[p.attrs["name"]] = p.text
					}
				}
			}
			if args := t.e.prop("HTTPsampler.Arguments"); args != nil && len(args.descendants("elementProp")) > 0 {
				a.issue(t, "blocked", "Arguments on HTTP Request Defaults need manual review.")
			}
			a.httpOptions(t, out.defaults)
			a.issue(t, "mapped", "HTTP defaults are resolved into each request in this scope; there is no separate defaults node in the journey.")
		case "HeaderManager":
			headers++
			if headers > 1 {
				a.issue(t, "blocked", "Multiple header managers at one scope need manual review.")
			}
			seen := map[string]bool{}
			for _, e := range t.e.descendants("elementProp") {
				name := http.CanonicalHeaderKey(e.value("Header.name"))
				if name == "" {
					a.issue(t, "blocked", "Empty header names need manual review.")
					continue
				}
				if e.value("Header.value") == "" {
					a.issue(t, "blocked", "Empty header values can remove inherited JMeter headers; this requires manual review.")
				}
				if seen[name] {
					a.issue(t, "blocked", "Repeated header name: "+name)
				}
				seen[name] = true
				out.headers[name] = a.resolve(t, e.value("Header.value"), false)
				a.issue(t, "mapped", "Scoped request header: "+name+". Child scopes override the same name.")
			}
		case "ResponseAssertion":
			out.assertions = append(out.assertions, t)
		case "CookieManager":
			cookies++
			if cookies > 1 || out.cookies {
				a.issue(t, "blocked", "Overlapping Cookie Managers need manual review.")
			}
			if parent.e.kind != "TestPlan" && parent.e.kind != "ThreadGroup" {
				a.issue(t, "blocked", "Cookie Manager must cover the entire thread group.")
			}
			if t.e.value("CookieManager.clearEachIteration") == "true" || t.e.value("CookieManager.controlledByThreadGroup") == "true" {
				a.issue(t, "blocked", "Cookie reset policies need engine support.")
			}
			if p := t.e.prop("CookieManager.cookies"); p != nil && len(p.children) > 0 {
				a.issue(t, "blocked", "Preloaded cookies need a supported mapping.")
			}
			policy := t.e.value("CookieManager.policy")
			if policy != "" && policy != "standard" {
				a.issue(t, "blocked", "Only the standard cookie policy is eligible for conversion.")
			}
			implementation := t.e.value("CookieManager.implementation")
			if implementation != "" && implementation != "org.apache.jmeter.protocol.http.control.HC4CookieHandler" {
				a.issue(t, "blocked", "Custom cookie implementations are not supported.")
			}
			out.cookies = true
			a.issue(t, "mapped", "Enables one isolated cookie jar per virtual user, retained between journeys. Cookie implementation differences are included in the runtime review.")
			enabled := true
			a.cfg.Cookies = &enabled
		}
	}
	return out
}
func (a *analyzer) sequence(parent *tree, in scope) []config.Step {
	s := a.scope(parent, in)
	var steps []config.Step
	for _, t := range parent.children {
		if t.e.attrs["enabled"] == "false" {
			continue
		}
		switch t.e.kind {
		case "HTTPSamplerProxy":
			steps = append(steps, a.request(t, s))
		case "GenericController":
			steps = append(steps, a.sequence(t, s)...)
		case "LoopController":
			value := a.resolve(t, t.e.value("LoopController.loops"), true)
			count, err := strconv.Atoi(value)
			if err != nil || count < 1 || count > config.MaxLoopIterations || t.e.value("LoopController.continue_forever") == "true" {
				a.issue(t, "blocked", "Only fixed loops with 1–1000 iterations are supported.")
				count = 1
			}
			steps = append(steps, config.Step{Name: a.unique(t.e.attrs["testname"]), Loop: &config.Loop{Count: &count, Steps: a.sequence(t, s)}})
			a.issue(t, "mapped", fmt.Sprintf("Fixed loop: %d body iterations per journey, stopping on a request/assertion failure, deadline, or cancellation.", count))
		}
	}
	return steps
}
func (a *analyzer) httpOptions(t *tree, p map[string]string) {
	for _, name := range []string{"HTTPSampler.follow_redirects", "HTTPSampler.auto_redirects", "HTTPSampler.image_parser", "HTTPSampler.DO_MULTIPART_POST", "HTTPSampler.BROWSER_COMPATIBLE_MULTIPART", "HTTPSampler.monitor"} {
		if p[name] == "true" {
			a.issue(t, "blocked", name+" is not supported.")
		}
	}
	for _, name := range []string{"HTTPSampler.connect_timeout", "HTTPSampler.response_timeout", "HTTPSampler.proxyHost", "HTTPSampler.proxyPort", "HTTPSampler.proxyUser", "HTTPSampler.proxyPass", "HTTPSampler.sourceIp", "HTTPSampler.embedded_url_re"} {
		if p[name] != "" && p[name] != "0" {
			a.issue(t, "blocked", name+" needs a supported mapping; it cannot be silently replaced by GoMeter defaults.")
		}
	}
	if p["HTTPSampler.sourceType"] != "" && p["HTTPSampler.sourceType"] != "0" {
		a.issue(t, "blocked", "Custom source-address selection is unsupported.")
	}
	if p["HTTPSampler.use_keepalive"] == "false" {
		a.issue(t, "blocked", "Disabling HTTP keep-alive needs engine support.")
	}
	if encoding := p["HTTPSampler.contentEncoding"]; encoding != "" && !strings.EqualFold(encoding, "UTF-8") {
		a.issue(t, "blocked", "Only UTF-8 request encoding is supported.")
	}
	if implementation := p["HTTPSampler.implementation"]; implementation != "" && implementation != "HttpClient4" {
		a.issue(t, "blocked", "Custom or Java HTTP implementations need a compatibility adapter.")
	}
	if files := t.e.prop("HTTPsampler.Files"); files != nil && len(files.descendants("elementProp")) > 0 {
		a.issue(t, "blocked", "Referenced upload files are not imported or opened.")
	}
}
func (a *analyzer) request(t *tree, in scope) config.Step {
	s := a.scope(t, in)
	p := clone(s.defaults)
	for _, e := range t.e.children {
		if e.kind == "stringProp" || e.kind == "boolProp" {
			if e.text != "" {
				p[e.attrs["name"]] = e.text
			}
		}
	}
	a.httpOptions(t, p)
	if p["HTTPSampler.follow_redirects"] == "" {
		a.issue(t, "blocked", "Set Follow Redirects explicitly to false; implicit redirect behavior cannot be assumed.")
	}
	method := strings.ToUpper(p["HTTPSampler.method"])
	if method == "" {
		method = "GET"
	}
	step := config.Step{Name: a.unique(t.e.attrs["testname"]), Method: method, Headers: clone(s.headers)}
	// JMeter considers 200–399 successful even when following redirects is off.
	for status := 200; status < 400; status++ {
		step.SuccessStatuses = append(step.SuccessStatuses, status)
	}
	protocol := a.resolve(t, p["HTTPSampler.protocol"], true)
	if protocol == "" {
		protocol = "http"
	}
	domain := a.resolve(t, p["HTTPSampler.domain"], true)
	port := a.resolve(t, p["HTTPSampler.port"], true)
	path := p["HTTPSampler.path"]
	if path == "" {
		path = "/"
	}
	literalPath := a.resolve(t, path, true)
	var target *url.URL
	var err error
	if strings.HasPrefix(literalPath, "http://") || strings.HasPrefix(literalPath, "https://") {
		if strings.Contains(path, "${") {
			a.issue(t, "blocked", "Variables in an absolute sampler URL need manual review.")
		}
		target, err = url.Parse(literalPath)
		if err == nil {
			path = targetPath(target)
		}
	} else {
		host := domain
		if port != "" {
			host = net.JoinHostPort(strings.Trim(domain, "[]"), port)
		}
		if !strings.HasPrefix(literalPath, "/") {
			literalPath = "/" + literalPath
		}
		target, err = url.Parse(protocol + "://" + host + literalPath)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	if err != nil || target == nil {
		a.issue(t, "blocked", "Invalid HTTP request URL.")
		return step
	}
	if _, err = config.ValidateURL(target.String()); err != nil {
		a.issue(t, "blocked", "Request URL: "+err.Error())
	}
	origin := &url.URL{Scheme: target.Scheme, Host: target.Host}
	if a.base == nil {
		a.base = origin
	} else if _, err = config.ResolveStepURL(a.base, target.String()); err != nil {
		a.issue(t, "blocked", "Requests across multiple origins need engine support.")
	}
	step.URL = a.resolve(t, path, false)
	args := t.e.prop("HTTPsampler.Arguments")
	if args != nil {
		if p["HTTPSampler.postBodyRaw"] == "true" && len(args.descendants("elementProp")) > 1 {
			a.issue(t, "blocked", "Multiple raw body argument segments need manual review.")
		}
		var query []string
		for _, arg := range args.descendants("elementProp") {
			name, value := arg.value("Argument.name"), arg.value("Argument.value")
			if p["HTTPSampler.postBodyRaw"] == "true" {
				if name != "" {
					a.issue(t, "blocked", "Raw body arguments must have empty names.")
				}
				step.Body += a.resolve(t, value, false)
				continue
			}
			if method != "GET" {
				a.issue(t, "blocked", "Named form/body arguments need a supported mapping. Raw body mode and GET parameters are supported.")
				continue
			}
			if strings.Contains(name, "${") || strings.Contains(value, "${") {
				a.issue(t, "blocked", "Variable GET arguments require a reviewed URL-encoding mapping.")
			}
			if arg.value("HTTPArgument.always_encode") != "true" || arg.value("HTTPArgument.use_equals") == "false" {
				a.issue(t, "blocked", "GET arguments require URL encoding and an equals separator.")
			}
			query = append(query, url.QueryEscape(name)+"="+url.QueryEscape(value))
		}
		if len(query) > 0 {
			separator := "?"
			if strings.Contains(step.URL, "?") {
				separator = "&"
			}
			step.URL += separator + strings.Join(query, "&")
		}
	}
	for _, assertion := range s.assertions {
		a.assertion(assertion, &step)
	}
	a.issue(t, "mapped", fmt.Sprintf("%s %s. Scoped defaults, headers, and assertions are copied to this request; raw body size: %d bytes before template expansion.", step.Method, step.URL, len(step.Body)))
	return step
}
func targetPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}
func (a *analyzer) assertion(t *tree, step *config.Step) {
	field, kind := t.e.value("Assertion.test_field"), t.e.value("Assertion.test_type")
	if scope := t.e.value("Assertion.scope"); scope != "" && scope != "parent" {
		a.issue(t, "blocked", "Subsample and variable assertion scopes need engine support.")
	}
	patterns := t.e.prop("Asserion.test_strings")
	if patterns == nil || len(patterns.children) != 1 {
		a.issue(t, "blocked", "Exactly one assertion pattern is supported.")
		return
	}
	value := patterns.children[0].text
	if field == "Assertion.response_code" && kind == "8" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 100 || status > 599 {
			a.issue(t, "blocked", "Expected response status must be a literal HTTP status.")
			return
		}
		if (status < 200 || status >= 300) && t.e.value("Assertion.assume_success") != "true" {
			a.issue(t, "blocked", "Non-2xx status assertions require Ignore Status enabled.")
			return
		}
		if len(step.SuccessStatuses) == 1 && step.SuccessStatuses[0] != status {
			a.issue(t, "blocked", "Conflicting response-code assertions need manual review.")
		}
		step.SuccessStatuses = []int{status}
		a.issue(t, "mapped", fmt.Sprintf("Response code must equal %d.", status))
		return
	}
	if field == "Assertion.response_data" && kind == "16" && t.e.value("Assertion.assume_success") != "true" {
		v := a.resolve(t, value, false)
		step.Assertions = append(step.Assertions, config.Assertion{BodyContains: &v})
		a.issue(t, "mapped", "Response body must contain the literal substring. This is not a regular-expression assertion.")
		return
	}
	a.issue(t, "blocked", "Only literal response-body substring and single response-code equality assertions are supported; regex and other modes need engine support.")
}
func (a *analyzer) preview(nodes []*tree) []Preview {
	var result []Preview
	for _, t := range nodes {
		if t.index < 0 {
			continue
		}
		result = append(result, Preview{ID: t.id, Name: a.report.Components[t.index].Name, Kind: t.e.kind, Children: a.preview(t.children)})
	}
	return result
}
