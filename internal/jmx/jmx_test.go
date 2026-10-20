package jmx_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gometer/internal/config"
	"gometer/internal/engine"
	"gometer/internal/jmx"
)

func source(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../examples/jmx/http-smoke.jmx")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func analyze(t *testing.T, src string, opts jmx.Options) jmx.Report {
	t.Helper()
	r, err := jmx.Analyze(strings.NewReader(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSupportedMappingAndRuntimeReview(t *testing.T) {
	r := analyze(t, source(t), jmx.Options{})
	if r.Ready || r.Config != nil || r.Blockers != 0 {
		t.Fatalf("must require runtime review: %+v", r)
	}
	r = analyze(t, source(t), jmx.Options{AcceptRuntime: true})
	if !r.Ready || r.Config == nil {
		t.Fatalf("expected ready: %+v", r)
	}
	c := r.Config
	if c.URL != "http://127.0.0.1:8787" || c.Mode != "users" || c.VUs != 1 || c.Duration != "2s" || c.Cookies == nil || *c.Cookies {
		t.Fatalf("load/cookie settings: %+v", c)
	}
	steps := c.Scenarios[0].Steps
	if steps[0].URL != "/work?item={{item}}" || steps[0].Headers["Accept"] != "application/json" || len(steps[0].SuccessStatuses) != 200 {
		t.Fatalf("request mapping: %+v", steps[0])
	}
	child := steps[1].Loop.Steps[0]
	if *steps[1].Loop.Count != 2 || child.Method != "POST" || child.Body != `{"item":"{{item}}"}` || child.Headers["Content-Type"] != "application/json" || child.Headers["Accept"] != "application/json" || len(child.Assertions) != 1 {
		t.Fatalf("nested mapping: %+v", steps[1])
	}
}

func TestFailureActionMappingAndExecution(t *testing.T) {
	for _, tc := range []struct {
		jmx, action                 string
		attempts, journeys, stopped int64
	}{
		{"startnextloop", config.NextIteration, 2, 2, 0}, {"continue", config.ContinueJourney, 6, 2, 0}, {"stopthread", config.StopUser, 1, 1, 1}, {"stoptest", config.StopTest, 1, 1, 0}, {"stoptestnow", config.StopTestNow, 1, 1, 0},
	} {
		t.Run(tc.jmx, func(t *testing.T) {
			src := strings.Replace(source(t), `<intProp name="LoopController.loops">-1</intProp>`, `<intProp name="LoopController.loops">2</intProp>`, 1)
			src = strings.Replace(src, `<boolProp name="ThreadGroup.scheduler">true</boolProp>`, `<boolProp name="ThreadGroup.scheduler">false</boolProp>`, 1)
			src = strings.Replace(src, "startnextloop", tc.jmx, 1)
			r := analyze(t, src, jmx.Options{AcceptRuntime: true})
			if !r.Ready || r.Config == nil || r.Config.ErrorAction() != tc.action {
				t.Fatalf("failure mapping: %+v", r)
			}
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					http.Error(w, "failed", 503)
				} else {
					fmt.Fprint(w, `{"ok":true}`)
				}
			}))
			defer target.Close()
			r.Config.URL = target.URL
			result, err := engine.Run(context.Background(), *r.Config, engine.Options{})
			if err != nil || result.Passed || result.Completed != tc.attempts || result.Journeys.Completed != tc.journeys || result.Journeys.Failed != tc.journeys {
				t.Fatalf("converted execution: %v %+v", err, result)
			}
			if r.Config.HasTestStopPolicy() && (result.SchemaVersion != 10 || !result.FailurePolicy.TestStopped) {
				t.Fatalf("test stop missing: %+v", result)
			}
			if tc.action != config.NextIteration && result.FailurePolicy.UsersStopped != tc.stopped {
				t.Fatalf("stopped users: %+v", result)
			}
		})
	}
}
func TestUnsupportedBehaviorNeverProducesConfiguration(t *testing.T) {
	src := source(t)
	for name, pair := range map[string][2]string{
		"negative ramp":          {"<stringProp name=\"ThreadGroup.ramp_time\">0</stringProp>", "<stringProp name=\"ThreadGroup.ramp_time\">-1</stringProp>"},
		"unknown failure action": {"startnextloop", "unknown"},
		"missing failure action": {`<stringProp name="ThreadGroup.on_sample_error">startnextloop</stringProp>`, ""},
		"redirects":              {"<boolProp name=\"HTTPSampler.follow_redirects\">false</boolProp>", "<boolProp name=\"HTTPSampler.follow_redirects\">true</boolProp>"},
		"sampler timeout":        {"<stringProp name=\"HTTPSampler.method\">GET</stringProp>", "<stringProp name=\"HTTPSampler.response_timeout\">3000</stringProp><stringProp name=\"HTTPSampler.method\">GET</stringProp>"},
		"unknown property":       {"<stringProp name=\"HTTPSampler.method\">GET</stringProp>", "<boolProp name=\"HTTPSampler.new_behavior\">false</boolProp><stringProp name=\"HTTPSampler.method\">GET</stringProp>"},
		"unknown sampler":        {"HTTPSamplerProxy", "org.example.PluginSampler"},
		"custom function":        {"${item}", "${__groovy(1+1)}"},
		"regex assertion":        {"<intProp name=\"Assertion.test_type\">16</intProp>", "<intProp name=\"Assertion.test_type\">2</intProp>"},
		"custom config":          {"HttpDefaultsGui", "OtherConfigGui"},
		"test class spoof":       {"testclass=\"HTTPSamplerProxy\"", "testclass=\"PluginSampler\""},
		"invalid boolean":        {`<boolProp name="HTTPSampler.follow_redirects">false</boolProp>`, `<boolProp name="HTTPSampler.follow_redirects">maybe</boolProp>`},
		"wrong property type":    {`<stringProp name="Header.value">application/json</stringProp>`, `<collectionProp name="Header.value"/>`},
		"empty header":           {`<stringProp name="Header.value">application/json</stringProp>`, `<stringProp name="Header.value"></stringProp>`},
		"duplicate property":     {`<stringProp name="HTTPSampler.method">GET</stringProp>`, `<stringProp name="HTTPSampler.method">GET</stringProp><stringProp name="HTTPSampler.method">POST</stringProp>`},
		"cross origin":           {`/work?item=${item}`, `https://example.test/work`},
		"absolute URL variable":  {`/work?item=${item}`, `http://127.0.0.1:8787/work?item=${item}`},
		"other baseline":         {`jmeter="5.6.3"`, `jmeter="5.5"`},
	} {
		t.Run(name, func(t *testing.T) {
			modified := strings.Replace(src, pair[0], pair[1], 1)
			if name == "unknown sampler" {
				modified = strings.ReplaceAll(src, pair[0], pair[1])
			}
			r := analyze(t, modified, jmx.Options{AcceptRuntime: true})
			if r.Ready || r.Config != nil || r.Blockers == 0 {
				t.Fatalf("silently imported: %+v", r)
			}
		})
	}
}

