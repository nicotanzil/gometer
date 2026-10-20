package engine

import (
	"fmt"
	"math"
	"reflect"
	"time"

	"gometer/internal/config"
	"gometer/internal/diagnostics"
	"gometer/internal/metrics"
)

type Assignment struct {
	Index    int       `json:"index"`
	Count    int       `json:"count"`
	Capacity int       `json:"capacity"`
	StartAt  time.Time `json:"start_at"`
}

func (a Assignment) Validate(p config.Plan) error {
	capacity := p.MaxInFlight / max(a.Count, 1)
	if a.Index < p.MaxInFlight%max(a.Count, 1) {
		capacity++
	}
	if p.Mode != "rate" || a.Count < 1 || a.Count > config.MaxWorkers || a.Count > p.MaxInFlight || a.Index < 0 || a.Index >= a.Count || a.Capacity != capacity || a.StartAt.IsZero() {
		return fmt.Errorf("invalid distributed rate assignment")
	}
	return nil
}

type WorkerStatus struct {
	Name          string               `json:"name"`
	URL           string               `json:"url"`
	Index         int                  `json:"index"`
	Capacity      int                  `json:"capacity"`
	Planned       int64                `json:"planned_arrivals"`
	Status        string               `json:"status"`
	Error         string               `json:"error,omitempty"`
	ClockOffsetMS float64              `json:"clock_offset_ms"`
	RoundTripMS   float64              `json:"round_trip_ms"`
	Requests      metrics.Snapshot     `json:"requests"`
	Journeys      JourneyMetrics       `json:"journeys"`
	Generator     *diagnostics.Summary `json:"generator,omitempty"`
}

// Timeline points keep only worker counters; detailed resource summaries live
// in the final result. This preserves the dashboard's 16 MiB bundle bound.
type WorkerPoint struct {
	Index         int    `json:"index"`
	Status        string `json:"status"`
	Scheduled     int64  `json:"scheduled"`
	Started       int64  `json:"started"`
	Completed     int64  `json:"completed"`
	Dropped       int64  `json:"dropped"`
	InFlight      int64  `json:"in_flight"`
	HTTPCompleted int64  `json:"http_completed"`
}

func WorkerPoints(workers []WorkerStatus, workload string) []WorkerPoint {
	var points []WorkerPoint
	for _, w := range workers {
		a := journeyMetrics(w.Requests)
		if workload == "journeys" {
			a = w.Journeys
		}
		points = append(points, WorkerPoint{w.Index, w.Status, a.Scheduled, a.Started, a.Completed, a.DroppedCapacity + a.DroppedScheduler, a.InFlight, w.Requests.Completed})
	}
	return points
}

type Distribution struct {
	Complete                  bool           `json:"complete"`
	PlannedArrivals           int64          `json:"planned_arrivals"`
	UnreportedPlannedArrivals int64          `json:"unreported_planned_arrivals"`
	PeakInFlightUpperBound    bool           `json:"peak_in_flight_upper_bound"`
	Failure                   string         `json:"failure,omitempty"`
	Workers                   []WorkerStatus `json:"workers"`
}

type LiveData struct {
	Requests metrics.State `json:"requests"`
	Journeys metrics.State `json:"journeys"`
}
type PhaseData struct {
	Requests metrics.State `json:"requests"`
	Journeys metrics.State `json:"journeys"`
}
type ScenarioData struct {
	Journeys         metrics.State   `json:"journeys"`
	Steps            []metrics.State `json:"steps"`
	Skipped          []int64         `json:"skipped"`
	Executed         []int64         `json:"executed,omitempty"`
	SkippedCondition []int64         `json:"skipped_condition,omitempty"`
	Controls         []ControlCounts `json:"controls,omitempty"`
}
type AggregateData struct {
	LiveData
	Phases    []PhaseData    `json:"phases"`
	Scenarios []ScenarioData `json:"scenarios"`
}

