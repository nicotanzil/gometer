package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gometer/internal/config"
	"gometer/internal/correlation"
	"gometer/internal/metrics"
)

type JourneyMetrics struct {
	Scheduled        int64            `json:"scheduled"`
	Started          int64            `json:"started"`
	Completed        int64            `json:"completed"`
	Succeeded        int64            `json:"succeeded"`
	Failed           int64            `json:"failed"`
	DroppedCapacity  int64            `json:"dropped_capacity"`
	DroppedScheduler int64            `json:"dropped_scheduler"`
	InFlight         int64            `json:"in_flight"`
	PeakInFlight     int64            `json:"peak_in_flight"`
	ErrorPercent     float64          `json:"error_percent"`
	DropPercent      float64          `json:"drop_percent"`
	StartedPerSecond float64          `json:"started_per_second"`
	Latency          metrics.Latency  `json:"latency"`
	ScheduledLatency metrics.Latency  `json:"scheduled_latency"`
	StartDelay       metrics.Latency  `json:"start_delay"`
	Errors           map[string]int64 `json:"errors"`
}

func journeyMetrics(s metrics.Snapshot) JourneyMetrics {
	return JourneyMetrics{Scheduled: s.Scheduled, Started: s.Started, Completed: s.Completed, Succeeded: s.Succeeded, Failed: s.Failed, DroppedCapacity: s.DroppedCapacity, DroppedScheduler: s.DroppedScheduler, InFlight: s.InFlight, PeakInFlight: s.PeakInFlight, ErrorPercent: s.ErrorPercent, DropPercent: s.DropPercent, StartedPerSecond: s.StartedRPS, Latency: s.Latency, ScheduledLatency: s.ScheduledLatency, StartDelay: s.StartDelay, Errors: s.Errors}
}

type StepResult struct {
	Name             string `json:"name"`
	Method           string `json:"method"`
	Skipped          int64  `json:"skipped"`
	Path             string `json:"path,omitempty"`
	ExecutedJourneys int64  `json:"executed_journeys,omitempty"`
	SkippedCondition int64  `json:"skipped_condition,omitempty"`
	metrics.Snapshot
}

type ScenarioResult struct {
	Name     string          `json:"name"`
	Weight   int64           `json:"weight"`
	Passed   bool            `json:"passed"`
	Journeys JourneyMetrics  `json:"journeys"`
	Steps    []StepResult    `json:"steps"`
	Checks   []Check         `json:"checks"`
	Controls []ControlResult `json:"controls,omitempty"`
}

func (s ScenarioResult) requestCount() int64 {
	var n int64
	for _, step := range s.Steps {
		n += step.Completed
	}
	return n
}

type scenarioStats struct {
	journeys         *metrics.Collector
	steps            []*metrics.Collector
	skipped          []atomic.Int64
	executed         []atomic.Int64
	skippedCondition []atomic.Int64
	controls         []controlStats
}

func (r *runner) newSession() *http.Client {
	if r.plan.Cookies != nil && !*r.plan.Cookies {
		return &http.Client{Transport: r.client.Transport, Timeout: r.client.Timeout, CheckRedirect: r.client.CheckRedirect}
	}
	jar, _ := cookiejar.New(nil)
	// All scenario URLs are restricted to one origin. The key cap also bounds a
	// long-lived user's cookie history, even if a target invents new cookie names.
	return &http.Client{Transport: r.client.Transport, Timeout: r.client.Timeout, CheckRedirect: r.client.CheckRedirect, Jar: &boundedJar{jar: jar, keys: map[cookieKey]int{}}}
}

func (r *runner) startJourney(phase, scenario int) {
	r.totalJourneys.Start()
	r.phaseJourneys[phase].Start()
	r.scenarioStats[scenario].journeys.Start()
}

func (r *runner) journey(parent context.Context, scheduled time.Time, phase, selected int, id, user, iteration int64, client *http.Client, row map[string]string) string {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, r.plan.JourneyLimit)
	defer cancel()
	scenario := r.plan.ScenarioPlans[selected]
	stats := r.scenarioStats[selected]
	values := make(map[string]string, len(scenario.Variables)+10)
	for k, v := range scenario.Variables {
		values[k] = v
	}
	if len(scenario.Data) > 0 {
		ordinal := r.plan.ScenarioOrdinal(id, selected)
		for k, v := range scenario.Data[ordinal%int64(len(scenario.Data))] {
			values[k] = v
		}
	}
	for k, v := range row {
		values[k] = v
	}
	values["user_id"], values["iteration"], values["journey_id"] = strconv.FormatInt(user, 10), strconv.FormatInt(iteration, 10), strconv.FormatInt(id+1, 10)
	execution := flowExecution{runner: r, ctx: ctx, client: client, scenario: scenario, stats: stats, values: values, scheduled: scheduled, phase: phase, visited: make([]bool, len(scenario.Steps)), bypassed: make([]bool, len(scenario.Steps))}
	category := execution.sequence(scenario.Flow)
	for i, visited := range execution.visited {
		if visited {
			stats.executed[i].Add(1)
		} else {
			stats.skipped[i].Add(1)
			if execution.bypassed[i] {
				stats.skippedCondition[i].Add(1)
			}
		}
	}
	if category == "" && ctx.Err() != nil {
		category = contextCategory(ctx)
	}
	if category == "" && execution.failed {
		category = "step_failed"
	}
	finish := time.Now()
	sample := metrics.Sample{Error: category, Latency: finish.Sub(start), ScheduledLatency: finish.Sub(scheduled), StartDelay: start.Sub(scheduled)}
	r.totalJourneys.Complete(sample)
	r.phaseJourneys[phase].Complete(sample)
	stats.journeys.Complete(sample)
	return category
}

