package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gometer/internal/config"
)

func TestShardsCoverFractionalPhasesAndGlobalJourneyIDs(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := config.Defaults()
	c.URL = target.URL
	c.Mode = "rate"
	c.MaxInFlight = 3
	c.Stages = []config.Stage{{Duration: "350ms", Rate: 7}, {Duration: "50ms", Rate: 0}, {Duration: "650ms", Rate: 11}}
	c.ArrivalDistribution = "jitter"
	c.Scenarios = []config.Scenario{{Name: "Flow", Steps: []config.Step{{URL: "/{{journey_id}}"}}}}
	start := time.Now().Add(150 * time.Millisecond)
	results := make([]Result, 3)
	errs := make([]error, 3)
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = Run(context.Background(), c, Options{Shard: &Assignment{Index: i, Count: 3, Capacity: 1, StartAt: start}, Capture: true})
		}(i)
	}
	wg.Wait()
	p, _ := c.Validate()
	var total int64
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if r.Journeys.Scheduled != PlannedForWorker(p, i, 3) || !r.Passed {
			t.Fatalf("assignment %d: %+v", i, r)
		}
		total += r.Journeys.Completed
	}
	if total != 11 || len(seen) != 11 {
		t.Fatalf("duplicated or omitted arrivals: completed=%d seen=%v", total, seen)
	}
	for i := 1; i <= 11; i++ {
		if seen[fmt.Sprintf("/%d", i)] != 1 {
			t.Fatalf("journey %d not dispatched exactly once", i)
		}
	}
	combined, err := Combine(c, start, p.LoadDuration, p.LoadDuration, results, Distribution{Complete: true, PlannedArrivals: 11}, false)
	if err != nil || !combined.Passed || combined.Journeys.Completed != 11 || combined.HTTP.FirstByte.Samples != 11 {
		t.Fatalf("merged result: %+v %v", combined, err)
	}
}

func TestLateShardsAttributeOnlyTheirOwnMissedDemand(t *testing.T) {
	c := config.Defaults()
	c.URL = "http://127.0.0.1:1"
	c.Mode = "rate"
	c.MaxInFlight = 3
	c.Rate = 11
	c.Duration = "650ms"
	three, one := 3, 1
	c.Scenarios = []config.Scenario{{Name: "A", Weight: &three, Steps: []config.Step{{URL: "/a"}}}, {Name: "B", Weight: &one, Steps: []config.Step{{URL: "/b"}}}}
	p, _ := c.Validate()
	var scheduled int64
	scenario := []int64{0, 0}
	for i := range 3 {
		r, err := Run(context.Background(), c, Options{Shard: &Assignment{Index: i, Count: 3, Capacity: 1, StartAt: time.Now().Add(-time.Second)}})
		if err != nil {
			t.Fatal(err)
		}
		if r.Journeys.Started != 0 || r.Journeys.DroppedScheduler != PlannedForWorker(p, i, 3) || r.HTTP != nil {
			t.Fatalf("late shard invented traffic: %+v", r)
		}
		scheduled += r.Journeys.Scheduled
		for j, s := range r.Scenarios {
			scenario[j] += s.Journeys.Scheduled
		}
	}
	if scheduled != 8 || scenario[0] != 6 || scenario[1] != 2 {
		t.Fatalf("missed global mix changed: %d %v", scheduled, scenario)
	}
}

func TestControlFlowWorkerMergePreservesRepeatedAttempts(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	c := journeyConfig(target.URL, config.Step{Name: "Repeat", Loop: &config.Loop{Count: pointer(3), Steps: []config.Step{{Name: "Request", URL: "/"}}}}, config.Step{Name: "Choose", If: &config.Branch{Condition: config.Condition{Variable: "journey_id", Value: pointer("1")}, Then: []config.Step{{Name: "First", URL: "/first"}}, Else: []config.Step{{Name: "Other", URL: "/other"}}}})
	c.Rate = 4
	c.Duration = "500ms"
	c.MaxInFlight = 2
	start := time.Now().Add(50 * time.Millisecond)
	parts := make([]Result, 2)
	var wg sync.WaitGroup
	for i := range parts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			parts[i], err = Run(context.Background(), c, Options{Capture: true, Shard: &Assignment{Index: i, Count: 2, Capacity: 1, StartAt: start}})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	result, err := Combine(c, start, 500*time.Millisecond, 500*time.Millisecond, parts, Distribution{Complete: true, PlannedArrivals: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJourneyAccounting(t, result)
	if err = result.ValidateFlow(mustPlan(t, c)); err != nil {
		t.Fatal(err)
	}
	s := result.Scenarios[0]
	if result.SchemaVersion != 6 || result.Completed != 8 || s.Steps[0].ExecutedJourneys != 2 || s.Steps[0].Completed != 6 || s.Controls[0].Iterations != 6 || s.Controls[1].ConditionTrue != 1 || s.Controls[1].ConditionFalse != 1 {
		t.Fatalf("%+v", result)
	}
	parts[0].Merge.Scenarios[0].Executed[0]++
	if _, err = Combine(c, start, 500*time.Millisecond, 500*time.Millisecond, parts, Distribution{}, false); err == nil {
		t.Fatal("tampered flow counts accepted")
	}
}
