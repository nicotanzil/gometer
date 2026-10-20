package config

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gometer/internal/correlation"
)

type JourneyThresholds struct {
	P95                 string   `json:"p95,omitempty"`
	MaxErrorPercent     *float64 `json:"max_error_percent"`
	MaxDropPercent      *float64 `json:"max_drop_percent,omitempty"`
	MinStartedPerSecond *float64 `json:"min_started_per_second,omitempty"`
}

type Extraction struct {
	JSONPointer *string `json:"json_pointer,omitempty"`
	Header      string  `json:"header,omitempty"`
}

type Assertion struct {
	JSONPointer  *string `json:"json_pointer,omitempty"`
	Header       string  `json:"header,omitempty"`
	Equals       *string `json:"equals,omitempty"`
	BodyContains *string `json:"body_contains,omitempty"`
}

type Step struct {
	Name            string                `json:"name"`
	URL             string                `json:"url,omitempty"`
	If              *Branch               `json:"if,omitempty"`
	Loop            *Loop                 `json:"loop,omitempty"`
	Method          string                `json:"method,omitempty"`
	Headers         map[string]string     `json:"headers,omitempty"`
	Body            string                `json:"body,omitempty"`
	SuccessStatuses []int                 `json:"success_statuses,omitempty"`
	Extract         map[string]Extraction `json:"extract,omitempty"`
	Assertions      []Assertion           `json:"assertions,omitempty"`
	Pause           string                `json:"pause,omitempty"`
}

type Scenario struct {
	Name       string              `json:"name"`
	Weight     *int                `json:"weight,omitempty"`
	Variables  map[string]string   `json:"variables,omitempty"`
	Data       []map[string]string `json:"data,omitempty"`
	CSV        *CSV                `json:"csv,omitempty"`
	Steps      []Step              `json:"steps"`
	Thresholds JourneyThresholds   `json:"thresholds,omitempty"`
}

type CompiledExtraction struct {
	Name    string
	Pointer correlation.Pointer
	Header  string
}
type CompiledAssertion struct {
	Pointer        correlation.Pointer
	Header         string
	IsJSON, IsBody bool
	Expected       correlation.Template
}
type StepPlan struct {
	Step
	Path                      string
	URLTemplate, BodyTemplate correlation.Template
	HeaderTemplates           map[string]correlation.Template
	Extractions               []CompiledExtraction
	Checks                    []CompiledAssertion
	CaptureBody               bool
	PauseDuration             time.Duration
}

type CompiledJourneyThresholds struct {
	JourneyThresholds
	P95Limit time.Duration
}
type ScenarioPlan struct {
	Name        string
	Weight      int64
	WeightStart int64
	Variables   map[string]string
	Data        []map[string]string
	CSV         *CSVPlan
	Steps       []StepPlan
	Flow        []FlowNode
	Controls    []ControlPlan
	Checks      CompiledJourneyThresholds
}

func (p Plan) IsJourney() bool { return len(p.ScenarioPlans) > 0 }

