package engine

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"gometer/internal/config"
)

// ValidateFlow checks the schema contract used by imported journey bundles.
func (r Result) ValidateFlow(p config.Plan) error {
	if !p.HasControlFlow() && r.SchemaVersion != 6 {
		return nil
	}
	if (r.SchemaVersion < 6 || r.SchemaVersion > 11) || len(r.Scenarios) != len(p.ScenarioPlans) {
		return fmt.Errorf("control flows require schema 6–10 with matching journeys")
	}
	var requests, journeys int64
	for i, s := range r.Scenarios {
		plan := p.ScenarioPlans[i]
		if s.Name != plan.Name || len(s.Steps) != len(plan.Steps) || len(s.Controls) != len(plan.Controls) || s.Journeys.Started < 0 || s.Journeys.Started != s.Journeys.Completed {
			return fmt.Errorf("journey shape or counts disagree")
		}
		journeys += s.Journeys.Completed
		for j, step := range s.Steps {
			expected := plan.Steps[j]
			if step.Path != expected.Path || step.Name != expected.Name || step.Method != expected.Method || step.Started < 0 || step.Started != step.Completed || step.Succeeded < 0 || step.Failed < 0 || step.Succeeded+step.Failed != step.Completed || step.ExecutedJourneys < 0 || step.ExecutedJourneys > step.Started || !withinVisits(step.Started, step.ExecutedJourneys, config.MaxFlowExecutions) || step.Skipped < 0 || step.ExecutedJourneys+step.Skipped != s.Journeys.Started || step.SkippedCondition < 0 || step.SkippedCondition > step.Skipped || step.Latency.Samples != step.Completed {
				return fmt.Errorf("request counts or identity disagree")
			}
			requests += step.Completed
		}
		for j, c := range s.Controls {
			expected := plan.Controls[j]
			if c.Name != expected.Name || c.Kind != expected.Kind || c.Path != expected.Path || !c.ControlCounts.validFor(expected) || !withinVisits(c.Entered, s.Journeys.Started, config.MaxFlowExecutions) {
				return fmt.Errorf("control counts or identity disagree")
			}
		}
	}
	if requests != r.Completed || journeys != r.Journeys.Completed {
		return fmt.Errorf("flow totals disagree")
	}
	return nil
}

// Counters are bounded by the static plan, not the number of loop executions.
type ControlCounts struct {
	Entered         int64 `json:"entered"`
	Completed       int64 `json:"completed"`
	Failed          int64 `json:"failed"`
	ConditionTrue   int64 `json:"condition_true"`
	ConditionFalse  int64 `json:"condition_false"`
	Iterations      int64 `json:"iterations"`
	ConditionErrors int64 `json:"condition_errors"`
}
type ControlResult struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path"`
	ControlCounts
}
type controlStats struct{ entered, completed, failed, truth, falsity, iterations, errors atomic.Int64 }

func (s *controlStats) snapshot() ControlCounts {
	return ControlCounts{s.entered.Load(), s.completed.Load(), s.failed.Load(), s.truth.Load(), s.falsity.Load(), s.iterations.Load(), s.errors.Load()}
}
func (c *ControlCounts) add(other ControlCounts) {
	c.Entered += other.Entered
	c.Completed += other.Completed
	c.Failed += other.Failed
	c.ConditionTrue += other.ConditionTrue
	c.ConditionFalse += other.ConditionFalse
	c.Iterations += other.Iterations
	c.ConditionErrors += other.ConditionErrors
}
func (c ControlCounts) valid() bool {
	return c.Entered >= 0 && c.Completed >= 0 && c.Failed >= 0 && c.ConditionTrue >= 0 && c.ConditionFalse >= 0 && c.Iterations >= 0 && c.ConditionErrors >= 0 && c.Completed <= c.Entered && c.Failed == c.Entered-c.Completed && c.ConditionErrors <= c.Failed
}

