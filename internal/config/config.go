// Package config validates user input before a load test starts.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"time"

	"gometer/internal/telemetry"
)

const MaxConfigBytes = 1 << 20

type Thresholds struct {
	P95             string   `json:"p95,omitempty"`
	MaxErrorPercent *float64 `json:"max_error_percent"`
	MaxDropPercent  *float64 `json:"max_drop_percent"`
	MinStartedRPS   *float64 `json:"min_started_rps,omitempty"`
}

type Stage struct {
	Name     string `json:"name"`
	Duration string `json:"duration"`
	Rate     int    `json:"rate"`
}

type Config struct {
	Name                string            `json:"name"`
	URL                 string            `json:"url"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers,omitempty"`
	Body                string            `json:"body,omitempty"`
	Mode                string            `json:"mode"`
	Duration            string            `json:"duration"`
	Iterations          int               `json:"iterations,omitempty"`
	RampUp              string            `json:"ramp_up,omitempty"`
	StartupDelay        string            `json:"startup_delay,omitempty"`
	OnError             string            `json:"on_error,omitempty"`
	VUs                 int               `json:"vus"`
	Rate                int               `json:"rate"`
	MaxInFlight         int               `json:"max_in_flight"`
	Timeout             string            `json:"timeout"`
	Insecure            bool              `json:"insecure,omitempty"`
	ThinkTime           string            `json:"think_time,omitempty"`
	MaxResponseBytes    int64             `json:"max_response_bytes"`
	SuccessStatuses     []int             `json:"success_statuses,omitempty"`
	Stages              []Stage           `json:"stages,omitempty"`
	Thresholds          Thresholds        `json:"thresholds,omitempty"`
	Variables           map[string]string `json:"variables,omitempty"`
	Cookies             *bool             `json:"cookies,omitempty"`
	Scenarios           []Scenario        `json:"scenarios,omitempty"`
	JourneyTimeout      string            `json:"journey_timeout"`
	JourneyThresholds   JourneyThresholds `json:"journey_thresholds,omitempty"`
	ArrivalDistribution string            `json:"arrival_distribution"`
	Seed                int64             `json:"seed"`
	Diagnostics         *bool             `json:"diagnostics,omitempty"`
	Workers             []string          `json:"workers,omitempty"`
	Telemetry           *telemetry.Config `json:"telemetry,omitempty"`
}

type Phase struct {
	Name     string
	Duration time.Duration
	Rate     int
}

type Plan struct {
	Config
	Phases           []Phase
	LoadDuration     time.Duration
	RequestTimeout   time.Duration
	UserThinkTime    time.Duration
	UserRampUp       time.Duration
	UserStartupDelay time.Duration
	P95Limit         time.Duration
	JourneyLimit     time.Duration
	JourneyChecks    CompiledJourneyThresholds
	ScenarioPlans    []ScenarioPlan
	TotalWeight      int64
	TargetTelemetry  *telemetry.Plan
}

func Defaults() Config {
	zeroError, zeroDrop := 0.0, 0.0
	zeroJourneyError := 0.0
	diagnostics := true
	return Config{
		Name: "HTTP test", Method: "GET", Mode: "users", Duration: "10s",
		VUs: 10, MaxInFlight: 100, Timeout: "5s", MaxResponseBytes: 10 << 20,
		Thresholds:     Thresholds{MaxErrorPercent: &zeroError, MaxDropPercent: &zeroDrop},
		JourneyTimeout: "30s", JourneyThresholds: JourneyThresholds{MaxErrorPercent: &zeroJourneyError},
		ArrivalDistribution: "constant", Seed: 1,
		Diagnostics: &diagnostics,
	}
}

func (c Config) DiagnosticsEnabled() bool { return c.Diagnostics == nil || *c.Diagnostics }

func (p Plan) HasUserSchedule() bool { return p.UserRampUp > 0 || p.UserStartupDelay > 0 }

const (
	NextIteration   = "next_iteration"
	ContinueJourney = "continue"
	StopUser        = "stop_user"
	StopTest        = "stop_test"
	StopTestNow     = "stop_test_now"
)

func (c Config) ErrorAction() string {
	if c.OnError == "" {
		return NextIteration
	}
	return c.OnError
}

func (c Config) HasFailurePolicy() bool { return c.ErrorAction() != NextIteration }

func (c Config) HasTestStopPolicy() bool {
	return c.ErrorAction() == StopTest || c.ErrorAction() == StopTestNow
}