func TestFiniteThreadGroupIterations(t *testing.T) {
	base := strings.Replace(source(t), `<intProp name="LoopController.loops">-1</intProp>`, `<intProp name="LoopController.loops">${__P(iterations,5)}</intProp>`, 1)
	for _, scheduler := range []string{"true", "false"} {
		src := strings.Replace(base, `<boolProp name="ThreadGroup.scheduler">true</boolProp>`, `<boolProp name="ThreadGroup.scheduler">`+scheduler+`</boolProp>`, 1)
		r := analyze(t, src, jmx.Options{AcceptRuntime: true, Properties: map[string]string{"iterations": "4"}})
		if !r.Ready || r.Config == nil || r.Config.Iterations != 4 || (r.Config.Duration != "") != (scheduler == "true") {
			t.Fatalf("scheduler %s: %+v", scheduler, r)
		}
		var attempts int
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts++; fmt.Fprint(w, `{"ok":true}`) }))
		r.Config.URL = s.URL
		result, err := engine.Run(context.Background(), *r.Config, engine.Options{})
		s.Close()
		if err != nil || !result.Passed || result.Journeys.Completed != 4 || result.Completed != 12 || attempts != 12 || result.StopReason != "iterations" {
			t.Fatalf("finite converted plan: attempts=%d err=%v result=%+v", attempts, err, result)
		}
	}
	for _, loops := range []string{"0", "-2", "1000001", "invalid"} {
		src := strings.Replace(source(t), `<intProp name="LoopController.loops">-1</intProp>`, `<intProp name="LoopController.loops">`+loops+`</intProp>`, 1)
		if r := analyze(t, src, jmx.Options{AcceptRuntime: true}); r.Ready || r.Config != nil || r.Blockers == 0 {
			t.Fatalf("invalid count %s imported", loops)
		}
	}
	src := strings.Replace(source(t), `<boolProp name="ThreadGroup.scheduler">true</boolProp>`, `<boolProp name="ThreadGroup.scheduler">false</boolProp>`, 1)
	if r := analyze(t, src, jmx.Options{AcceptRuntime: true}); r.Ready || r.Config != nil || r.Blockers == 0 {
		t.Fatal("unbounded thread group imported")
	}
}

