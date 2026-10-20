package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gometer/internal/config"
)

type FailurePolicy struct {
	Action       string `json:"action"`
	UsersStopped int64  `json:"users_stopped"`
	TestStopped  bool   `json:"test_stopped,omitempty"`
}

// A test stop closes admission independently of workload cancellation. Keeping
// its clock and state together lets progress retain the actual admission window
// while graceful journeys drain. The caller's context remains manual Stop.
type testStop struct {
	mu                         sync.Mutex
	at                         time.Duration
	start                      time.Time
	action                     string
	closeAdmission, cancelWork context.CancelFunc
}

func (s *testStop) trigger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at != 0 {
		return
	}
	s.at = max(time.Nanosecond, time.Since(s.start))
	s.closeAdmission()
	if s.action == config.StopTestNow {
		s.cancelWork()
	}
}

func (s *testStop) elapsed() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at
}

func (r *runner) loadElapsed(wall time.Duration) time.Duration {
	if at := r.testStop.elapsed(); at > 0 {
		wall = min(wall, at)
	}
	elapsed := max(time.Duration(0), wall-r.plan.UserStartupDelay)
	if r.plan.LoadDuration > 0 {
		elapsed = min(elapsed, r.plan.LoadDuration)
	}
	return elapsed
}

func (r *runner) failurePolicy() *FailurePolicy {
	if !r.plan.HasFailurePolicy() {
		return nil
	}
	return &FailurePolicy{Action: r.plan.ErrorAction(), UsersStopped: r.usersStopped.Load(), TestStopped: r.testStop.elapsed() > 0}
}

func (f FailurePolicy) Validate(c config.Config, failed int64) error {
	if f.Action != c.ErrorAction() || f.UsersStopped < 0 || f.UsersStopped > int64(c.VUs) || f.UsersStopped > failed || (f.Action != config.StopUser && f.UsersStopped != 0) {
		return fmt.Errorf("failure action or stopped-user counts disagree")
	}
	if f.TestStopped && (!c.HasTestStopPolicy() || failed < 1) {
		return fmt.Errorf("test stop requires a matching failure action and failed work")
	}
	return nil
}

func (r Result) validateFailurePolicy(p config.Plan) error {
	if !p.HasFailurePolicy() {
		if r.FailurePolicy != nil || (r.SchemaVersion >= 9 && !p.HasCSV()) {
			return fmt.Errorf("unexpected failure policy")
		}
		return nil
	}
	wantSchema := 9
	if p.HasTestStopPolicy() {
		wantSchema = 10
	}
	if p.HasCSV() {
		wantSchema = 11
	}
	if r.SchemaVersion != wantSchema || r.FailurePolicy == nil {
		return fmt.Errorf("failure action requires schema %d", wantSchema)
	}
	failed := r.Failed
	if p.IsJourney() {
		failed = r.Journeys.Failed
	}
	if err := r.FailurePolicy.Validate(p.Config, failed); err != nil {
		return err
	}
	if p.HasTestStopPolicy() && !r.Interrupted && failed > 0 && !r.FailurePolicy.TestStopped {
		return fmt.Errorf("failed work requires the configured test stop")
	}
	if p.HasTestStopPolicy() {
		found := 0
		for _, check := range r.Checks {
			if check.Name != "test_stopped_on_error" {
				continue
			}
			found++
			if check.Passed == r.FailurePolicy.TestStopped || check.Actual != boolNumber(r.FailurePolicy.TestStopped) || check.Limit != 0 || check.Unit != "boolean" {
				return fmt.Errorf("test stop check disagrees with failure policy")
			}
		}
		if found != 1 {
			return fmt.Errorf("test stop requires one mandatory stopping check")
		}
	}
	if r.FailurePolicy.UsersStopped > 0 && r.StopReason == "iterations" {
		return fmt.Errorf("users stopped on error before normal iteration completion")
	}
	if r.FailurePolicy.UsersStopped > 0 && r.Passed {
		return fmt.Errorf("users stopped on error cannot pass the run")
	}
	if r.FailurePolicy.TestStopped {
		if r.Passed {
			return fmt.Errorf("test stopped on error cannot pass")
		}
		wantReason := "test_stopped"
		if p.ErrorAction() == config.StopTestNow {
			wantReason = "test_stopped_now"
		}
		if !r.Interrupted && r.StopReason != wantReason {
			return fmt.Errorf("test stop reason disagrees with the failure action")
		}
	}
	return nil
}