// UserStartOffset follows the standard thread-group ramp convention: the
// first user starts immediately and the last at ramp * (users-1) / users.
func (p Plan) UserStartOffset(user int) time.Duration {
	return p.UserStartupDelay + p.UserRampUp*time.Duration(user)/time.Duration(p.VUs)
}

func Load(r io.Reader) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxConfigBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("configuration exceeds 1 MiB")
	}
	c := Defaults()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("configuration must contain exactly one JSON object")
	}
	if strings.TrimSpace(string(data)) == "null" {
		return Config{}, fmt.Errorf("configuration must be a JSON object")
	}
	// Count-only configurations must not inherit the duration default.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, err
	}
	if _, present := fields["duration"]; !present && c.Iterations > 0 {
		c.Duration = ""
	}
	return c, nil
}

func (c Config) Validate() (Plan, error) {
	for _, s := range c.Scenarios {
		if s.CSV != nil {
			source, err := json.Marshal(c)
			if err != nil || len(source) > MaxConfigBytes {
				return Plan{}, fmt.Errorf("configuration including CSV data exceeds 1 MiB")
			}
			break
		}
	}

	p := Plan{Config: c}
	if err := c.validateWorkers(); err != nil {
		return p, err
	}
	if c.Telemetry != nil {
		compiled, err := c.Telemetry.Compile()
		if err != nil {
			return p, err
		}
		p.TargetTelemetry = &compiled
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return p, fmt.Errorf("url must be an absolute http:// or https:// URL")
	}
	if u.User != nil || u.Fragment != "" {
		return p, fmt.Errorf("url must not contain credentials or a fragment; use request headers for authentication")
	}
	if port := u.Port(); port != "" {
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 {
			return p, fmt.Errorf("url port must be between 1 and 65535")
		}
	}
	p.Method = strings.ToUpper(c.Method)
	if !isToken(p.Method) {
		return p, fmt.Errorf("method must be a valid HTTP method token")
	}
	for k, v := range c.Headers {
		if !isToken(k) || !isHeaderValue(v) {
			return p, fmt.Errorf("invalid HTTP header %q", k)
		}
		// net/http owns framing; accepting these headers would be misleading.
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			return p, fmt.Errorf("header %q is generated by the HTTP engine", k)
		}
	}
	if len(c.Body) > MaxConfigBytes {
		return p, fmt.Errorf("request body exceeds 1 MiB")
	}
	if c.Mode != "users" && c.Mode != "rate" {
		return p, fmt.Errorf("mode must be users or rate")
	}
	switch c.ErrorAction() {
	case NextIteration, ContinueJourney, StopUser, StopTest, StopTestNow:
	default:
		return p, fmt.Errorf("on_error must be next_iteration, continue, stop_user, stop_test, or stop_test_now")
	}
	if c.HasFailurePolicy() && c.Mode != "users" {
		return p, fmt.Errorf("nondefault failure actions currently require users mode")
	}
	if c.Iterations < 0 || c.Iterations > 1000000 {
		return p, fmt.Errorf("iterations must be between 0 and 1000000 (0 means duration only)")
	}
	if c.Iterations > 0 && c.Mode != "users" {
		return p, fmt.Errorf("iterations is only supported in users mode")
	}
	for _, setting := range []struct {
		name, value string
		target      *time.Duration
	}{
		{"ramp_up", c.RampUp, &p.UserRampUp},
		{"startup_delay", c.StartupDelay, &p.UserStartupDelay},
	} {
		if setting.value == "" {
			continue
		}
		value, err := time.ParseDuration(setting.value)
		if err != nil || value < 0 || value > 24*time.Hour {
			return p, fmt.Errorf("%s must be a duration between 0 and 24h", setting.name)
		}
		if c.Mode != "users" && value != 0 {
			return p, fmt.Errorf("%s is only supported in users mode", setting.name)
		}
		*setting.target = value
	}
	if c.VUs < 1 || c.VUs > 100000 {
		return p, fmt.Errorf("vus must be between 1 and 100000")
	}
	if c.MaxInFlight < 1 || c.MaxInFlight > 100000 {
		return p, fmt.Errorf("max_in_flight must be between 1 and 100000")
	}
	if c.MaxResponseBytes < 1 || c.MaxResponseBytes > 1<<40 {
		return p, fmt.Errorf("max_response_bytes must be between 1 and 1099511627776")
	}
	p.RequestTimeout, err = positiveDuration("timeout", c.Timeout)
	if err != nil {
		return p, err
	}
	if c.ThinkTime != "" {
		p.UserThinkTime, err = time.ParseDuration(c.ThinkTime)
		if err != nil || p.UserThinkTime < 0 || p.UserThinkTime > 24*time.Hour {
			return p, fmt.Errorf("think_time must be a duration between 0 and 24h")
		}
	}
	if c.Mode == "rate" && p.UserThinkTime != 0 {
		return p, fmt.Errorf("think_time is only supported in users mode")
	}
	if len(c.SuccessStatuses) > 500 {
		return p, fmt.Errorf("too many success_statuses")
	}
	for _, status := range c.SuccessStatuses {
		if status < 100 || status > 599 {
			return p, fmt.Errorf("success_statuses must contain HTTP codes between 100 and 599")
		}
	}
	if len(c.Stages) > 100 {
		return p, fmt.Errorf("at most 100 stages are supported")
	}
	if c.Mode == "users" && (c.Rate != 0 || len(c.Stages) != 0) {
		return p, fmt.Errorf("rate and stages require rate mode")
	}
	if len(c.Stages) > 0 {
		if c.Rate != 0 {
			return p, fmt.Errorf("use either rate or stages, not both")
		}
		for i, s := range c.Stages {
			d, err := positiveDuration(fmt.Sprintf("stages[%d].duration", i), s.Duration)
			if err != nil {
				return p, err
			}
			if s.Rate < 0 || s.Rate > 1000000 {
				return p, fmt.Errorf("stage rate must be between 0 and 1000000 requests/second")
			}
			name := s.Name
			if name == "" {
				name = fmt.Sprintf("Phase %d", i+1)
			}
			p.Phases = append(p.Phases, Phase{Name: name, Duration: d, Rate: s.Rate})
			p.LoadDuration += d
		}
	} else {
		var d time.Duration
		if c.Duration != "" || c.Iterations == 0 {
			d, err = positiveDuration("duration", c.Duration)
			if err != nil {
				return p, err
			}
		}
		if c.Mode == "rate" && (c.Rate < 1 || c.Rate > 1000000) {
			return p, fmt.Errorf("rate must be between 1 and 1000000 requests/second")
		}
		p.Phases = []Phase{{Name: c.Name, Duration: d, Rate: c.Rate}}
		p.LoadDuration = d
	}
	if p.LoadDuration > 24*time.Hour {
		return p, fmt.Errorf("total load duration must not exceed 24h")
	}
	if c.Mode == "rate" {
		active := false
		for _, phase := range p.Phases {
			active = active || phase.Rate > 0
		}
		if !active {
			return p, fmt.Errorf("at least one stage must have a positive rate")
		}
	}
	if c.Thresholds.P95 != "" {
		p.P95Limit, err = positiveDuration("thresholds.p95", c.Thresholds.P95)
		if err != nil {
			return p, err
		}
	}
	for name, v := range map[string]*float64{"max_error_percent": c.Thresholds.MaxErrorPercent, "max_drop_percent": c.Thresholds.MaxDropPercent} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100) {
			return p, fmt.Errorf("thresholds.%s must be between 0 and 100", name)
		}
	}
	if v := c.Thresholds.MinStartedRPS; v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
		return p, fmt.Errorf("thresholds.min_started_rps must be a finite nonnegative number")
	}
	if err := p.compileScenarios(); err != nil {
		return p, err
	}
	return p, nil
}

// ValidateURL is also used after expansion: extracted text must not introduce
// unsupported schemes, userinfo, or fragments into a request.
func ValidateURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid HTTP URL")
	}
	if port := u.Port(); port != "" {
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid URL port")
		}
	}
	return u, nil
}

func ValidateHeaders(headers map[string]string) error {
	seen := map[string]bool{}
	for name, value := range headers {
		key := strings.ToLower(name)
		if !isToken(name) || !isHeaderValue(value) || key == "content-length" || key == "transfer-encoding" || seen[key] {
			return fmt.Errorf("invalid or duplicate HTTP header %q", name)
		}
		seen[key] = true
	}
	return nil
}

func positiveDuration(name, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 || d > 24*time.Hour {
		return 0, fmt.Errorf("%s must be a duration greater than 0 and no longer than 24h", name)
	}
	return d, nil
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func isHeaderValue(s string) bool {
	for i := range s {
		if (s[i] < 32 && s[i] != '\t') || s[i] == 127 {
			return false
		}
	}
	return true
}

func (p Plan) Successful(status int) bool {
	if len(p.SuccessStatuses) == 0 {
		return status >= 200 && status < 300
	}
	for _, s := range p.SuccessStatuses {
		if s == status {
			return true
		}
	}
	return false
}