func TestUserRampAndStartupMapping(t *testing.T) {
	src := strings.Replace(source(t), `<intProp name="LoopController.loops">-1</intProp>`, `<intProp name="LoopController.loops">1</intProp>`, 1)
	src = strings.Replace(src, `<stringProp name="ThreadGroup.num_threads">1</stringProp>`, `<stringProp name="ThreadGroup.num_threads">2</stringProp>`, 1)
	src = strings.Replace(src, `<stringProp name="ThreadGroup.ramp_time">0</stringProp>`, `<stringProp name="ThreadGroup.ramp_time">${__P(ramp,1)}</stringProp>`, 1)
	src = strings.Replace(src, `<stringProp name="ThreadGroup.delay">0</stringProp>`, `<stringProp name="ThreadGroup.delay">1</stringProp>`, 1)
	r := analyze(t, src, jmx.Options{AcceptRuntime: true})
	if !r.Ready || r.Config == nil || r.Config.VUs != 2 || r.Config.RampUp != "1s" || r.Config.StartupDelay != "1s" {
		t.Fatalf("ramp mapping: %+v", r)
	}
	var mu sync.Mutex
	var reads []time.Time
	attempts := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		if r.Method == "GET" {
			reads = append(reads, time.Now())
		}
		mu.Unlock()
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer target.Close()
	r.Config.URL = target.URL
	result, err := engine.Run(context.Background(), *r.Config, engine.Options{})
	if err != nil || !result.Passed || result.Journeys.Completed != 2 || attempts != 6 || result.UserSchedule.Users.Started != 2 || len(reads) != 2 {
		t.Fatalf("ramped execution: %v %+v", err, result)
	}
	for i, at := range reads {
		if at.Before(result.StartedAt.Add(time.Second + time.Duration(i)*500*time.Millisecond)) {
			t.Fatal("converted user started before scheduled offset")
		}
	}
	// The source scheduler ignores startup delay when disabled, including properties.
	unscheduled := strings.Replace(src, `<boolProp name="ThreadGroup.scheduler">true</boolProp>`, `<boolProp name="ThreadGroup.scheduler">false</boolProp>`, 1)
	unscheduled = strings.Replace(unscheduled, `<stringProp name="ThreadGroup.delay">1</stringProp>`, `<stringProp name="ThreadGroup.delay">${__P(unused_delay)}</stringProp>`, 1)
	if r := analyze(t, unscheduled, jmx.Options{AcceptRuntime: true}); !r.Ready || r.Config.StartupDelay != "" || r.MissingInputs != 0 {
		t.Fatalf("inactive scheduler delay: %+v", r)
	}
}