func contextCategory(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "timeout"
}

// A step is a logical HTTP attempt. Validation of expanded fields can fail
// before any bytes go over the wire, and is visible as a template error.
func (r *runner) step(ctx context.Context, client *http.Client, step config.StepPlan, values map[string]string, scheduled time.Time, phase int, collector *metrics.Collector) string {
	start := time.Now()
	r.total.Start()
	r.phases[phase].Start()
	collector.Start()
	sample := metrics.Sample{StartDelay: start.Sub(scheduled)}
	req, err := r.expandRequest(ctx, step, values)
	if err != nil {
		sample.Error = "template"
	}
	var response *http.Response
	var trace *requestTrace
	var body bytes.Buffer
	jar, _ := client.Jar.(*boundedJar)
	var rejectedBefore int64
	if jar != nil {
		rejectedBefore = jar.Rejections()
	}
	if sample.Error == "" {
		if r.plan.DiagnosticsEnabled() {
			req, trace = traceRequest(req, start, r.total, r.phases[phase], collector)
		}
		response, err = client.Do(req)
		if response != nil {
			trace.headers(response.Proto)
			sample.Status = response.StatusCode
			var destination io.Writer = io.Discard
			if step.CaptureBody {
				destination = &captureWriter{buffer: &body}
			}
			sample.Bytes, err = io.Copy(destination, io.LimitReader(response.Body, r.plan.MaxResponseBytes+1))
			response.Body.Close()
			if sample.Bytes > r.plan.MaxResponseBytes {
				sample.Error = "response_too_large"
			} else if step.CaptureBody && sample.Bytes > correlation.MaxValueBytes {
				sample.Error = "capture_too_large"
			}
		}
		if sample.Error == "" && err != nil {
			var netErr net.Error
			switch {
			case errors.Is(err, context.Canceled):
				sample.Error = "canceled"
			case errors.Is(err, context.DeadlineExceeded):
				sample.Error = "timeout"
			case errors.As(err, &netErr) && netErr.Timeout():
				sample.Error = "timeout"
			default:
				sample.Error = "transport"
			}
		}
	}
	networkEnd := time.Now()
	trace.finish(networkEnd, err != nil || sample.Error == "response_too_large")
	if sample.Error == "" && jar != nil && jar.Rejections() > rejectedBefore {
		sample.Error = "cookie_limit"
	}
	if sample.Error == "" && !(config.Plan{Config: config.Config{SuccessStatuses: step.SuccessStatuses}}).Successful(sample.Status) {
		sample.Error = "http_status"
	}
	if sample.Error == "" {
		sample.Error = verifyResponse(step, response, body.Bytes(), values)
	}
	sample.Latency = networkEnd.Sub(start)
	sample.ScheduledLatency = networkEnd.Sub(scheduled)
	r.total.Complete(sample)
	r.phases[phase].Complete(sample)
	collector.Complete(sample)
	return sample.Error
}

func (r *runner) expandRequest(ctx context.Context, step config.StepPlan, values map[string]string) (*http.Request, error) {
	raw, err := step.URLTemplate.Expand(values)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(r.plan.URL)
	target, err := config.ResolveStepURL(base, raw)
	if err != nil {
		return nil, err
	}
	body, err := step.BodyTemplate.Expand(values)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	var total int
	for k, t := range step.HeaderTemplates {
		v, err := t.Expand(values)
		if err != nil {
			return nil, err
		}
		headers[k] = v
		total += len(k) + len(v)
	}
	if total > correlation.MaxValueBytes {
		return nil, errors.New("expanded headers exceed 1 MiB")
	}
	if err := config.ValidateHeaders(headers); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, step.Method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	return req, nil
}

type captureWriter struct{ buffer *bytes.Buffer }

func (w *captureWriter) Write(p []byte) (int, error) {
	n := len(p)
	left := correlation.MaxValueBytes - w.buffer.Len()
	if left > 0 {
		w.buffer.Write(p[:min(left, n)])
	}
	return n, nil
}

