package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"gometer/internal/config"
)

func TestControlCountersRejectImpossibleOrOverflowingClaims(t *testing.T) {
	for _, tt := range []struct {
		plan   config.ControlPlan
		counts ControlCounts
		valid  bool
	}{
		{config.ControlPlan{Kind: "branch"}, ControlCounts{Entered: 1, Completed: 1, ConditionTrue: 1}, true},
		{config.ControlPlan{Kind: "branch"}, ControlCounts{Entered: 1, Completed: 1, ConditionTrue: 2}, false},
		{config.ControlPlan{Kind: "loop", Count: 2, MaxIterations: 2}, ControlCounts{Entered: 1, Completed: 1, Iterations: 2}, true},
		{config.ControlPlan{Kind: "loop", Count: 2, MaxIterations: 2}, ControlCounts{Entered: 1, Completed: 1, Iterations: 1}, false},
		{config.ControlPlan{Kind: "loop", MaxIterations: 3}, ControlCounts{Entered: 1, Completed: 1, Iterations: 4, ConditionTrue: 4, ConditionFalse: 1}, false},
		{config.ControlPlan{Kind: "loop", MaxIterations: 3}, ControlCounts{Entered: 1, Failed: 1, Iterations: 3, ConditionTrue: 4}, true},
	} {
		if got := tt.counts.validFor(tt.plan); got != tt.valid {
			t.Fatalf("%+v got %v", tt, got)
		}
	}
	if !withinVisits(1<<62, 1<<62, 10000) || withinVisits(1, 0, 10000) {
		t.Fatal("overflow-safe comparison failed")
	}
}