func (r *runner) aggregateData() *AggregateData {
	a := &AggregateData{LiveData: LiveData{Requests: r.total.State(), Journeys: r.totalJourneys.State()}}
	for i := range r.phases {
		a.Phases = append(a.Phases, PhaseData{r.phases[i].State(), r.phaseJourneys[i].State()})
	}
	for _, stats := range r.scenarioStats {
		s := ScenarioData{Journeys: stats.journeys.State()}
		for i, step := range stats.steps {
			s.Steps = append(s.Steps, step.State())
			s.Skipped = append(s.Skipped, stats.skipped[i].Load())
			if r.plan.HasControlFlow() {
				s.Executed = append(s.Executed, stats.executed[i].Load())
				s.SkippedCondition = append(s.SkippedCondition, stats.skippedCondition[i].Load())
			}
		}
		for i := range stats.controls {
			s.Controls = append(s.Controls, stats.controls[i].snapshot())
		}
		a.Scenarios = append(a.Scenarios, s)
	}
	return a
}

func (r *runner) firstSlot(baseID int64) int64 {
	if r.shard == nil {
		return 0
	}
	n := int64(r.shard.Count)
	return (int64(r.shard.Index) - baseID%n + n) % n
}
func (r *runner) dropSlots(phase int, first, end int64, scheduler bool, baseID int64) {
	if first >= end {
		return
	}
	if r.shard == nil {
		r.dropRange(phase, end-first, scheduler, baseID+first)
		return
	}
	n := (end-1-first)/int64(r.shard.Count) + 1
	if !r.plan.IsJourney() {
		r.total.Drop(n, scheduler)
		r.phases[phase].Drop(n, scheduler)
	}
	r.totalJourneys.Drop(n, scheduler)
	r.phaseJourneys[phase].Drop(n, scheduler)
	if r.plan.IsJourney() {
		for i, count := range r.plan.ScenarioStride(baseID+first, n, int64(r.shard.Count)) {
			r.scenarioStats[i].journeys.Drop(count, scheduler)
		}
	}
}

// PlannedForWorker counts assigned global ordinals across all phases. Pauses
// have no slots and phase boundaries do not reset the worker assignment.
func PlannedForWorker(p config.Plan, index, count int) int64 {
	var base, total int64
	for _, phase := range p.Phases {
		n := slots(phase.Duration, phase.Rate)
		first := (int64(index) - base%int64(count) + int64(count)) % int64(count)
		if first < n {
			total += (n-1-first)/int64(count) + 1
		}
		base += n
	}
	return total
}

func MergeLive(data []LiveData, elapsed time.Duration) (metrics.Snapshot, JourneyMetrics, error) {
	var requests, journeys []metrics.State
	for _, d := range data {
		requests = append(requests, d.Requests)
		journeys = append(journeys, d.Journeys)
	}
	r, err := metrics.Merge(requests)
	if err != nil {
		return metrics.Snapshot{}, JourneyMetrics{}, err
	}
	j, err := metrics.Merge(journeys)
	if err != nil {
		return metrics.Snapshot{}, JourneyMetrics{}, err
	}
	return r.Snapshot(elapsed), journeyMetrics(j.Snapshot(elapsed)), nil
}

