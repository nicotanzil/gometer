package engine

import (
	"fmt"
	"sync/atomic"

	"gometer/internal/config"
)

type DatasetResult struct {
	Scenario       string `json:"scenario"`
	Name           string `json:"name"`
	Rows           int    `json:"rows"`
	Sharing        string `json:"sharing"`
	OnEOF          string `json:"on_eof"`
	RowsAssigned   int64  `json:"rows_assigned"`
	UsersExhausted int64  `json:"users_exhausted"`
}

type datasetStats struct{ assigned, exhausted atomic.Int64 }

func DatasetSettings(p config.Plan) []DatasetResult {
	var result []DatasetResult
	for _, s := range p.ScenarioPlans {
		if c := s.CSV; c != nil {
			result = append(result, DatasetResult{Scenario: s.Name, Name: c.Label(), Rows: len(c.Rows), Sharing: c.SharingMode(), OnEOF: c.EOFAction()})
		}
	}
	return result
}

func (r *runner) datasets() []DatasetResult {
	result := DatasetSettings(r.plan)
	i := 0
	for selected, s := range r.plan.ScenarioPlans {
		if s.CSV == nil {
			continue
		}
		// Read monotonic counters before the journey snapshot. Shared EOF
		// probes can race with earlier admitted users starting their work.
		result[i].UsersExhausted = r.datasetStats[selected].exhausted.Load()
		result[i].RowsAssigned = r.datasetStats[selected].assigned.Load()
		i++
	}
	return result
}

// Each user owns its per-scenario cursors. Shared cursors use the weighted
// admission ordinal, independent of request completion order.
func (r *runner) datasetRow(id int64, selected int, cursors []int64) (map[string]string, bool) {
	c := r.plan.ScenarioPlans[selected].CSV
	if c == nil {
		return nil, true
	}
	ordinal := r.plan.ScenarioOrdinal(id, selected)
	if c.SharingMode() == "per_user" {
		ordinal = cursors[selected]
	}
	if c.EOFAction() == "stop_user" && ordinal >= int64(len(c.Rows)) {
		r.datasetStats[selected].exhausted.Add(1)
		return nil, false
	}
	cursors[selected]++
	return c.Rows[ordinal%int64(len(c.Rows))], true
}

// ValidateDatasets validates both timeline and final observations, without
// trusting a bundle's row counts or source settings.
func ValidateDatasets(p config.Plan, datasets []DatasetResult, started int64) error {
	if started < 0 {
		return fmt.Errorf("invalid admitted journey count")
	}
	want := DatasetSettings(p)
	if len(want) != len(datasets) {
		return fmt.Errorf("CSV dataset observations do not match the configuration")
	}
	var assigned, exhausted int64
	for i, d := range datasets {
		settings := d
		settings.RowsAssigned = 0
		settings.UsersExhausted = 0
		if settings != want[i] || d.RowsAssigned < 0 || d.RowsAssigned > started-assigned || d.UsersExhausted < 0 || d.UsersExhausted > int64(p.VUs) || (d.OnEOF != "stop_user" && d.UsersExhausted != 0) {
			return fmt.Errorf("invalid CSV dataset settings or counts")
		}
		if d.OnEOF == "stop_user" {
			if (d.Sharing == "shared" && d.RowsAssigned > int64(d.Rows)) || (d.Sharing == "per_user" && d.RowsAssigned > int64(d.Rows)*int64(p.VUs)) {
				return fmt.Errorf("CSV rows were reused past EOF")
			}
		}
		assigned += d.RowsAssigned
		exhausted += d.UsersExhausted
	}
	if assigned > started || exhausted > int64(p.VUs) {
		return fmt.Errorf("CSV counts exceed admitted journeys or users")
	}
	return nil
}

func (r Result) validateDatasets(p config.Plan) error {
	if p.HasCSV() != (r.SchemaVersion == 11) {
		return fmt.Errorf("CSV datasets require schema 11")
	}
	if err := ValidateDatasets(p, r.Datasets, r.Journeys.Started); err != nil {
		return err
	}
	var exhausted int64
	for _, d := range r.Datasets {
		if d.UsersExhausted > 0 {
			minimum := int64(d.Rows)
			if d.Sharing == "per_user" {
				minimum *= d.UsersExhausted
			}
			if d.RowsAssigned < minimum {
				return fmt.Errorf("CSV exhaustion precedes row assignment")
			}
		}
		found := false
		for _, s := range r.Scenarios {
			if s.Name == d.Scenario {
				found = true
				if d.RowsAssigned != s.Journeys.Started {
					return fmt.Errorf("CSV assignments disagree with scenario journeys")
				}
			}
		}
		if !found {
			return fmt.Errorf("CSV scenario result is missing")
		}
		exhausted += d.UsersExhausted
	}
	if r.FailurePolicy != nil && exhausted+r.FailurePolicy.UsersStopped > int64(p.VUs) {
		return fmt.Errorf("CSV and failure stops exceed the user count")
	}
	if r.StopReason == "data_exhausted" && (r.Interrupted || exhausted == 0 || (r.UserSchedule != nil && r.UserSchedule.Users.Started != int64(p.VUs))) {
		return fmt.Errorf("data_exhausted requires normal CSV exhaustion")
	}
	if r.StopReason == "iterations" && exhausted != 0 {
		return fmt.Errorf("CSV users stopped before all iterations completed")
	}
	return nil
}
