package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func finiteConfig(target string) config.Config {
	c := testConfig(target)
	c.Mode, c.Rate, c.VUs, c.Iterations, c.Duration = "users", 0, 10, 5, ""
	return c
}

func TestFiniteRequestIterationsAndActualThroughput(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	for _, duration := range []string{"", "1h"} {
		c := finiteConfig(s.URL)
		c.Duration = duration
		before := attempts.Load()
		r := runTest(t, c)
		if !r.Passed || r.Completed != 50 || attempts.Load()-before != 50 || r.Iterations != 5 || r.StopReason != "iterations" || r.SchemaVersion != 7 || r.LoadSeconds <= 0 || r.LoadSeconds >= 10 || r.Phases[0].LoadSeconds != r.LoadSeconds || r.StartedRPS != float64(r.Started)/r.LoadSeconds {
			t.Fatalf("finite request result: %+v", r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFiniteJourneysRetainIsolatedSessionsAndCountFailures(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]int{}
	var bad atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/after" {
			return
		}
		id := r.URL.Query().Get("id")
		iteration, _ := strconv.Atoi(r.URL.Query().Get("iteration"))
		cookie, err := r.Cookie("session")
		if (iteration == 1 && err == nil) || (iteration > 1 && (err != nil || cookie.Value != id)) {
			bad.Add(1)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: id, Path: "/"})
		mu.Lock()
		seen[id] = append(seen[id], iteration)
		mu.Unlock()
		if iteration == 2 {
			http.Error(w, "intentional failure", 503)
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.Scenarios = []config.Scenario{{Name: "Session", Steps: []config.Step{{Name: "Attempt", URL: "/?id={{user_id}}&iteration={{iteration}}"}, {Name: "After", URL: "/after"}}}}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if r.Passed || r.Journeys.Completed != 50 || r.Journeys.Failed != 10 || r.Completed != 90 || r.Scenarios[0].Steps[1].Skipped != 10 || r.StopReason != "iterations" || bad.Load() != 0 || len(seen) != 10 {
		t.Fatalf("session/failure behavior: bad=%d users=%d result=%+v", bad.Load(), len(seen), r)
	}
	for id, iterations := range seen {
		if len(iterations) != 5 {
			t.Fatalf("user %s: %v", id, iterations)
		}
		for i, value := range iterations {
			if value != i+1 {
				t.Fatalf("user %s sequence: %v", id, iterations)
			}
		}
	}
}

func TestFiniteDurationStopsAdmissionAndDrainsJourney(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(200 * time.Millisecond)
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Duration = 1, "100ms"
	c.Scenarios = []config.Scenario{{Name: "Drain", Steps: []config.Step{{Name: "Slow", URL: "/slow"}, {Name: "Finish", URL: "/finish"}}}}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if !r.Passed || r.StopReason != "duration" || r.Journeys.Completed != 1 || r.Completed != 2 || r.LoadSeconds != .1 || r.TotalSeconds < .2 {
		t.Fatalf("duration/drain behavior: %+v", r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestFiniteIterationsSkipFinalThinkTimeAndCancelBetweenIterations(t *testing.T) {
	first := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case first <- struct{}{}:
		default:
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.ThinkTime = 1, 1, "1h"
	deadlineCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	r, err := Run(deadlineCtx, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Completed != 1 || r.Interrupted || r.StopReason != "iterations" {
		t.Fatalf("waited after final iteration: %+v", r)
	}
	<-first
	c.Iterations = 5
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
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("first iteration did not start")
	}
	cancel()
	select {
	case r := <-done:
		if !r.Interrupted || r.Passed || r.Completed != 1 || r.StopReason != "interrupted" {
			t.Fatalf("cancellation: %+v", r)
		}
		assertAccounting(t, r.Snapshot)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock think time")
	}
}