func TestFlowCancellationStopsEnclosingLoop(t *testing.T) {
	received := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer target.Close()
	c := journeyConfig(target.URL, config.Step{Name: "Repeat", Loop: &config.Loop{Count: pointer(1000), Steps: []config.Step{{Name: "Wait", URL: "/"}}}}, config.Step{Name: "Later", URL: "/later"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		r, err := Run(ctx, c, Options{})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	select {
	case <-received:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("no request")
	}
	select {
	case r := <-done:
		if !r.Interrupted || r.Journeys.Errors["canceled"] != 1 || r.Scenarios[0].Controls[0].Failed != 1 || r.Scenarios[0].Steps[1].Skipped != 1 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not finish")
	}
}

func TestNestedFlowExecutesSelectedPathsAndRestoresCounters(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := journeyConfig(target.URL,
		config.Step{Name: "Outer", Loop: &config.Loop{Count: pointer(2), IndexVariable: "item", Steps: []config.Step{
			{Name: "Choose", If: &config.Branch{Condition: config.Condition{Variable: "item", Value: pointer("1")}, Then: []config.Step{{Name: "First", URL: "/first/{{item}}"}}, Else: []config.Step{{Name: "Other", URL: "/other/{{item}}"}}}},
			{Name: "Inner", Loop: &config.Loop{Count: pointer(2), IndexVariable: "item", Steps: []config.Step{{Name: "Repeat", URL: "/inner/{{item}}"}}}},
			{Name: "Restored", URL: "/outer/{{item}}"},
		}}}, config.Step{Name: "Finish", URL: "/finish/{{item}}"})
	c.Variables = map[string]string{"item": "original"}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	want := []string{"/first/1", "/inner/1", "/inner/2", "/outer/1", "/other/2", "/inner/1", "/inner/2", "/outer/2", "/finish/original"}
	if !reflect.DeepEqual(paths, want) || r.SchemaVersion != 6 || r.Journeys.Succeeded != 1 {
		t.Fatalf("paths=%v result=%+v", paths, r)
	}
	if err := r.ValidateFlow(mustPlan(t, c)); err != nil {
		t.Fatal(err)
	}
	s := r.Scenarios[0]
	if s.Steps[2].Completed != 4 || s.Steps[2].ExecutedJourneys != 1 || s.Controls[1].ConditionTrue != 1 || s.Controls[1].ConditionFalse != 1 || s.Controls[2].Iterations != 4 {
		t.Fatalf("counts=%+v", s)
	}
}
func mustPlan(t *testing.T, c config.Config) config.Plan {
	t.Helper()
	p, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWhileFlowTerminationAndFailures(t *testing.T) {
	for _, mode := range []string{"terminate", "limit", "numeric", "zero", "request", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if mode == "request" {
					http.Error(w, "fail", 500)
					return
				}
				if mode == "deadline" {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(time.Second):
						return
					}
				}
				more := "true"
				if mode == "terminate" && n == 2 {
					more = "false"
				}
				w.Header().Set("X-More", more)
				fmt.Fprint(w, "ok")
			}))
			defer target.Close()
			condition := config.Condition{Variable: "more", Value: pointer("true")}
			if mode == "numeric" {
				condition.Operator = "gt"
				condition.Value = pointer("1")
			}
			c := journeyConfig(target.URL, config.Step{Name: "Poll", Loop: &config.Loop{While: &condition, MaxIterations: 3, Steps: []config.Step{{Name: "Fetch", URL: "/", Extract: map[string]config.Extraction{"more": {Header: "X-More"}}}}}}, config.Step{Name: "Done", URL: "/done"})
			c.Variables = map[string]string{"more": "true"}
			if mode == "zero" {
				c.Variables["more"] = "false"
			}
			if mode == "deadline" {
				c.JourneyTimeout = "10ms"
			}
			r := runTest(t, c)
			assertJourneyAccounting(t, r)
			if err := r.ValidateFlow(mustPlan(t, c)); err != nil {
				t.Fatal(err)
			}
			s := r.Scenarios[0]
			control := s.Controls[0]
			switch mode {
			case "terminate":
				if r.Completed != 3 || r.Journeys.Succeeded != 1 || control.Iterations != 2 || control.ConditionFalse != 1 {
					t.Fatalf("%+v", r)
				}
			case "zero":
				if r.Completed != 1 || s.Steps[0].SkippedCondition != 1 || control.Iterations != 0 || r.Journeys.Succeeded != 1 {
					t.Fatalf("%+v", r)
				}
			case "limit":
				if r.Completed != 3 || r.Journeys.Errors["loop_limit"] != 1 || control.ConditionTrue != 4 || s.Steps[1].Skipped != 1 {
					t.Fatalf("%+v", r)
				}
			case "numeric":
				if r.Completed != 0 || r.Journeys.Errors["condition"] != 1 || control.ConditionErrors != 1 {
					t.Fatalf("%+v", r)
				}
			case "request":
				if r.Completed != 1 || r.Journeys.Errors["step_failed"] != 1 || control.Failed != 1 {
					t.Fatalf("%+v", r)
				}
			case "deadline":
				if r.Journeys.Errors["timeout"] != 1 || control.Failed != 1 || s.Steps[1].Skipped != 1 {
					t.Fatalf("%+v", r)
				}
			}
		})
	}
}

func TestBranchBypassesAreNotHTTPFailures(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	c := journeyConfig(target.URL, config.Step{Name: "Choose", If: &config.Branch{Condition: config.Condition{Variable: "journey_id", Value: pointer("1")}, Then: []config.Step{{Name: "Yes", URL: "/yes"}}, Else: []config.Step{{Name: "No", URL: "/no"}}}})
	r := runTest(t, c)
	s := r.Scenarios[0]
	if r.Completed != 1 || r.Journeys.Succeeded != 1 || s.Steps[1].Skipped != 1 || s.Steps[1].SkippedCondition != 1 || s.Steps[1].Latency.Samples != 0 {
		t.Fatalf("%+v", r)
	}
	// Imported counters must not claim more journeys reached a node than attempts.
	r.Scenarios[0].Steps[0].ExecutedJourneys = 2
	if err := r.ValidateFlow(mustPlan(t, c)); err == nil {
		t.Fatal("tampered result accepted")
	}
}