func (p *Plan) compileScenarios() error {
	if p.ArrivalDistribution != "constant" && p.ArrivalDistribution != "jitter" {
		return fmt.Errorf("arrival_distribution must be constant or jitter")
	}
	if p.Mode != "rate" && p.ArrivalDistribution != "constant" {
		return fmt.Errorf("arrival jitter requires rate mode")
	}
	if len(p.Scenarios) > 20 {
		return fmt.Errorf("at most 20 scenarios are supported")
	}
	var err error
	p.JourneyLimit, err = positiveDuration("journey_timeout", p.JourneyTimeout)
	if err != nil {
		return err
	}
	p.JourneyChecks, err = compileJourneyThresholds(p.JourneyThresholds)
	if err != nil {
		return fmt.Errorf("journey_thresholds: %w", err)
	}
	if len(p.Scenarios) == 0 {
		if len(p.Variables) > 0 {
			return fmt.Errorf("variables require scenarios")
		}
		if p.JourneyLimit != 30*time.Second || p.JourneyThresholds.P95 != "" || p.JourneyThresholds.MaxDropPercent != nil || p.JourneyThresholds.MinStartedPerSecond != nil || (p.JourneyThresholds.MaxErrorPercent != nil && *p.JourneyThresholds.MaxErrorPercent != 0) {
			return fmt.Errorf("journey_timeout and journey_thresholds require scenarios")
		}
		return nil
	}
	if p.Body != "" || p.Method != "GET" {
		return fmt.Errorf("set body and method on scenario steps; top-level method must remain GET")
	}
	if err := ValidateHeaders(p.Headers); err != nil {
		return err
	}
	base, _ := url.Parse(p.URL)
	names := map[string]bool{}
	totalSteps := 0
	for i, s := range p.Scenarios {
		if s.Name == "" || len(s.Name) > 128 || names[s.Name] {
			return fmt.Errorf("scenario names must be nonempty, unique, and at most 128 bytes")
		}
		names[s.Name] = true
		weight := 1
		if s.Weight != nil {
			weight = *s.Weight
		}
		if weight < 1 || weight > 1000 {
			return fmt.Errorf("scenario %q: weight must be between 1 and 1000", s.Name)
		}
		if len(s.Data) > 10000 {
			return fmt.Errorf("scenario %q: at most 10000 data rows are supported", s.Name)
		}
		sp := ScenarioPlan{Name: s.Name, Weight: int64(weight), WeightStart: p.TotalWeight, Variables: map[string]string{}, Data: s.Data}
		if s.CSV != nil {
			if p.Mode != "users" || len(p.Workers) > 0 {
				return fmt.Errorf("CSV datasets currently require local users mode")
			}
			if len(s.Data) > 0 {
				return fmt.Errorf("scenario %q: choose CSV or inline data, not both", s.Name)
			}
			sp.CSV, err = CompileCSV(*s.CSV)
			if err != nil {
				return fmt.Errorf("scenario %q: %w", s.Name, err)
			}
		}
		sp.Checks, err = compileJourneyThresholds(s.Thresholds)
		if err != nil {
			return fmt.Errorf("scenario %q thresholds: %w", s.Name, err)
		}
		known := map[string]bool{"user_id": true, "iteration": true, "journey_id": true}
		for _, values := range []map[string]string{p.Variables, s.Variables} {
			for k, v := range values {
				if !variableAllowed(k) || len(v) > correlation.MaxValueBytes {
					return fmt.Errorf("scenario %q: invalid variable name or value", s.Name)
				}
				known[k] = true
				sp.Variables[k] = v
			}
		}
		for rowIndex, row := range s.Data {
			if len(row) == 0 {
				return fmt.Errorf("scenario %q: data rows must not be empty", s.Name)
			}
			if len(row) != len(s.Data[0]) {
				return fmt.Errorf("scenario %q: data rows must have identical keys", s.Name)
			}
			for k, v := range row {
				if !variableAllowed(k) || len(v) > correlation.MaxValueBytes {
					return fmt.Errorf("scenario %q: invalid data variable", s.Name)
				}
				if _, ok := s.Data[0][k]; !ok {
					return fmt.Errorf("scenario %q: data rows must have identical keys", s.Name)
				}
				if rowIndex == 0 {
					known[k] = true
				}
			}
		}
		if sp.CSV != nil {
			for k := range sp.CSV.Rows[0] {
				known[k] = true
			}
		}
		compiler := flowCompiler{plan: p, scenario: &sp, base: base, names: map[string]bool{}}
		var cost int
		sp.Flow, _, cost, err = compiler.sequence(s.Steps, known, fmt.Sprintf("scenarios[%d].steps", i), 0)
		if err != nil {
			return err
		}
		if compiler.nodes < 1 || len(sp.Steps) == 0 {
			return fmt.Errorf("scenario %q needs at least one HTTP request", s.Name)
		}
		if cost > MaxFlowExecutions {
			return fmt.Errorf("scenario %q exceeds %d possible node executions per journey", s.Name, MaxFlowExecutions)
		}
		totalSteps += compiler.nodes
		if totalSteps > 100 {
			return fmt.Errorf("at most 100 total flow nodes are supported")
		}
		p.TotalWeight += int64(weight)
		if p.TotalWeight > 10000 {
			return fmt.Errorf("total scenario weight must not exceed 10000")
		}
		p.ScenarioPlans = append(p.ScenarioPlans, sp)
	}
	return nil
}