func TestJMeterRampRoundingAndUnsupportedSchedules(t *testing.T) {
	base := source(t)
	for _, tc := range []struct{ users, ramp, want string }{{"3", "1", "999ms"}, {"100000", "1", ""}, {"4", "4", "4s"}} {
		src := strings.Replace(base, `<stringProp name="ThreadGroup.num_threads">1</stringProp>`, `<stringProp name="ThreadGroup.num_threads">`+tc.users+`</stringProp>`, 1)
		src = strings.Replace(src, `<stringProp name="ThreadGroup.ramp_time">0</stringProp>`, `<stringProp name="ThreadGroup.ramp_time">`+tc.ramp+`</stringProp>`, 1)
		r := analyze(t, src, jmx.Options{AcceptRuntime: true})
		if !r.Ready || r.Config.RampUp != tc.want {
			t.Fatalf("rounding %+v: %+v", tc, r)
		}
	}
	for _, setting := range []string{"ThreadGroup.ramp_time", "ThreadGroup.delay"} {
		for _, value := range []string{"-1", "86401", "1.5", "bad"} {
			src := strings.Replace(base, `<stringProp name="`+setting+`">0</stringProp>`, `<stringProp name="`+setting+`">`+value+`</stringProp>`, 1)
			if r := analyze(t, src, jmx.Options{AcceptRuntime: true}); r.Config != nil || r.Blockers == 0 {
				t.Fatalf("invalid %s=%s imported", setting, value)
			}
		}
	}
	src := strings.Replace(base, `<boolProp name="ThreadGroup.scheduler">true</boolProp>`, `<boolProp name="ThreadGroup.delayedStart">true</boolProp><boolProp name="ThreadGroup.scheduler">true</boolProp>`, 1)
	if r := analyze(t, src, jmx.Options{AcceptRuntime: true}); r.Config != nil || r.Blockers == 0 {
		t.Fatal("different delayed-start scheduler imported")
	}
}
func TestPropertiesAndDisabledSubtrees(t *testing.T) {
	src := strings.Replace(source(t), "${__P(host,127.0.0.1)}", "${__P(host)}", 1)
	r := analyze(t, src, jmx.Options{AcceptRuntime: true})
	if r.Ready || r.MissingInputs != 1 || r.Config != nil {
		t.Fatalf("missing property: %+v", r)
	}
	r = analyze(t, src, jmx.Options{AcceptRuntime: true, Properties: map[string]string{"host": "example.test"}})
	if !r.Ready || r.Config.URL != "http://example.test:8787" {
		t.Fatalf("resolved property: %+v", r)
	}
	// A later occurrence with a default cannot hide an earlier missing property.
	src = strings.Replace(src, "${__P(port,8787)}", "${__P(host,8787)}", 1)
	r = analyze(t, src, jmx.Options{AcceptRuntime: true})
	if r.MissingInputs != 1 || r.Config != nil {
		t.Fatalf("missing input masked by later default: %+v", r)
	}
	base := source(t)
	at := strings.Index(base, "      <ConfigTestElement")
	src = base[:at] + `<JSR223Sampler testname="Disabled script" enabled="false"/><hashTree><CSVDataSet testname="Private file" enabled="true"/><hashTree/></hashTree>` + base[at:]
	r = analyze(t, src, jmx.Options{AcceptRuntime: true})
	if !r.Ready {
		t.Fatalf("disabled subtree: %+v", r)
	}
	found := false
	for _, c := range r.Components {
		if c.Name == "Disabled script" {
			found = c.Status == "ignored"
		}
		if c.Name == "Private file" {
			t.Fatal("disabled descendants were processed")
		}
	}
	if !found {
		t.Fatal("disabled component not retained")
	}
}
func TestMalformedAndBoundedXML(t *testing.T) {
	for _, src := range []string{"", `<jmeterTestPlan version="1.2"><hashTree/></jmeterTestPlan>`, source(t) + source(t), `<!DOCTYPE x [<!ENTITY secret SYSTEM "file:///etc/passwd">]>` + source(t), strings.Replace(source(t), `version="1.2"`, `version="1.2" version="1.2"`, 1), strings.Replace(source(t), `<hashTree>`, `<hashTree xmlns="urn:other">`, 1), strings.Repeat(" ", jmx.MaxSourceBytes+1)} {
		if _, err := jmx.Analyze(strings.NewReader(src), jmx.Options{}); err == nil {
			t.Fatal("accepted invalid/unsafe XML")
		}
	}
	r := analyze(t, strings.Replace(source(t), "/work?item=${item}", "http://[invalid", 1), jmx.Options{AcceptRuntime: true})
	if r.Blockers == 0 || r.Config != nil {
		t.Fatal("malformed absolute URL accepted")
	}
}