// Compare against visits*limit without overflowing on an imported counter.
func withinVisits(value, visits int64, limit int) bool {
	if value < 0 || visits < 0 || limit < 1 {
		return false
	}
	quotient, remainder := value/int64(limit), value%int64(limit)
	return quotient < visits || (quotient == visits && remainder == 0)
}
func (c ControlCounts) validFor(plan config.ControlPlan) bool {
	if !c.valid() {
		return false
	}
	if plan.Kind == "branch" {
		return c.Iterations == 0 && c.ConditionTrue <= c.Entered && c.ConditionFalse <= c.Entered-c.ConditionTrue && c.ConditionErrors == c.Entered-c.ConditionTrue-c.ConditionFalse
	}
	if !withinVisits(c.Iterations, c.Entered, plan.MaxIterations) {
		return false
	}
	if plan.Count > 0 {
		return c.ConditionTrue == 0 && c.ConditionFalse == 0 && c.ConditionErrors == 0 && c.Iterations/int64(plan.Count) >= c.Completed
	}
	return c.ConditionFalse == c.Completed && c.ConditionTrue >= c.Iterations && c.ConditionTrue-c.Iterations <= c.Failed
}

type flowExecution struct {
	runner            *runner
	ctx               context.Context
	client            *http.Client
	scenario          config.ScenarioPlan
	stats             *scenarioStats
	values            map[string]string
	scheduled         time.Time
	phase, executions int
	attempted         bool
	failed            bool
	visited, bypassed []bool
}

func (e *flowExecution) bypass(nodes []config.FlowNode) {
	for _, n := range nodes {
		if n.Kind == "request" {
			e.bypassed[n.Request] = true
		}
		e.bypass(n.Then)
		e.bypass(n.Else)
		e.bypass(n.Body)
	}
}
func (e *flowExecution) sequence(nodes []config.FlowNode) string {
	for _, n := range nodes {
		if e.ctx.Err() != nil {
			return contextCategory(e.ctx)
		}
		e.executions++
		if e.executions > config.MaxFlowExecutions {
			return "execution_limit"
		}
		if n.Kind != "request" {
			if failure := e.control(n); failure != "" {
				return failure
			}
			continue
		}
		step := e.scenario.Steps[n.Request]
		due := time.Now()
		if !e.attempted {
			due = e.scheduled
		}
		e.attempted = true
		e.visited[n.Request] = true
		if failure := e.runner.step(e.ctx, e.client, step, e.values, due, e.phase, e.stats.steps[n.Request]); failure != "" {
			e.failed = true
			if e.ctx.Err() != nil {
				return contextCategory(e.ctx)
			}
			if e.runner.plan.ErrorAction() == config.ContinueJourney {
				continue
			}
			return "step_failed"
		}
		if step.PauseDuration > 0 && !waitUntil(e.ctx, time.Now().Add(step.PauseDuration)) {
			return contextCategory(e.ctx)
		}
	}
	return ""
}
func (e *flowExecution) condition(n config.FlowNode, stats *controlStats) (bool, string) {
	truth, err := n.Condition.Evaluate(e.values)
	if err != nil {
		stats.errors.Add(1)
		return false, "condition"
	}
	if truth {
		stats.truth.Add(1)
	} else {
		stats.falsity.Add(1)
	}
	return truth, ""
}
func (e *flowExecution) control(n config.FlowNode) (failure string) {
	stats := &e.stats.controls[n.Control]
	stats.entered.Add(1)
	defer func() {
		if failure == "" {
			stats.completed.Add(1)
		} else {
			stats.failed.Add(1)
		}
	}()
	if n.Kind == "branch" {
		truth, err := e.condition(n, stats)
		if err != "" {
			return err
		}
		if truth {
			e.bypass(n.Else)
			return e.sequence(n.Then)
		}
		e.bypass(n.Then)
		return e.sequence(n.Else)
	}
	old, present := e.values[n.IndexVariable]
	if n.IndexVariable != "" {
		defer func() {
			if present {
				e.values[n.IndexVariable] = old
			} else {
				delete(e.values, n.IndexVariable)
			}
		}()
	}
	for i := 0; ; i++ {
		if e.ctx.Err() != nil {
			return contextCategory(e.ctx)
		}
		if n.Count > 0 {
			if i >= n.Count {
				return ""
			}
		} else {
			truth, err := e.condition(n, stats)
			if err != "" {
				return err
			}
			if !truth {
				if i == 0 {
					e.bypass(n.Body)
				}
				return ""
			}
			if i >= n.MaxIterations {
				return "loop_limit"
			}
		}
		if n.IndexVariable != "" {
			e.values[n.IndexVariable] = strconv.Itoa(i + 1)
		}
		stats.iterations.Add(1)
		if failure = e.sequence(n.Body); failure != "" {
			return failure
		}
	}
}
