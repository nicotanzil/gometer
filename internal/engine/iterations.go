package engine

import (
	"fmt"

	"gometer/internal/config"
)

// ValidateIterations checks the stopping contract without trusting imported totals.
func (r Result) ValidateIterations(p config.Plan) error {
	if err := r.validateDatasets(p); err != nil {
		return err
	}
	if err := r.validateFailurePolicy(p); err != nil {
		return err
	}
	if err := r.validateUserSchedule(p); err != nil {
		return err
	}
	if r.Iterations != p.Iterations || (p.Iterations > 0 && (r.SchemaVersion < 7 || r.SchemaVersion > 11)) || (r.SchemaVersion == 7 && p.Iterations == 0) {
		return fmt.Errorf("iteration settings or schema disagree")
	}
	if p.Iterations == 0 && !p.HasUserSchedule() && !p.HasFailurePolicy() && !p.HasCSV() {
		if r.StopReason != "" {
			return fmt.Errorf("unexpected iteration stop reason")
		}
		return nil
	}
	if r.ConcurrencyLimit != p.VUs {
		return fmt.Errorf("user count disagrees with iteration settings")
	}
	completed := r.Completed
	if p.IsJourney() {
		completed = r.Journeys.Completed
	}
	target := int64(p.VUs) * int64(p.Iterations)
	if completed < 0 || (p.Iterations > 0 && completed > target) {
		return fmt.Errorf("completed iterations exceed the per-user limit")
	}
	switch r.StopReason {
	case "iterations":
		if r.Interrupted || p.Iterations == 0 || completed != target {
			return fmt.Errorf("iteration completion requires every user to finish")
		}
	case "duration":
		if r.Interrupted || p.LoadDuration == 0 || r.LoadSeconds != p.LoadDuration.Seconds() {
			return fmt.Errorf("duration stop requires the configured time limit")
		}
	case "interrupted":
		if !r.Interrupted {
			return fmt.Errorf("interrupted stop requires an interrupted result")
		}
	case "data_exhausted":
		if !p.HasCSV() || (p.Iterations > 0 && completed >= target) {
			return fmt.Errorf("CSV exhaustion must precede iteration completion")
		}
	case "users_stopped":
		if r.Interrupted || r.FailurePolicy == nil || r.FailurePolicy.Action != config.StopUser || r.FailurePolicy.UsersStopped == 0 {
			return fmt.Errorf("users_stopped requires a user stopped by the failure action")
		}
	case "test_stopped", "test_stopped_now":
		if r.Interrupted || r.FailurePolicy == nil || !r.FailurePolicy.TestStopped {
			return fmt.Errorf("test stop requires an error-triggered stop")
		}
	default:
		return fmt.Errorf("invalid iteration stop reason")
	}
	return nil
}
