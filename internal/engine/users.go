package engine

import (
	"fmt"
	"time"

	"gometer/internal/config"
)

// User counts describe virtual-user lifecycles, not HTTP concurrency. A user
// can be active while waiting between iterations or executing a journey pause.
type UserCounts struct {
	Planned  int64 `json:"planned"`
	Started  int64 `json:"started"`
	Finished int64 `json:"finished"`
	Active   int64 `json:"active"`
}

type UserSchedule struct {
	RampUpSeconds         float64    `json:"ramp_up_seconds"`
	PlannedStartupSeconds float64    `json:"planned_startup_seconds"`
	StartupSeconds        float64    `json:"startup_seconds"`
	Waiting               bool       `json:"waiting,omitempty"`
	Users                 UserCounts `json:"users"`
}

func (r *runner) userSchedule(wall time.Duration) *UserSchedule {
	if !r.plan.HasUserSchedule() {
		return nil
	}
	finished := r.usersFinished.Load()
	started := r.usersStarted.Load()
	return &UserSchedule{
		RampUpSeconds: r.plan.UserRampUp.Seconds(), PlannedStartupSeconds: r.plan.UserStartupDelay.Seconds(),
		StartupSeconds: min(wall, r.plan.UserStartupDelay).Seconds(), Waiting: wall < r.plan.UserStartupDelay,
		Users: UserCounts{Planned: int64(r.plan.VUs), Started: started, Finished: finished, Active: started - finished},
	}
}

// ValidateUserSchedule also validates timeline snapshots. Older schemas omit
// these fields; importing them must not invent observations of active users.
func (s UserSchedule) Validate(p config.Plan, wall, load float64, final bool) error {
	u := s.Users
	if s.RampUpSeconds != p.UserRampUp.Seconds() || s.PlannedStartupSeconds != p.UserStartupDelay.Seconds() || s.StartupSeconds < 0 || s.StartupSeconds > s.PlannedStartupSeconds || s.StartupSeconds > wall || s.StartupSeconds+load > wall+1e-6 || u.Planned != int64(p.VUs) || u.Started < 0 || u.Started > u.Planned || u.Finished < 0 || u.Finished > u.Started || u.Active != u.Started-u.Finished {
		return fmt.Errorf("invalid user schedule timing or counts")
	}
	if s.StartupSeconds < s.PlannedStartupSeconds && (load != 0 || u.Started != 0) {
		return fmt.Errorf("work started before startup delay finished")
	}
	if (final && (s.Waiting || u.Active != 0)) || (!final && s.Waiting != (s.StartupSeconds < s.PlannedStartupSeconds)) {
		return fmt.Errorf("user lifecycle disagrees with schedule state")
	}
	return nil
}

func (r Result) validateUserSchedule(p config.Plan) error {
	if !p.HasUserSchedule() {
		if r.UserSchedule != nil || r.SchemaVersion == 8 {
			return fmt.Errorf("unexpected user schedule")
		}
		return nil
	}
	if (r.SchemaVersion < 8 || r.SchemaVersion > 11) || r.UserSchedule == nil || r.ConcurrencyLimit != p.VUs {
		return fmt.Errorf("scheduled users require schema 8–11 and matching user settings")
	}
	if err := r.UserSchedule.Validate(p, r.TotalSeconds, r.LoadSeconds, true); err != nil {
		return err
	}
	if !r.Interrupted && r.UserSchedule.StartupSeconds != p.UserStartupDelay.Seconds() {
		return fmt.Errorf("startup delay was not completed")
	}
	if r.StopReason == "iterations" && r.UserSchedule.Users.Started != int64(p.VUs) {
		return fmt.Errorf("not every user reached its iteration limit")
	}
	return nil
}