func (p Plan) compileStep(s Step, index int, known map[string]bool, base *url.URL) (StepPlan, error) {
	if s.Name == "" {
		s.Name = fmt.Sprintf("Step %d", index+1)
	}
	if len(s.Name) > 128 {
		return StepPlan{}, fmt.Errorf("step name exceeds 128 bytes")
	}
	if s.Method == "" {
		s.Method = "GET"
	}
	s.Method = strings.ToUpper(s.Method)
	if !isToken(s.Method) {
		return StepPlan{}, fmt.Errorf("invalid HTTP method")
	}
	if s.URL == "" {
		return StepPlan{}, fmt.Errorf("step url is required")
	}
	if err := ValidateHeaders(s.Headers); err != nil {
		return StepPlan{}, err
	}
	headers := map[string]string{}
	for k, v := range p.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	for k, v := range s.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	s.Headers = headers
	if len(s.SuccessStatuses) == 0 {
		s.SuccessStatuses = p.SuccessStatuses
	}
	for _, status := range s.SuccessStatuses {
		if status < 100 || status > 599 {
			return StepPlan{}, fmt.Errorf("invalid success status")
		}
	}
	sp := StepPlan{Step: s, HeaderTemplates: map[string]correlation.Template{}}
	compile := func(raw string) (correlation.Template, error) { return correlation.Compile(raw, known) }
	var err error
	sp.URLTemplate, err = compile(s.URL)
	if err != nil {
		return sp, err
	}
	sp.BodyTemplate, err = compile(s.Body)
	if err != nil {
		return sp, err
	}
	for k, v := range headers {
		sp.HeaderTemplates[k], err = compile(v)
		if err != nil {
			return sp, err
		}
	}
	// Use safe placeholder values for static validation, then validate the actual
	// expanded URL again at execution time. All steps stay on the base origin.
	values := map[string]string{}
	for k := range known {
		values[k] = "gometer"
	}
	sample, err := sp.URLTemplate.Expand(values)
	if err != nil {
		return sp, err
	}
	if _, err := ResolveStepURL(base, sample); err != nil {
		return sp, err
	}
	if s.Pause != "" {
		sp.PauseDuration, err = time.ParseDuration(s.Pause)
		if err != nil || sp.PauseDuration < 0 || sp.PauseDuration > p.JourneyLimit {
			return sp, fmt.Errorf("pause must be nonnegative and no longer than journey_timeout")
		}
	}
	if len(s.Extract) > 100 || len(s.Assertions) > 100 {
		return sp, fmt.Errorf("at most 100 extractions and 100 assertions are supported per step")
	}
	for name, e := range s.Extract {
		if !variableAllowed(name) || (e.JSONPointer == nil) == (e.Header == "") {
			return sp, fmt.Errorf("extract requires a variable name and exactly one of json_pointer or header")
		}
		ce := CompiledExtraction{Name: name, Header: http.CanonicalHeaderKey(e.Header)}
		if e.JSONPointer != nil {
			ce.Pointer, err = correlation.ParsePointer(*e.JSONPointer)
			sp.CaptureBody = true
		} else if !isToken(e.Header) {
			return sp, fmt.Errorf("invalid extraction header")
		}
		if err != nil {
			return sp, err
		}
		sp.Extractions = append(sp.Extractions, ce)
		known[name] = true
	}
	for _, a := range s.Assertions {
		selectors := 0
		if a.JSONPointer != nil {
			selectors++
		}
		if a.Header != "" {
			selectors++
		}
		if a.BodyContains != nil {
			selectors++
		}
		if selectors != 1 || (a.BodyContains == nil && a.Equals == nil) || (a.BodyContains != nil && a.Equals != nil) {
			return sp, fmt.Errorf("assertion requires one selector, plus equals for a JSON or header selector")
		}
		ca := CompiledAssertion{Header: http.CanonicalHeaderKey(a.Header), IsJSON: a.JSONPointer != nil, IsBody: a.BodyContains != nil}
		if a.JSONPointer != nil {
			ca.Pointer, err = correlation.ParsePointer(*a.JSONPointer)
			if err != nil {
				return sp, err
			}
			sp.CaptureBody = true
		}
		if a.Header != "" && !isToken(a.Header) {
			return sp, fmt.Errorf("invalid assertion header")
		}
		expected := a.Equals
		if a.BodyContains != nil {
			expected = a.BodyContains
			sp.CaptureBody = true
		}
		ca.Expected, err = compile(*expected)
		if err != nil {
			return sp, err
		}
		sp.Checks = append(sp.Checks, ca)
	}
	return sp, nil
}

