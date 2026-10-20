package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func TestTestWideStopsDrainOrCancelAdmittedJourneys(t *testing.T) {
	for _, action := range []string{config.StopTest, config.StopTestNow} {
		t.Run(action, func(t *testing.T) {
			peerStarted, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releasePeer := func() { releaseOnce.Do(func() { close(release) }) }
			defer releasePeer()
			var after atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/first" {
					after.Add(1)
					return
				}
				if r.URL.Query().Get("user") == "1" {
					select {
					case <-peerStarted:
						http.Error(w, "failed", 503)
					case <-r.Context().Done():
					}
					return
				}
				close(peerStarted)
				select {
				case <-release:
					fmt.Fprint(w, "ok")
				case <-r.Context().Done():
				}
			}))
			defer s.Close()
			c := finiteConfig(s.URL)
			c.VUs, c.Iterations, c.OnError = 2, 5, action
			c.Timeout = "10s"
			c.Thresholds.MaxErrorPercent = nil
			c.JourneyThresholds.MaxErrorPercent = nil
			c.Scenarios = []config.Scenario{{Name: "Drain", Steps: []config.Step{
				{URL: "/first?user={{user_id}}"},
				{Loop: &config.Loop{Count: pointer(2), Steps: []config.Step{{URL: "/loop"}}}}, {URL: "/finish"},
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			progress := make(chan Progress, 8)
			done := make(chan Result, 1)
			go func() {
				r, err := Run(ctx, c, Options{OnProgress: func(p Progress) {
					if p.FailurePolicy.TestStopped {
						progress <- p
					}
				}})
				if err != nil {
					t.Error(err)
				}
				done <- r
			}()
			var observed Progress
			if action == config.StopTest {
				// Two drain observations must have the same admission denominator.
				for i := 0; i < 2; i++ {
					select {
					case p := <-progress:
						if !p.Draining || p.Failed != 1 || p.Completed != 1 || (i > 0 && p.Elapsed != observed.Elapsed) {
							t.Fatalf("draining progress: %+v", p)
						}
						observed = p
					case <-ctx.Done():
						t.Fatal("no graceful drain progress")
					}
				}
				releasePeer()
			}
			select {
			case r := <-done:
				assertJourneyAccounting(t, r)
				wantRequests, wantAfter, wantFailed, reason := int64(5), int64(3), int64(1), "test_stopped"
				if action == config.StopTestNow {
					wantRequests, wantAfter, wantFailed, reason = 2, 0, 2, "test_stopped_now"
				}
				if r.Passed || r.Interrupted || r.SchemaVersion != 10 || r.StopReason != reason || !r.FailurePolicy.TestStopped || r.FailurePolicy.UsersStopped != 0 || r.Completed != wantRequests || r.Journeys.Completed != 2 || r.Journeys.Failed != wantFailed || after.Load() != wantAfter {
					t.Fatalf("test stop: %+v after=%d", r, after.Load())
				}
				if action == config.StopTest && (r.LoadSeconds != observed.Elapsed.Seconds() || r.TotalSeconds-r.LoadSeconds < 1) {
					t.Fatalf("drain changed admission denominator: %+v", r)
				}
				if action == config.StopTestNow && r.Errors["canceled"] != 1 {
					t.Fatalf("active request not recorded as canceled: %+v", r)
				}
				found := false
				for _, check := range r.Checks {
					if check.Name == "test_stopped_on_error" && !check.Passed && check.Actual == 1 {
						found = true
					}
				}
				if !found {
					t.Fatalf("disabled thresholds hid stop: %+v", r.Checks)
				}
				p, _ := c.Validate()
				if err := r.ValidateIterations(p); err != nil {
					t.Fatal(err)
				}
				if err := r.ValidateFlow(p); err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("test stop did not finish")
			}
		})
	}
}