func verifyResponse(step config.StepPlan, response *http.Response, body []byte, values map[string]string) string {
	if len(step.Extractions) == 0 && len(step.Checks) == 0 {
		return ""
	}
	var document any
	var jsonErr error
	needsJSON := false
	for _, e := range step.Extractions {
		if e.Header == "" {
			needsJSON = true
		}
	}
	for _, a := range step.Checks {
		needsJSON = needsJSON || a.IsJSON
	}
	if needsJSON {
		document, jsonErr = correlation.DecodeJSON(body)
	}
	// Commit extracted values only after every extraction and assertion passes.
	current := make(map[string]string, len(values)+len(step.Extractions))
	for k, v := range values {
		current[k] = v
	}
	var total int
	for _, e := range step.Extractions {
		var value string
		var err error
		if e.Header != "" {
			if _, ok := response.Header[e.Header]; !ok {
				return "extraction"
			}
			value = response.Header.Get(e.Header)
		} else {
			if jsonErr != nil {
				return "extraction"
			}
			value, err = e.Pointer.Scalar(document)
		}
		if err != nil || len(value) > correlation.MaxValueBytes {
			return "extraction"
		}
		current[e.Name] = value
	}
	for _, v := range current {
		total += len(v)
	}
	if total > correlation.MaxValueBytes {
		return "extraction"
	}
	for _, a := range step.Checks {
		expected, err := a.Expected.Expand(current)
		if err != nil {
			return "template"
		}
		switch {
		case a.IsBody:
			if !strings.Contains(string(body), expected) {
				return "assertion"
			}
		case a.IsJSON:
			if jsonErr != nil {
				return "assertion"
			}
			actual, err := a.Pointer.Scalar(document)
			if err != nil || actual != expected {
				return "assertion"
			}
		default:
			if _, ok := response.Header[a.Header]; !ok || response.Header.Get(a.Header) != expected {
				return "assertion"
			}
		}
	}
	for k, v := range current {
		values[k] = v
	}
	return ""
}

func journeyChecks(prefix string, s JourneyMetrics, t config.CompiledJourneyThresholds) []Check {
	checks := make([]Check, 0)
	if t.P95Limit > 0 {
		limit := float64(t.P95Limit) / 1e6
		checks = append(checks, Check{Name: prefix + "p95", Passed: s.Completed > 0 && s.Latency.P95MS <= limit, Actual: s.Latency.P95MS, Limit: limit, Unit: "ms"})
	}
	for _, test := range []struct {
		name  string
		value float64
		limit *float64
	}{
		{"max_error_percent", s.ErrorPercent, t.MaxErrorPercent}, {"max_drop_percent", s.DropPercent, t.MaxDropPercent},
	} {
		if test.limit != nil {
			valid := s.Completed > 0
			if test.name == "max_drop_percent" {
				valid = s.Scheduled > 0
			}
			checks = append(checks, Check{Name: prefix + test.name, Passed: valid && test.value <= *test.limit, Actual: test.value, Limit: *test.limit, Unit: "%"})
		}
	}
	if v := t.MinStartedPerSecond; v != nil {
		checks = append(checks, Check{Name: prefix + "min_started_per_second", Passed: s.StartedPerSecond >= *v, Actual: s.StartedPerSecond, Limit: *v, Unit: "journeys/s"})
	}
	return checks
}

func (r *Result) evaluateJourneys(p config.Plan, stats []*scenarioStats, elapsed time.Duration) {
	r.Checks = append(r.Checks, journeyChecks("journeys.", r.Journeys, p.JourneyChecks)...)
	for i, scenario := range p.ScenarioPlans {
		s := ScenarioResult{Name: scenario.Name, Weight: scenario.Weight, Passed: true, Journeys: journeyMetrics(stats[i].journeys.Snapshot(elapsed))}
		for j, step := range scenario.Steps {
			result := StepResult{Name: step.Name, Method: step.Method, Skipped: stats[i].skipped[j].Load(), Snapshot: stats[i].steps[j].Snapshot(elapsed)}
			if p.HasControlFlow() {
				result.Path = step.Path
				result.ExecutedJourneys = stats[i].executed[j].Load()
				result.SkippedCondition = stats[i].skippedCondition[j].Load()
			}
			s.Steps = append(s.Steps, result)
		}
		for j, control := range scenario.Controls {
			s.Controls = append(s.Controls, ControlResult{Name: control.Name, Kind: control.Kind, Path: control.Path, ControlCounts: stats[i].controls[j].snapshot()})
		}
		s.Checks = journeyChecks("", s.Journeys, scenario.Checks)
		for _, check := range s.Checks {
			s.Passed = s.Passed && check.Passed
			check.Name = "scenario[" + s.Name + "]." + check.Name
			r.Checks = append(r.Checks, check)
		}
		r.Scenarios = append(r.Scenarios, s)
	}
	for _, check := range r.Checks {
		r.Passed = r.Passed && check.Passed
	}
}