func variableAllowed(k string) bool {
	return correlation.NameValid(k) && k != "user_id" && k != "iteration" && k != "journey_id"
}

func ResolveStepURL(base *url.URL, value string) (string, error) {
	rel, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid step URL")
	}
	u, err := ValidateURL(base.ResolveReference(rel).String())
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Hostname(), base.Hostname()) || originPort(u) != originPort(base) {
		return "", fmt.Errorf("scenario step URL must remain on the base URL origin")
	}
	return u.String(), nil
}

func originPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func compileJourneyThresholds(t JourneyThresholds) (CompiledJourneyThresholds, error) {
	c := CompiledJourneyThresholds{JourneyThresholds: t}
	var err error
	if t.P95 != "" {
		c.P95Limit, err = positiveDuration("p95", t.P95)
		if err != nil {
			return c, err
		}
	}
	for _, v := range []*float64{t.MaxErrorPercent, t.MaxDropPercent} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100) {
			return c, fmt.Errorf("percentage thresholds must be between 0 and 100")
		}
	}
	if v := t.MinStartedPerSecond; v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
		return c, fmt.Errorf("minimum journey throughput must be a finite nonnegative number")
	}
	return c, nil
}

func (p Plan) ScenarioAt(id int64) int {
	position := id % p.TotalWeight
	for i, s := range p.ScenarioPlans {
		if position < s.WeightStart+s.Weight {
			return i
		}
	}
	return 0
}

// ScenarioRange attributes arbitrarily large skipped ranges without looping
// through every missed arrival. This preserves exact weighted-mix accounting.
func (p Plan) ScenarioRange(start, n int64) []int64 {
	counts := make([]int64, len(p.ScenarioPlans))
	if n <= 0 {
		return counts
	}
	cycles, rest := n/p.TotalWeight, n%p.TotalWeight
	position := start % p.TotalWeight
	for i, s := range p.ScenarioPlans {
		counts[i] = cycles * s.Weight
		for _, interval := range [][2]int64{{position, min(position+rest, p.TotalWeight)}, {0, max(int64(0), position+rest-p.TotalWeight)}} {
			counts[i] += max(int64(0), min(interval[1], s.WeightStart+s.Weight)-max(interval[0], s.WeightStart))
		}
	}
	return counts
}

// ScenarioStride counts interleaved worker assignments in at most TotalWeight
// iterations, even for billions of missed slots.
func (p Plan) ScenarioStride(start, n, stride int64) []int64 {
	counts := make([]int64, len(p.ScenarioPlans))
	if n <= 0 {
		return counts
	}
	a, b := p.TotalWeight, stride
	for b != 0 {
		a, b = b, a%b
	}
	period := p.TotalWeight / a
	cycles, rest := n/period, n%period
	for i := int64(0); i < min(n, period); i++ {
		count := cycles
		if i < rest {
			count++
		}
		counts[p.ScenarioAt(start+i*stride)] += count
	}
	return counts
}
