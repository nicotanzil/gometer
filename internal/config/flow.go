package config

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"gometer/internal/correlation"
)

const MaxFlowDepth = 8
const MaxLoopIterations = 1000
const MaxFlowExecutions = 10000

// Conditions use explicit comparisons, never executable expressions or scripts.
type Condition struct {
	Variable string  `json:"variable"`
	Operator string  `json:"operator"`
	Value    *string `json:"value,omitempty"`
}
type Branch struct {
	Condition Condition `json:"condition"`
	Then      []Step    `json:"then"`
	Else      []Step    `json:"else,omitempty"`
}
type Loop struct {
	Count         *int       `json:"count,omitempty"`
	While         *Condition `json:"while,omitempty"`
	MaxIterations int        `json:"max_iterations,omitempty"`
	IndexVariable string     `json:"index_variable,omitempty"`
	Steps         []Step     `json:"steps"`
}
type CompiledCondition struct {
	Condition
	Expected correlation.Template
}
type ControlPlan struct {
	Name, Kind, Path     string
	Count, MaxIterations int
}
type FlowNode struct {
	Kind                 string
	Request, Control     int
	Condition            CompiledCondition
	Then, Else, Body     []FlowNode
	Count, MaxIterations int
	IndexVariable        string
}

func (p Plan) HasControlFlow() bool {
	for _, s := range p.ScenarioPlans {
		if len(s.Controls) > 0 {
			return true
		}
	}
	return false
}

func compileCondition(c Condition, known map[string]bool) (CompiledCondition, error) {
	if !correlation.NameValid(c.Variable) {
		return CompiledCondition{}, fmt.Errorf("condition requires a valid variable name")
	}
	if c.Operator == "" {
		c.Operator = "equals"
	}
	result := CompiledCondition{Condition: c}
	if c.Operator == "exists" || c.Operator == "not_exists" {
		if c.Value != nil {
			return result, fmt.Errorf("existence conditions do not take a value")
		}
		return result, nil
	}
	switch c.Operator {
	case "equals", "not_equals", "contains", "not_contains", "gt", "gte", "lt", "lte":
	default:
		return result, fmt.Errorf("unsupported condition operator")
	}
	if !known[c.Variable] {
		return result, fmt.Errorf("condition variable %q is not defined before this node", c.Variable)
	}
	if c.Value == nil {
		return result, fmt.Errorf("comparison conditions require a value (which may be empty)")
	}
	var err error
	result.Expected, err = correlation.Compile(*c.Value, known)
	return result, err
}

// Evaluate returns an error for missing comparison inputs and invalid numeric
// operands. Such an error fails the journey without inventing an HTTP attempt.
func (c CompiledCondition) Evaluate(values map[string]string) (bool, error) {
	actual, present := values[c.Variable]
	if c.Operator == "exists" {
		return present, nil
	}
	if c.Operator == "not_exists" {
		return !present, nil
	}
	if !present {
		return false, fmt.Errorf("condition variable is unavailable")
	}
	expected, err := c.Expected.Expand(values)
	if err != nil {
		return false, err
	}
	switch c.Operator {
	case "equals":
		return actual == expected, nil
	case "not_equals":
		return actual != expected, nil
	case "contains":
		return strings.Contains(actual, expected), nil
	case "not_contains":
		return !strings.Contains(actual, expected), nil
	}
	a, errA := strconv.ParseFloat(actual, 64)
	b, errB := strconv.ParseFloat(expected, 64)
	if errA != nil || errB != nil || math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false, fmt.Errorf("numeric conditions require finite numbers")
	}
	switch c.Operator {
	case "gt":
		return a > b, nil
	case "gte":
		return a >= b, nil
	case "lt":
		return a < b, nil
	case "lte":
		return a <= b, nil
	}
	return false, fmt.Errorf("unsupported condition operator")
}

type flowCompiler struct {
	plan     *Plan
	scenario *ScenarioPlan
	base     *url.URL
	names    map[string]bool
	nodes    int
}

