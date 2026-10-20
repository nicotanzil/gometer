package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gometer/internal/engine"
)

func TestFailurePolicyBundlesPreserveExecutionAndRejectMismatches(t *testing.T) {
	s := testServer(t)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.URL.Path == "/fail" {
			http.Error(w, "failed", 503)
		}
	}))
	defer target.Close()
	for _, action := range []string{"continue", "stop_user"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(`{"url":%q,"vus":2,"iterations":2,"ramp_up":"20ms","on_error":%q,"scenarios":[{"name":"Flow","steps":[{"name":"Loop","loop":{"count":2,"steps":[{"name":"Fail","url":"/fail"},{"name":"After","url":"/after"}]}}]}]}`, target.URL, action)
			before := attempts.Load()
			run := awaitRun(t, s, startRun(t, s, body))
			want := int64(16)
			stopped := int64(0)
			if action == "stop_user" {
				want, stopped = 2, 2
			}
			if run.Status != "failed" || run.Result.SchemaVersion != 9 || run.Result.Completed != want || attempts.Load()-before != want || run.Result.FailurePolicy.UsersStopped != stopped || run.Samples[0].FailurePolicy == nil {
				t.Fatalf("policy run: %+v", run)
			}
			bundle, _ := json.Marshal(run)
			if w := request(s, "POST", "/api/runs/import", string(bundle)); w.Code != 201 || attempts.Load()-before != want {
				t.Fatalf("round trip: %d %s", w.Code, w.Body)
			}
			for _, mutate := range []func(*Run){
				func(r *Run) { r.Config.OnError = "next_iteration" }, func(r *Run) { r.Result.SchemaVersion = 8 }, func(r *Run) { r.Result.FailurePolicy = nil },
				func(r *Run) { r.Result.FailurePolicy.Action = "unknown" }, func(r *Run) { r.Result.FailurePolicy.UsersStopped = 3 }, func(r *Run) { r.Result.FailurePolicy.UsersStopped = -1 },
				func(r *Run) { r.Samples[0].FailurePolicy = nil }, func(r *Run) { r.Samples[0].FailurePolicy.Action = "unknown" }, func(r *Run) { r.Samples[0].FailurePolicy.UsersStopped = 1 },
			} {
				var invalid Run
				json.Unmarshal(bundle, &invalid)
				mutate(&invalid)
				data, _ := json.Marshal(invalid)
				if w := request(s, "POST", "/api/runs/import", string(data)); w.Code != 400 {
					t.Fatalf("invalid policy accepted: %d %s", w.Code, w.Body)
				}
			}
			if action == "stop_user" {
				var invalid Run
				json.Unmarshal(bundle, &invalid)
				invalid.Result.Passed = true
				data, _ := json.Marshal(invalid)
				if w := request(s, "POST", "/api/runs/import", string(data)); w.Code != 400 {
					t.Fatal("stopped users cannot pass")
				}
			}
			stored, err := s.store.getRun(run.ID)
			if err != nil || stored.Config.OnError != action || stored.Result.FailurePolicy.UsersStopped != stopped {
				t.Fatalf("persistence: %v %+v", err, stored)
			}
		})
	}
}

func TestTestStopBundlesPreservePartialResultsAndRejectMismatches(t *testing.T) {
	s := testServer(t)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); http.Error(w, "failed", 503) }))
	defer target.Close()
	for _, action := range []string{"stop_test", "stop_test_now"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(`{"url":%q,"vus":4,"iterations":5,"ramp_up":"1h","on_error":%q,"thresholds":{"max_error_percent":null},"journey_thresholds":{"max_error_percent":null},"scenarios":[{"name":"Fail","steps":[{"url":"/fail"},{"url":"/after"}]}]}`, target.URL, action)
			before := attempts.Load()
			run := awaitRun(t, s, startRun(t, s, body))
			if run.Status != "failed" || run.Result.SchemaVersion != 10 || run.Result.Interrupted || !run.Result.FailurePolicy.TestStopped || run.Result.Completed != 1 || run.Result.Journeys.Completed != 1 || run.Result.UserSchedule.Users.Started != 1 {
				t.Fatalf("test stop: %+v", run)
			}
			bundle, _ := json.Marshal(run)
			if w := request(s, "POST", "/api/runs/import", string(bundle)); w.Code != 201 || attempts.Load()-before != 1 {
				t.Fatalf("round trip: %d %s", w.Code, w.Body)
			}
			for _, mutate := range []func(*Run){
				func(r *Run) { r.Result.SchemaVersion = 9 }, func(r *Run) { r.Config.OnError = "stop_user" }, func(r *Run) { r.Result.FailurePolicy.TestStopped = false }, func(r *Run) { r.Result.FailurePolicy.UsersStopped = 1 }, func(r *Run) { r.Result.Passed = true }, func(r *Run) { r.Result.StopReason = "iterations" }, func(r *Run) { r.Result.StopReason = "interrupted" },
				func(r *Run) {
					if action == "stop_test" {
						r.Result.StopReason = "test_stopped_now"
					} else {
						r.Result.StopReason = "test_stopped"
					}
				},
				func(r *Run) { r.Result.Checks = nil }, func(r *Run) {
					for i := range r.Result.Checks {
						if r.Result.Checks[i].Name == "test_stopped_on_error" {
							r.Result.Checks[i].Passed = true
						}
					}
				},
				func(r *Run) { r.Samples[0].FailurePolicy.TestStopped = true },
				func(r *Run) { r.Samples[len(r.Samples)-1].FailurePolicy.TestStopped = false },
				func(r *Run) {
					last := r.Samples[len(r.Samples)-1]
					f := *last.FailurePolicy
					f.TestStopped = false
					last.FailurePolicy = &f
					r.Samples = append(r.Samples, last)
				},
				func(r *Run) {
					r.Result.FailurePolicy = &engine.FailurePolicy{Action: action, TestStopped: true}
					r.Result.Journeys.Failed = 0
				},
			} {
				var invalid Run
				json.Unmarshal(bundle, &invalid)
				mutate(&invalid)
				data, _ := json.Marshal(invalid)
				if w := request(s, "POST", "/api/runs/import", string(data)); w.Code != 400 {
					t.Fatalf("invalid test stop accepted: %d %s", w.Code, w.Body)
				}
			}
			stored, err := s.store.getRun(run.ID)
			if err != nil || !stored.Result.FailurePolicy.TestStopped || stored.Config.OnError != action {
				t.Fatalf("saved stop: %v %+v", err, stored)
			}
		})
	}
}