func validateTotals(total metrics.State, parts []metrics.State) error {
	if err := total.Validate(); err != nil {
		return err
	}
	merged, err := metrics.Merge(parts)
	if err != nil {
		return err
	}
	got := merged.State()
	a, b := total.Counts, got.Counts
	a.PeakInFlight = 0
	b.PeakInFlight = 0
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("worker aggregate counts disagree with their component counts")
	}
	for i, left := range []metrics.HistogramState{total.Latency, total.ScheduledLatency, total.StartDelay} {
		right := []metrics.HistogramState{got.Latency, got.ScheduledLatency, got.StartDelay}[i]
		if left.Count != right.Count || left.MinNS != right.MinNS || left.MaxNS != right.MaxNS || !reflect.DeepEqual(left.Buckets, right.Buckets) || math.Abs(left.SumNS-right.SumNS) > max(1, math.Abs(left.SumNS))*1e-8 {
			return fmt.Errorf("worker aggregate histogram disagrees with its components")
		}
	}
	return nil
}
func validateAggregate(p config.Plan, a *AggregateData) error {
	var requests, journeys []metrics.State
	for _, phase := range a.Phases {
		requests = append(requests, phase.Requests)
		journeys = append(journeys, phase.Journeys)
	}
	if err := validateTotals(a.Requests, requests); err != nil {
		return err
	}
	if err := validateTotals(a.Journeys, journeys); err != nil {
		return err
	}
	if !p.IsJourney() {
		return nil
	}
	requests = nil
	journeys = nil
	for i, scenario := range a.Scenarios {
		if len(scenario.Steps) != len(p.ScenarioPlans[i].Steps) || len(scenario.Skipped) != len(scenario.Steps) {
			return fmt.Errorf("worker step shape mismatch")
		}
		if p.HasControlFlow() {
			if len(scenario.Executed) != len(scenario.Steps) || len(scenario.SkippedCondition) != len(scenario.Steps) || len(scenario.Controls) != len(p.ScenarioPlans[i].Controls) {
				return fmt.Errorf("worker flow shape mismatch")
			}
			for j, control := range scenario.Controls {
				if !control.validFor(p.ScenarioPlans[i].Controls[j]) || !withinVisits(control.Entered, scenario.Journeys.Counts.Started, config.MaxFlowExecutions) {
					return fmt.Errorf("invalid worker control counters")
				}
			}
		}
		journeys = append(journeys, scenario.Journeys)
		for j, step := range scenario.Steps {
			executed := step.Counts.Started
			if p.HasControlFlow() {
				executed = scenario.Executed[j]
				if scenario.SkippedCondition[j] < 0 || scenario.SkippedCondition[j] > scenario.Skipped[j] {
					return fmt.Errorf("invalid conditional skip count")
				}
			}
			if executed < 0 || executed > step.Counts.Started || !withinVisits(step.Counts.Started, executed, config.MaxFlowExecutions) || scenario.Skipped[j] < 0 || executed+scenario.Skipped[j] != scenario.Journeys.Counts.Started {
				return fmt.Errorf("worker skipped step counts disagree")
			}
			requests = append(requests, step)
		}
	}
	if err := validateTotals(a.Requests, requests); err != nil {
		return err
	}
	return validateTotals(a.Journeys, journeys)
}