func cloneKnown(values map[string]bool) map[string]bool {
	result := map[string]bool{}
	for k, v := range values {
		result[k] = v
	}
	return result
}
func guardedKnown(values map[string]bool, c Condition, truth bool) map[string]bool {
	result := cloneKnown(values)
	if (c.Operator == "exists" && truth) || (c.Operator == "not_exists" && !truth) {
		result[c.Variable] = true
	}
	return result
}
func (f *flowCompiler) sequence(steps []Step, known map[string]bool, path string, depth int) ([]FlowNode, map[string]bool, int, error) {
	if depth > MaxFlowDepth {
		return nil, nil, 0, fmt.Errorf("%s: control flow nesting exceeds %d levels", path, MaxFlowDepth)
	}
	known = cloneKnown(known)
	var nodes []FlowNode
	cost := 0
	for i, s := range steps {
		p := fmt.Sprintf("%s[%d]", path, i)
		f.nodes++
		if f.nodes > 50 {
			return nil, nil, 0, fmt.Errorf("%s: at most 50 flow nodes are supported per journey", p)
		}
		if s.Name == "" {
			s.Name = fmt.Sprintf("Step %d", f.nodes)
		}
		if len(s.Name) > 128 || f.names[s.Name] {
			return nil, nil, 0, fmt.Errorf("%s: step names must be unique and at most 128 bytes", p)
		}
		f.names[s.Name] = true
		n := FlowNode{Kind: "request", Request: len(f.scenario.Steps), Control: -1}
		nodeCost := 1
		if s.If != nil || s.Loop != nil {
			if (s.If != nil && s.Loop != nil) || s.URL != "" || s.Method != "" || s.Body != "" || s.Pause != "" || len(s.Headers) > 0 || len(s.Extract) > 0 || len(s.Assertions) > 0 || len(s.SuccessStatuses) > 0 {
				return nil, nil, 0, fmt.Errorf("%s: use exactly one HTTP request, if, or loop per node", p)
			}
			n.Control = len(f.scenario.Controls)
			kind := "branch"
			if s.Loop != nil {
				kind = "loop"
			}
			n.Kind = kind
			f.scenario.Controls = append(f.scenario.Controls, ControlPlan{Name: s.Name, Kind: kind, Path: p})
			if s.If != nil {
				if len(s.If.Then)+len(s.If.Else) == 0 {
					return nil, nil, 0, fmt.Errorf("%s.if needs at least one child node", p)
				}
				c, err := compileCondition(s.If.Condition, known)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("%s.if.condition: %w", p, err)
				}
				n.Condition = c
				then, kt, ct, err := f.sequence(s.If.Then, guardedKnown(known, c.Condition, true), p+".if.then", depth+1)
				if err != nil {
					return nil, nil, 0, err
				}
				other, ke, ce, err := f.sequence(s.If.Else, guardedKnown(known, c.Condition, false), p+".if.else", depth+1)
				if err != nil {
					return nil, nil, 0, err
				}
				n.Then, n.Else = then, other
				nodeCost += max(ct, ce)
				known = map[string]bool{}
				for k, v := range kt {
					if ke[k] {
						known[k] = v
					}
				}
			} else {
				loop := s.Loop
				if (loop.Count == nil) == (loop.While == nil) || len(loop.Steps) == 0 {
					return nil, nil, 0, fmt.Errorf("%s.loop requires child steps and exactly one of count or while", p)
				}
				iterations := loop.MaxIterations
				bodyKnown := cloneKnown(known)
				if loop.Count != nil {
					iterations = *loop.Count
					n.Count = iterations
					if loop.MaxIterations != 0 {
						return nil, nil, 0, fmt.Errorf("%s.loop: max_iterations applies only to while loops", p)
					}
				} else {
					c, err := compileCondition(*loop.While, known)
					if err != nil {
						return nil, nil, 0, fmt.Errorf("%s.loop.while: %w", p, err)
					}
					n.Condition = c
					bodyKnown = guardedKnown(known, c.Condition, true)
				}
				if iterations < 1 || iterations > MaxLoopIterations {
					return nil, nil, 0, fmt.Errorf("%s.loop: count or max_iterations must be between 1 and %d", p, MaxLoopIterations)
				}
				n.MaxIterations = iterations
				n.IndexVariable = loop.IndexVariable
				if loop.IndexVariable != "" {
					if !variableAllowed(loop.IndexVariable) {
						return nil, nil, 0, fmt.Errorf("%s.loop: invalid index_variable", p)
					}
					bodyKnown[loop.IndexVariable] = true
				}
				body, kb, cb, err := f.sequence(loop.Steps, bodyKnown, p+".loop.steps", depth+1)
				if err != nil {
					return nil, nil, 0, err
				}
				n.Body = body
				nodeCost += iterations * cb
				if loop.Count != nil {
					if !known[loop.IndexVariable] {
						delete(kb, loop.IndexVariable)
					}
					known = kb
				}
			}
		} else {
			compiled, err := f.plan.compileStep(s, f.nodes-1, known, f.base)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("%s: %w", p, err)
			}
			compiled.Path = p
			f.scenario.Steps = append(f.scenario.Steps, compiled)
		}
		if n.Control >= 0 {
			f.scenario.Controls[n.Control].Count = n.Count
			f.scenario.Controls[n.Control].MaxIterations = n.MaxIterations
		}
		cost += nodeCost
		if cost > MaxFlowExecutions {
			return nil, nil, 0, fmt.Errorf("%s: exceeds %d possible node executions per journey", p, MaxFlowExecutions)
		}
		nodes = append(nodes, n)
	}
	return nodes, known, cost, nil
}