func TestTestWideStopsCancelPendingRampStarts(t *testing.T) {
	for _, action := range []string{config.StopTest, config.StopTestNow} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 503) }))
		c := finiteConfig(s.URL)
		c.VUs, c.OnError, c.RampUp = 4, action, "1h"
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		r, err := Run(ctx, c, Options{})
		cancel()
		s.Close()
		if err != nil || r.Interrupted || r.Completed != 1 || !r.FailurePolicy.TestStopped || r.UserSchedule.Users != (UserCounts{Planned: 4, Started: 1, Finished: 1}) {
			t.Fatalf("pending starts: %v %+v", err, r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestErrorDuringDurationDrainRetainsDurationAndStopReason(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		http.Error(w, "late failure", 503)
	}))
	defer s.Close()
	for _, action := range []string{config.StopTest, config.StopTestNow} {
		c := finiteConfig(s.URL)
		c.VUs, c.Duration, c.OnError = 1, "100ms", action
		r := runTest(t, c)
		if r.Interrupted || r.Passed || !r.FailurePolicy.TestStopped || r.Completed != 1 || r.LoadSeconds != .1 || r.TotalSeconds < .2 {
			t.Fatalf("late error during duration drain: %+v", r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImmediateTestStopCancelsJourneyPause(t *testing.T) {
	releaseFailure := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user") == "1" {
			select {
			case <-releaseFailure:
				http.Error(w, "failed", 503)
			case <-r.Context().Done():
			}
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.OnError, c.JourneyTimeout, c.Timeout = 2, config.StopTestNow, "2h", "10s"
	c.Scenarios = []config.Scenario{{Name: "Pause", Steps: []config.Step{{URL: "/?user={{user_id}}", Pause: "1h"}, {URL: "/after"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var once sync.Once
	r, err := Run(ctx, c, Options{OnProgress: func(p Progress) {
		if p.Completed == 1 && p.Failed == 0 {
			once.Do(func() { close(releaseFailure) })
		}
	}})
	if err != nil || r.Interrupted || !r.FailurePolicy.TestStopped || r.Completed != 2 || r.Journeys.Errors["canceled"] != 1 || r.Scenarios[0].Steps[1].Skipped != 2 {
		t.Fatalf("pause cancellation: %v %+v", err, r)
	}
}

func TestManualStopCanInterruptGracefulErrorDrain(t *testing.T) {
	peer := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user") == "1" {
			select {
			case <-peer:
				http.Error(w, "failed", 503)
			case <-r.Context().Done():
			}
			return
		}
		close(peer)
		<-r.Context().Done()
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.OnError, c.Timeout = 2, config.StopTest, "10s"
	c.Scenarios = []config.Scenario{{Name: "Manual", Steps: []config.Step{{URL: "/?user={{user_id}}"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	r, err := Run(ctx, c, Options{OnProgress: func(p Progress) {
		if p.FailurePolicy.TestStopped {
			cancel()
		}
	}})
	if err != nil || !r.Interrupted || r.StopReason != "interrupted" || !r.FailurePolicy.TestStopped || r.Passed {
		t.Fatalf("manual stop after failure: %v %+v", err, r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestTestStopPoliciesPreserveHealthyRunsAndJourneyErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	for _, action := range []string{config.StopTest, config.StopTestNow} {
		c := finiteConfig(s.URL)
		c.VUs, c.Iterations, c.OnError = 2, 3, action
		r := runTest(t, c)
		if !r.Passed || r.Completed != 6 || r.StopReason != "iterations" || r.FailurePolicy.TestStopped || r.SchemaVersion != 10 {
			t.Fatalf("healthy run: %+v", r)
		}
		c.VUs = 1
		c.Scenarios = []config.Scenario{{Name: "Condition", Variables: map[string]string{"number": "invalid"}, Steps: []config.Step{{If: &config.Branch{Condition: config.Condition{Variable: "number", Operator: "gt", Value: pointer("1")}, Then: []config.Step{{URL: "/"}}}}}}}
		r = runTest(t, c)
		if r.Interrupted || !r.FailurePolicy.TestStopped || r.Journeys.Errors["condition"] != 1 || r.Completed != 0 || r.Journeys.Completed != 1 {
			t.Fatalf("journey-level error: %+v", r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}
