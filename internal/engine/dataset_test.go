package engine

import (
	"context"
	"fmt"
	"gometer/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func csvConfig(target string) config.Config {
	c := finiteConfig(target)
	c.VUs = 4
	c.Iterations = 5
	c.Scenarios = []config.Scenario{{Name: "Data", CSV: &config.CSV{Name: "people.csv", Content: "name\nA\nB"}, Steps: []config.Step{{Name: "Read", URL: "/?name={{name}}&user={{user_id}}"}}}}
	return c
}

func TestCSVRowSharingRecyclingAndNormalExhaustion(t *testing.T) {
	for _, sharing := range []string{"shared", "per_user"} {
		for _, eof := range []string{"recycle", "stop_user"} {
			t.Run(sharing+"/"+eof, func(t *testing.T) {
				var mu sync.Mutex
				seen := map[string][]string{}
				rows := map[string]int{}
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					user, name := r.URL.Query().Get("user"), r.URL.Query().Get("name")
					seen[user] = append(seen[user], name)
					rows[name]++
				}))
				defer s.Close()
				c := csvConfig(s.URL)
				c.Scenarios[0].CSV.Sharing = sharing
				c.Scenarios[0].CSV.OnEOF = eof
				result := runTest(t, c)
				want := int64(20)
				exhausted := int64(0)
				reason := "iterations"
				if eof == "stop_user" {
					want = 2
					if sharing == "per_user" {
						want = 8
					}
					exhausted = 4
					reason = "data_exhausted"
				}
				if !result.Passed || result.SchemaVersion != 11 || result.StopReason != reason || result.Journeys.Completed != want || result.Completed != want || result.Datasets[0].RowsAssigned != want || result.Datasets[0].UsersExhausted != exhausted || result.Journeys.Failed != 0 || result.Journeys.DroppedCapacity+result.Journeys.DroppedScheduler != 0 {
					t.Fatalf("CSV result: %+v", result)
				}
				if sharing == "per_user" {
					for _, sequence := range seen {
						for i, row := range sequence {
							if row != []string{"A", "B"}[i%2] {
								t.Fatalf("sequence %v", sequence)
							}
						}
					}
				} else if rows["A"] != int(want/2) || rows["B"] != int(want/2) {
					t.Fatalf("shared rows: %v", rows)
				}
				p, _ := c.Validate()
				if err := result.ValidateIterations(p); err != nil {
					t.Fatal(err)
				}
				assertJourneyAccounting(t, result)
			})
		}
	}
}

func TestCSVPerUserCursorsArePerScenarioAndLoopRowsAreStable(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen[r.URL.Path] = append(seen[r.URL.Path], r.URL.Query().Get("name"))
	}))
	defer s.Close()
	c := csvConfig(s.URL)
	c.VUs = 1
	c.Iterations = 6
	c.Scenarios[0].CSV.Sharing = "per_user"
	c.Scenarios[0].Steps = []config.Step{{Name: "Repeat", Loop: &config.Loop{Count: csvPtr(2), Steps: []config.Step{{Name: "A", URL: "/a?name={{name}}"}}}}}
	other := c.Scenarios[0]
	other.Name = "Other"
	other.CSV = &config.CSV{Content: "name\nX\nY", Sharing: "per_user"}
	other.Steps = []config.Step{{Name: "B", URL: "/b?name={{name}}"}}
	c.Scenarios = append(c.Scenarios, other)
	result := runTest(t, c)
	if !result.Passed || strings.Join(seen["/a"], "") != "AABBAA" || strings.Join(seen["/b"], "") != "XYX" || result.Datasets[0].RowsAssigned != 3 || result.Datasets[1].RowsAssigned != 3 {
		t.Fatalf("weighted loop data: %v %+v", seen, result)
	}
	p, _ := c.Validate()
	if err := result.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestCSVFailedJourneyConsumesRowAndRetainsFailurePolicies(t *testing.T) {
	for _, action := range []string{config.NextIteration, config.ContinueJourney, config.StopUser, config.StopTest, config.StopTestNow} {
		t.Run(action, func(t *testing.T) {
			var mu sync.Mutex
			var names []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := r.URL.Query().Get("name")
				mu.Lock()
				names = append(names, name)
				mu.Unlock()
				if name == "A" {
					http.Error(w, "failure", 503)
				}
			}))
			defer s.Close()
			c := csvConfig(s.URL)
			c.VUs = 1
			c.Iterations = 3
			c.OnError = action
			c.Scenarios[0].CSV.OnEOF = "stop_user"
			c.Variables = map[string]string{"name": "wrong"}
			r := runTest(t, c)
			want := 2
			if action == config.StopUser || c.HasTestStopPolicy() {
				want = 1
			}
			if r.Passed || r.Completed != int64(want) || r.Datasets[0].RowsAssigned != int64(want) || r.Journeys.Failed != 1 || strings.Join(names, "") != "AB"[:want] {
				t.Fatalf("failure data: %v %+v", names, r)
			}
			p, _ := c.Validate()
			if err := r.ValidateIterations(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCSVExhaustionDoesNotCancelLaterRampUsers(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	c := csvConfig(s.URL)
	c.RampUp = "100ms"
	c.Scenarios[0].CSV.Content = "name\nA"
	c.Scenarios[0].CSV.OnEOF = "stop_user"
	r := runTest(t, c)
	if !r.Passed || r.Completed != 1 || attempts.Load() != 1 || r.Datasets[0].UsersExhausted != 4 || r.UserSchedule.Users.Started != 4 || r.TotalSeconds < .075 {
		t.Fatalf("ramp: %+v", r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestCSVProgressAndCancellationRetainCounts(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") }))
	defer s.Close()
	c := csvConfig(s.URL)
	c.Iterations = 0
	c.Duration = "10s"
	c.ThinkTime = "5ms"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := c.Validate()
	samples := 0
	r, err := Run(ctx, c, Options{OnProgress: func(progress Progress) {
		samples++
		if e := ValidateDatasets(p, progress.Datasets, progress.Journeys.Started); e != nil {
			t.Error(e)
		}
		cancel()
	}})
	if err != nil || samples != 1 || !r.Interrupted || r.StopReason != "interrupted" || r.Datasets[0].RowsAssigned != r.Journeys.Started || r.Datasets[0].UsersExhausted != 0 {
		t.Fatalf("cancellation: %v %+v", err, r)
	}
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
	c.Duration = "20ms"
	c.ThinkTime = "1s"
	r = runTest(t, c)
	if !r.Passed || r.StopReason != "duration" || r.Datasets[0].RowsAssigned != 4 || r.LoadSeconds != .02 {
		t.Fatalf("duration: %+v", r)
	}
}

func csvPtr[T any](v T) *T { return &v }