func TestScopedCookieManagerAndMisplacedExecution(t *testing.T) {
	base := source(t)
	at := strings.Index(base, "      <ConfigTestElement")
	cookie := `<CookieManager testname="Session cookies" enabled="true"><collectionProp name="CookieManager.cookies"/><boolProp name="CookieManager.clearEachIteration">false</boolProp><stringProp name="CookieManager.policy">standard</stringProp></CookieManager><hashTree/>`
	r := analyze(t, base[:at]+cookie+base[at:], jmx.Options{AcceptRuntime: true})
	if !r.Ready || r.Config.Cookies == nil || !*r.Config.Cookies {
		t.Fatalf("cookie mapping: %+v", r)
	}
	// An executable tree outside a ThreadGroup must never be silently skipped.
	misplaced := `<GenericController testname="Outside group" enabled="true"/><hashTree/>`
	r = analyze(t, base[:at]+misplaced+base[at:], jmx.Options{AcceptRuntime: true})
	if r.Ready || r.Blockers == 0 || r.Config != nil {
		t.Fatal("misplaced execution was silently skipped")
	}
}

func TestXMLStructuralLimitsAndSplitText(t *testing.T) {
	base := source(t)
	at := strings.Index(base, "      <ConfigTestElement")
	tooMany := strings.Repeat(`<GenericController enabled="false"/><hashTree/>`, 501)
	if _, err := jmx.Analyze(strings.NewReader(base[:at]+tooMany+base[at:]), jmx.Options{}); err == nil {
		t.Fatal("component limit not enforced")
	}
	deep := strings.Repeat(`<GenericController enabled="false"/><hashTree>`, 65) + strings.Repeat(`</hashTree>`, 65)
	if _, err := jmx.Analyze(strings.NewReader(base[:at]+deep+base[at:]), jmx.Options{}); err == nil {
		t.Fatal("XML depth limit not enforced")
	}
	// Comments split decoder character-data tokens without changing the value.
	r := analyze(t, strings.Replace(base, ">coffee</stringProp>", ">co<!-- split -->ff<![CDATA[ee]]></stringProp>", 1), jmx.Options{AcceptRuntime: true})
	if !r.Ready || r.Config.Scenarios[0].Variables["item"] != "coffee" {
		t.Fatal("split text changed variable content")
	}
}
func TestImportedPlanRequestContentsCountsAndNoImplicitCookies(t *testing.T) {
	var mu sync.Mutex
	var failures []string
	var received int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		received++
		if r.Header.Get("Cookie") != "" {
			failures = append(failures, "cookies were added without a Cookie Manager")
		}
		if r.Header.Get("Accept") != "application/json" {
			failures = append(failures, "scope header missing")
		}
		if r.Method == "GET" && r.RequestURI != "/work?item=coffee" {
			failures = append(failures, "GET URL changed: "+r.RequestURI)
		}
		if r.Method == "POST" && (string(body) != `{"item":"coffee"}` || r.Header.Get("Content-Type") != "application/json") {
			failures = append(failures, "POST contents changed")
		}
		http.SetCookie(w, &http.Cookie{Name: "unexpected", Value: "cookie", Path: "/"})
		time.Sleep(3 * time.Millisecond)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	r := analyze(t, source(t), jmx.Options{AcceptRuntime: true, Properties: map[string]string{"host": u.Hostname(), "port": u.Port()}})
	if !r.Ready {
		t.Fatalf("blocked: %+v", r)
	}
	r.Config.Duration = "1s"
	result, err := engine.Run(context.Background(), *r.Config, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failures) > 0 {
		t.Fatal(failures)
	}
	if result.Completed != received || result.Failed != 0 || result.Completed != 3*result.Journeys.Completed || result.Journeys.Completed < 1 {
		t.Fatalf("observed=%d, result=%+v", received, result)
	}
}
