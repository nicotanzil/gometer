package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func TestFailureActionsAcrossNestedLoopAndOuterIterations(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "failed", 503)
		}
	}))
	defer s.Close()
	for _, tc := range []struct {
		action                                     string
		attempts, journeys, failed, stopped, loops int64
		reason                                     string
	}{
		{"", 2, 2, 2, 0, 0, "iterations"},
		{config.ContinueJourney, 10, 2, 4, 0, 2, "iterations"},
		{config.StopUser, 1, 1, 1, 1, 0, "users_stopped"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			c := finiteConfig(s.URL)
			c.VUs, c.Iterations, c.OnError = 1, 2, tc.action
			c.Scenarios = []config.Scenario{{Name: "Flow", Steps: []config.Step{
				{Name: "Twice", Loop: &config.Loop{Count: pointer(2), Steps: []config.Step{{Name: "Fail", URL: "/fail", Pause: "1s"}, {Name: "After", URL: "/after"}}}},
				{Name: "Finish", URL: "/finish"},
			}}}
			// Disabled thresholds cannot hide a user stopped by the policy.
			if tc.action == config.StopUser {
				c.Thresholds.MaxErrorPercent = nil
				c.JourneyThresholds.MaxErrorPercent = nil
			}
			r := runTest(t, c)
			assertJourneyAccounting(t, r)
			if r.Passed || r.Completed != tc.attempts || r.Failed != tc.failed || r.Journeys.Completed != tc.journeys || r.Journeys.Failed != tc.journeys || r.StopReason != tc.reason || r.Scenarios[0].Controls[0].Completed != tc.loops {
				t.Fatalf("failure action: %+v", r)
			}
			if tc.action != "" && (r.SchemaVersion != 9 || r.FailurePolicy.UsersStopped != tc.stopped) {
				t.Fatalf("policy metadata: %+v", r)
			}
			if tc.action == config.StopUser {
				found := false
				for _, check := range r.Checks {
					if check.Name == "users_stopped_on_error" && !check.Passed && check.Actual == 1 {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing mandatory check: %+v", r.Checks)
				}
			}
			p, _ := c.Validate()
			if err := r.ValidateIterations(p); err != nil {
				t.Fatal(err)
			}
			if err := r.ValidateFlow(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStopUserPreservesPeersAndPendingRampStarts(t *testing.T) {
	var first, second atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user") == "1" {
			first.Add(1)
			http.Error(w, "failed", 503)
		} else {
			second.Add(1)
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.OnError, c.RampUp, c.StartupDelay = 2, 3, config.StopUser, "80ms", "10ms"
	c.Scenarios = []config.Scenario{{Name: "Peers", Steps: []config.Step{{URL: "/?user={{user_id}}"}}}}
	r := runTest(t, c)
	if r.Passed || first.Load() != 1 || second.Load() != 3 || r.Journeys.Completed != 4 || r.FailurePolicy.UsersStopped != 1 || r.StopReason != "users_stopped" || r.UserSchedule.Users != (UserCounts{Planned: 2, Started: 2, Finished: 2}) {
		t.Fatalf("peer isolation: %+v", r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestStopUserEndsDurationOnlyRunEarlyAndSuccessfulUsersComplete(t *testing.T) {
	for _, fail := range []bool{true, false} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail {
				http.Error(w, "failed", 503)
			}
		}))
		c := finiteConfig(s.URL)
		c.VUs, c.OnError = 2, config.StopUser
		if fail {
			c.Iterations, c.Duration = 0, "1h"
		} else {
			c.Iterations = 3
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		r, err := Run(ctx, c, Options{})
		cancel()
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if fail {
			if r.Interrupted || r.Passed || r.Completed != 2 || r.FailurePolicy.UsersStopped != 2 || r.StopReason != "users_stopped" {
				t.Fatalf("duration-only: %+v", r)
			}
		} else if !r.Passed || r.Completed != 6 || r.FailurePolicy.UsersStopped != 0 || r.StopReason != "iterations" {
			t.Fatalf("successful users: %+v", r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContinueDoesNotPublishFailedVerificationValues(t *testing.T) {
	var sent atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		fmt.Fprint(w, `{"token":"secret","ok":false}`)
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.OnError = 1, 1, config.ContinueJourney
	c.Scenarios = []config.Scenario{{Name: "Verify", Steps: []config.Step{
		{URL: "/start", Extract: map[string]config.Extraction{"token": {JSONPointer: pointer("/token")}}, Assertions: []config.Assertion{{JSONPointer: pointer("/ok"), Equals: pointer("true")}}},
		{URL: "/dependent?token={{token}}"}, {URL: "/independent"},
	}}}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if r.Completed != 3 || sent.Load() != 2 || r.Errors["assertion"] != 1 || r.Errors["template"] != 1 || r.Journeys.Failed != 1 || r.Scenarios[0].Steps[2].Succeeded != 1 {
		t.Fatalf("transactional verification: %+v", r)
	}
}

func TestContinueHonorsRequestTimeoutJourneyDeadlineAndSafetyLimits(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.OnError = 1, 1, config.ContinueJourney
	c.Timeout = "20ms"
	c.Scenarios = []config.Scenario{{Name: "Timeout", Steps: []config.Step{{URL: "/slow"}, {URL: "/after"}}}}
	r := runTest(t, c)
	if r.Completed != 2 || r.Failed != 1 || r.Scenarios[0].Steps[1].Succeeded != 1 || r.Journeys.Failed != 1 {
		t.Fatalf("request timeout should advance: %+v", r)
	}
	c.Timeout = "1s"
	c.JourneyTimeout = "20ms"
	r = runTest(t, c)
	if r.Completed != 1 || r.Journeys.Errors["timeout"] != 1 || r.Scenarios[0].Steps[1].Skipped != 1 {
		t.Fatalf("journey timeout must abort: %+v", r)
	}
	c.JourneyTimeout = "30s"
	c.Scenarios = []config.Scenario{{Name: "Limit", Variables: map[string]string{"repeat": "true"}, Steps: []config.Step{
		{Loop: &config.Loop{While: &config.Condition{Variable: "repeat", Value: pointer("true")}, MaxIterations: 2, Steps: []config.Step{{URL: "/"}}}}, {URL: "/after"},
	}}}
	r = runTest(t, c)
	if r.Completed != 2 || r.Journeys.Errors["loop_limit"] != 1 || r.Scenarios[0].Steps[1].Skipped != 1 {
		t.Fatalf("safety limit must abort: %+v", r)
	}
	c.Scenarios = []config.Scenario{{Name: "Condition", Variables: map[string]string{"number": "invalid"}, Steps: []config.Step{
		{If: &config.Branch{Condition: config.Condition{Variable: "number", Operator: "gt", Value: pointer("1")}, Then: []config.Step{{URL: "/"}}}}, {URL: "/after"},
	}}}
	r = runTest(t, c)
	if r.Completed != 0 || r.Journeys.Errors["condition"] != 1 || r.Scenarios[0].Steps[1].Skipped != 1 {
		t.Fatalf("condition error must abort: %+v", r)
	}
}

func TestManualCancellationDoesNotTriggerFailurePolicy(t *testing.T) {
	for _, action := range []string{config.StopUser, config.StopTest, config.StopTestNow} {
		ctx, cancel := context.WithCancel(context.Background())
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
		c := finiteConfig(s.URL)
		c.VUs, c.OnError = 1, action
		r, err := Run(ctx, c, Options{})
		cancel()
		s.Close()
		if err != nil || !r.Interrupted || r.StopReason != "interrupted" || r.FailurePolicy.UsersStopped != 0 || r.FailurePolicy.TestStopped {
			t.Fatalf("manual cancellation: %v %+v", err, r)
		}
		p, _ := c.Validate()
		if err := r.ValidateIterations(p); err != nil {
			t.Fatal(err)
		}
	}
}