// Combine rebuilds final counts and quantiles from worker buckets, then applies
// thresholds once to the global population. Worker resource summaries stay per
// process; CPU percentages and heap peaks are never averaged into one process.
func Combine(c config.Config, start time.Time, load, total time.Duration, parts []Result, distribution Distribution, interrupted bool) (Result, error) {
	p, err := c.Validate()
	if err != nil {
		return Result{}, err
	}
	var live []LiveData
	for _, part := range parts {
		if part.Merge == nil || part.Name != p.Name || part.URL != p.URL || part.Mode != p.Mode || part.Insecure != p.Insecure || len(part.Merge.Phases) != len(p.Phases) || len(part.Merge.Scenarios) != len(p.ScenarioPlans) {
			return Result{}, fmt.Errorf("worker result does not match its configuration")
		}
		if part.Merge.Requests.Counts.InFlight != 0 || part.Merge.Journeys.Counts.InFlight != 0 {
			return Result{}, fmt.Errorf("worker final result has active work")
		}
		if err := validateAggregate(p, part.Merge); err != nil {
			return Result{}, err
		}
		live = append(live, part.Merge.LiveData)
	}
	requests, journeys, err := MergeLive(live, load)
	if err != nil {
		return Result{}, err
	}
	workload := "requests"
	if p.IsJourney() {
		workload = "journeys"
	}
	r := Result{SchemaVersion: 4, Name: p.Name, URL: p.URL, Method: p.Method, Mode: p.Mode, Insecure: p.Insecure, ConcurrencyLimit: p.MaxInFlight, StartedAt: start.UTC(), PlannedLoadSeconds: p.LoadDuration.Seconds(), LoadSeconds: load.Seconds(), TotalSeconds: max(load, total).Seconds(), Interrupted: interrupted, Workload: workload, ArrivalDistribution: p.ArrivalDistribution, Seed: p.Seed, Snapshot: requests, Journeys: journeys, Distribution: &distribution}
	if p.HasControlFlow() {
		r.SchemaVersion = 6
	}
	var offset time.Duration
	for i, phase := range p.Phases {
		var states []LiveData
		for _, part := range parts {
			d := part.Merge.Phases[i]
			states = append(states, LiveData{d.Requests, d.Journeys})
		}
		elapsed := min(max(load-offset, time.Duration(0)), phase.Duration)
		rs, js, err := MergeLive(states, elapsed)
		if err != nil {
			return Result{}, err
		}
		target := phase.Rate
		if p.IsJourney() {
			target = 0
		}
		r.Phases = append(r.Phases, PhaseResult{Name: phase.Name, TargetRPS: target, TargetRate: phase.Rate, PlannedSeconds: phase.Duration.Seconds(), LoadSeconds: elapsed.Seconds(), Snapshot: rs, Journeys: js})
		offset += phase.Duration
	}
	arrivals := r.Arrivals().Scheduled
	if !interrupted && distribution.Complete && arrivals != distribution.PlannedArrivals {
		r.Distribution.Complete = false
		r.Distribution.Failure = "reported arrivals do not cover the assigned schedule"
	}
	r.Distribution.UnreportedPlannedArrivals = max(int64(0), distribution.PlannedArrivals-arrivals)
	r.evaluate(p)
	if p.IsJourney() {
		r.Checks = append(r.Checks, journeyChecks("journeys.", journeys, p.JourneyChecks)...)
		for i, scenario := range p.ScenarioPlans {
			var states []metrics.State
			for _, part := range parts {
				d := part.Merge.Scenarios[i]
				if len(d.Steps) != len(scenario.Steps) || len(d.Skipped) != len(scenario.Steps) {
					return Result{}, fmt.Errorf("worker step shape mismatch")
				}
				states = append(states, d.Journeys)
			}
			collector, err := metrics.Merge(states)
			if err != nil {
				return Result{}, err
			}
			s := ScenarioResult{Name: scenario.Name, Weight: scenario.Weight, Passed: true, Journeys: journeyMetrics(collector.Snapshot(load))}
			for j, step := range scenario.Steps {
				states = nil
				var skipped, executed, conditional int64
				for _, part := range parts {
					d := part.Merge.Scenarios[i]
					states = append(states, d.Steps[j])
					if d.Skipped[j] < 0 || d.Skipped[j] > d.Journeys.Counts.Started {
						return Result{}, fmt.Errorf("invalid skipped step count")
					}
					skipped += d.Skipped[j]
					if p.HasControlFlow() {
						executed += d.Executed[j]
						conditional += d.SkippedCondition[j]
					}
				}
				collector, err := metrics.Merge(states)
				if err != nil {
					return Result{}, err
				}
				result := StepResult{Name: step.Name, Method: step.Method, Skipped: skipped, Snapshot: collector.Snapshot(load)}
				if p.HasControlFlow() {
					result.Path = step.Path
					result.ExecutedJourneys = executed
					result.SkippedCondition = conditional
				}
				s.Steps = append(s.Steps, result)
			}
			for j, control := range scenario.Controls {
				counts := ControlCounts{}
				for _, part := range parts {
					counts.add(part.Merge.Scenarios[i].Controls[j])
				}
				s.Controls = append(s.Controls, ControlResult{Name: control.Name, Kind: control.Kind, Path: control.Path, ControlCounts: counts})
			}
			s.Checks = journeyChecks("", s.Journeys, scenario.Checks)
			for _, check := range s.Checks {
				s.Passed = s.Passed && check.Passed
				check.Name = "scenario[" + s.Name + "]." + check.Name
				r.Checks = append(r.Checks, check)
			}
			r.Scenarios = append(r.Scenarios, s)
		}
	}
	r.Checks = append(r.Checks, Check{Name: "worker_results_complete", Passed: r.Distribution.Complete, Actual: boolNumber(r.Distribution.Complete), Limit: 1, Unit: "boolean"})
	for _, check := range r.Checks {
		r.Passed = r.Passed && check.Passed
	}
	return r, nil
}
