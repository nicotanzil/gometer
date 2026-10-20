// Package engine executes HTTP workloads without unbounded queues or samples.
package engine

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gometer/internal/config"
	"gometer/internal/diagnostics"
	"gometer/internal/metrics"
	"gometer/internal/telemetry"
)

type Check struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Actual float64 `json:"actual"`
	Limit  float64 `json:"limit"`
	Unit   string  `json:"unit"`
}

type PhaseResult struct {
	Name           string         `json:"name"`
	TargetRPS      int            `json:"target_rps"`
	PlannedSeconds float64        `json:"planned_seconds"`
	LoadSeconds    float64        `json:"load_seconds"`
	TargetRate     int            `json:"target_rate"`
	Journeys       JourneyMetrics `json:"journeys"`
	metrics.Snapshot
}

type Result struct {
	SchemaVersion       int                  `json:"schema_version"`
	Name                string               `json:"name"`
	URL                 string               `json:"url"`
	Method              string               `json:"method"`
	Insecure            bool                 `json:"insecure,omitempty"`
	Mode                string               `json:"mode"`
	ConcurrencyLimit    int                  `json:"concurrency_limit"`
	Iterations          int                  `json:"iterations,omitempty"`
	StopReason          string               `json:"stop_reason,omitempty"`
	UserSchedule        *UserSchedule        `json:"user_schedule,omitempty"`
	Datasets            []DatasetResult      `json:"datasets,omitempty"`
	FailurePolicy       *FailurePolicy       `json:"failure_policy,omitempty"`
	StartedAt           time.Time            `json:"started_at"`
	PlannedLoadSeconds  float64              `json:"planned_load_seconds"`
	LoadSeconds         float64              `json:"load_seconds"`
	TotalSeconds        float64              `json:"total_seconds"`
	Interrupted         bool                 `json:"interrupted"`
	Passed              bool                 `json:"passed"`
	Workload            string               `json:"workload"`
	ArrivalDistribution string               `json:"arrival_distribution"`
	Seed                int64                `json:"seed"`
	Journeys            JourneyMetrics       `json:"journeys"`
	Scenarios           []ScenarioResult     `json:"scenarios,omitempty"`
	Generator           *diagnostics.Summary `json:"generator,omitempty"`
	Assignment          *Assignment          `json:"assignment,omitempty"`
	Distribution        *Distribution        `json:"distribution,omitempty"`
	Telemetry           *telemetry.Summary   `json:"telemetry,omitempty"`
	Merge               *AggregateData       `json:"-"`
	metrics.Snapshot
	Phases []PhaseResult `json:"phases"`
	Checks []Check       `json:"checks"`
}

type Progress struct {
	Elapsed       time.Duration
	WallElapsed   time.Duration
	PhaseIndex    int
	PhaseName     string
	TargetRate    int
	Draining      bool
	UserSchedule  *UserSchedule
	Datasets      []DatasetResult
	FailurePolicy *FailurePolicy
	Journeys      JourneyMetrics
	Workload      string
	Generator     *diagnostics.Resources
	Merge         *LiveData
	Workers       []WorkerStatus
	Telemetry     *telemetry.Snapshot
	metrics.Snapshot
}

type Options struct {
	OnProgress func(Progress)
	Shard      *Assignment
	Capture    bool
	Telemetry  telemetry.Options
}

type runner struct {
	plan          config.Plan
	client        *http.Client
	total         *metrics.Collector
	phases        []*metrics.Collector
	wg            sync.WaitGroup
	totalJourneys *metrics.Collector
	phaseJourneys []*metrics.Collector
	scenarioStats []*scenarioStats
	nextID        atomic.Int64
	shard         *Assignment
	capacity      int
	usersStarted  atomic.Int64
	usersFinished atomic.Int64
	usersStopped  atomic.Int64
	datasetStats  []datasetStats
	testStop      *testStop
}

func Run(ctx context.Context, c config.Config, opts Options) (Result, error) {
	p, err := c.Validate()
	if err != nil {
		return Result{}, err
	}
	if len(c.Workers) > 0 {
		return Result{}, fmt.Errorf("worker configurations must execute through the distributed coordinator")
	}
	if opts.Shard != nil {
		if err := opts.Shard.Validate(p); err != nil {
			return Result{}, err
		}
	}
	var targetSampler *telemetry.Sampler
	if p.TargetTelemetry != nil {
		if opts.Shard != nil {
			return Result{}, fmt.Errorf("target telemetry must run on the coordinator")
		}
		targetSampler, err = telemetry.New(p.TargetTelemetry.Config, opts.Telemetry)
		if err != nil {
			return Result{}, err
		}
		defer targetSampler.Close()
	}
	limit := p.MaxInFlight
	if p.Mode == "users" {
		limit = p.VUs
	}
	if opts.Shard != nil {
		limit = opts.Shard.Capacity
	}
	// Each workload owns its transport. This explicit opt-in never changes
	// the independent worker-control or target-telemetry TLS clients.
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		TLSClientConfig:        &tls.Config{InsecureSkipVerify: p.Insecure},
		DialContext:            (&net.Dialer{Timeout: p.RequestTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           limit,
		MaxIdleConnsPerHost:    limit,
		MaxConnsPerHost:        limit,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    p.RequestTimeout,
		MaxResponseHeaderBytes: 1 << 20,
		DisableCompression:     true,
	}
	defer transport.CloseIdleConnections()
	r := &runner{
		plan: p, total: metrics.New(), totalJourneys: metrics.New(), shard: opts.Shard, capacity: limit,
		client: &http.Client{
			Transport: transport, Timeout: p.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for range p.Phases {
		r.phases = append(r.phases, metrics.New())
		r.phaseJourneys = append(r.phaseJourneys, metrics.New())
	}
	r.datasetStats = make([]datasetStats, len(p.ScenarioPlans))
	for _, scenario := range p.ScenarioPlans {
		stats := &scenarioStats{journeys: metrics.New(), skipped: make([]atomic.Int64, len(scenario.Steps)), executed: make([]atomic.Int64, len(scenario.Steps)), skippedCondition: make([]atomic.Int64, len(scenario.Steps)), controls: make([]controlStats, len(scenario.Controls))}
		for range scenario.Steps {
			stats.steps = append(stats.steps, metrics.New())
		}
		r.scenarioStats = append(r.scenarioStats, stats)
	}
	workload := "requests"
	if p.IsJourney() {
		workload = "journeys"
	}
	start := time.Now()
	if opts.Shard != nil {
		// Convert the coordinated wall-clock timestamp to this process's
		// monotonic clock once. Later scheduling uses monotonic durations.
		start = start.Add(opts.Shard.StartAt.Sub(start))
		if !waitUntil(ctx, start) {
			return Result{}, ctx.Err()
		}
	}
	var sampler *diagnostics.Sampler
	if targetSampler != nil {
		targetSampler.Start(ctx, start)
	}
	if p.DiagnosticsEnabled() {
		sampler = diagnostics.New()
	}
	loadStart := start.Add(p.UserStartupDelay)
	var loadCtx context.Context
	var cancelLoad context.CancelFunc
	if p.LoadDuration > 0 {
		loadCtx, cancelLoad = context.WithDeadline(ctx, loadStart.Add(p.LoadDuration))
	} else {
		loadCtx, cancelLoad = context.WithCancel(ctx)
	}
	defer cancelLoad()
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	if p.HasTestStopPolicy() {
		r.testStop = &testStop{start: start, action: p.ErrorAction(), closeAdmission: cancelLoad, cancelWork: cancelWork}
	}
	var draining atomic.Bool
	stopProgress := make(chan struct{})
	progressDone := make(chan struct{})
	if opts.OnProgress != nil || sampler != nil {
		go func() {
			defer close(progressDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopProgress:
					return
				case <-ticker.C:
					wallElapsed := time.Since(start)
					elapsed := r.loadElapsed(wallElapsed)
					phaseIndex := progressPhase(p.Phases, elapsed)
					var resources *diagnostics.Resources
					if sampler != nil {
						value := sampler.Sample()
						resources = &value
					}
					if opts.OnProgress != nil {
						progress := Progress{Elapsed: elapsed, WallElapsed: wallElapsed, PhaseIndex: phaseIndex, PhaseName: p.Phases[phaseIndex].Name, TargetRate: p.Phases[phaseIndex].Rate, Draining: draining.Load() || (p.LoadDuration > 0 && wallElapsed >= p.UserStartupDelay+p.LoadDuration), UserSchedule: r.userSchedule(wallElapsed), FailurePolicy: r.failurePolicy(), Datasets: r.datasets(), Snapshot: r.total.Snapshot(elapsed), Journeys: journeyMetrics(r.totalJourneys.Snapshot(elapsed)), Workload: workload, Generator: resources}
						if targetSampler != nil {
							progress.Telemetry = targetSampler.Live()
						}
						if opts.Capture {
							progress.Merge = &LiveData{Requests: r.total.State(), Journeys: r.totalJourneys.State()}
						}
						opts.OnProgress(progress)
					}
				}
			}
		}()
	} else {
		close(progressDone)
	}
	stopReason := "duration"
	if p.Mode == "users" {
		stopReason = r.runUsers(workCtx, loadCtx, start)
	} else {
		r.runRate(ctx, loadCtx, start)
	}
	loadEnd := time.Now()
	draining.Store(true)
	loadElapsed := r.loadElapsed(loadEnd.Sub(start))
	// Planned completion stops new arrivals but lets dispatched requests drain.
	// Parent cancellation (e.g. Ctrl+C) also aborts requests in progress.
	r.wg.Wait()
	if r.testStop.elapsed() > 0 {
		stopReason = "test_stopped"
		if p.ErrorAction() == config.StopTestNow {
			stopReason = "test_stopped_now"
		}
	}
	close(stopProgress)
	<-progressDone
	var targetTelemetry *telemetry.Summary
	if targetSampler != nil {
		value := targetSampler.Finish()
		targetTelemetry = &value
	}
	var generator *diagnostics.Summary
	if sampler != nil {
		value := sampler.Finish()
		generator = &value
	}
	result := Result{
		SchemaVersion: 3, Name: p.Name, URL: p.URL, Method: p.Method, Generator: generator, Assignment: opts.Shard,
		Mode: p.Mode, Insecure: p.Insecure, ConcurrencyLimit: limit, StartedAt: start.UTC(),
		Iterations:         p.Iterations,
		UserSchedule:       r.userSchedule(loadEnd.Sub(start)),
		FailurePolicy:      r.failurePolicy(),
		Datasets:           r.datasets(),
		PlannedLoadSeconds: p.LoadDuration.Seconds(), LoadSeconds: loadElapsed.Seconds(),
		TotalSeconds: time.Since(start).Seconds(), Interrupted: ctx.Err() != nil,
		Snapshot: r.total.Snapshot(loadElapsed), Phases: make([]PhaseResult, 0, len(p.Phases)),
		Workload: workload, ArrivalDistribution: p.ArrivalDistribution, Seed: p.Seed, Journeys: journeyMetrics(r.totalJourneys.Snapshot(loadElapsed)),
		Telemetry: targetTelemetry,
	}
	if targetTelemetry != nil {
		result.SchemaVersion = 5
	}
	if p.HasControlFlow() {
		result.SchemaVersion = 6
	}
	if p.Iterations > 0 || p.HasUserSchedule() || p.HasFailurePolicy() || p.HasCSV() {
		result.SchemaVersion = 7
		if p.HasUserSchedule() {
			result.SchemaVersion = 8
		}
		if p.HasFailurePolicy() {
			result.SchemaVersion = 9
		}
		if p.HasTestStopPolicy() {
			result.SchemaVersion = 10
		}
		if p.HasCSV() {
			result.SchemaVersion = 11
		}
		result.StopReason = stopReason
		if result.Interrupted {
			result.StopReason = "interrupted"
		}
	}
	if result.UserSchedule != nil {
		result.UserSchedule.Waiting = false
	}
	var offset time.Duration
	for i, phase := range p.Phases {
		elapsed := loadElapsed - offset
		if elapsed < 0 {
			elapsed = 0
		}
		if phase.Duration > 0 && elapsed > phase.Duration {
			elapsed = phase.Duration
		}
		targetRPS := phase.Rate
		if p.IsJourney() {
			targetRPS = 0
		}
		result.Phases = append(result.Phases, PhaseResult{
			Name: phase.Name, TargetRPS: targetRPS, TargetRate: phase.Rate, PlannedSeconds: phase.Duration.Seconds(),
			LoadSeconds: elapsed.Seconds(), Snapshot: r.phases[i].Snapshot(elapsed),
			Journeys: journeyMetrics(r.phaseJourneys[i].Snapshot(elapsed)),
		})
		offset += phase.Duration
	}
	result.evaluate(p)
	if p.IsJourney() {
		result.evaluateJourneys(p, r.scenarioStats, loadElapsed)
	}
	if opts.Capture {
		result.Merge = r.aggregateData()
	}
	return result, nil
}

// At a boundary, live progress names the newly active admission phase. During
// drain it stays on the final phase; totals remain across the whole run.
func progressPhase(phases []config.Phase, elapsed time.Duration) int {
	var end time.Duration
	for i, phase := range phases {
		end += phase.Duration
		if elapsed < end {
			return i
		}
	}
	return len(phases) - 1
}

func (r *runner) runUsers(ctx, loadCtx context.Context, start time.Time) string {
	var users sync.WaitGroup
	deadline, timed := loadCtx.Deadline()
	for user := range r.plan.VUs {
		due := start.Add(r.plan.UserStartOffset(user))
		if !waitUntil(loadCtx, due) || (timed && !time.Now().Before(deadline)) {
			return "duration"
		}
		r.wg.Add(1)
		users.Add(1)
		go func() {
			defer r.wg.Done()
			defer users.Done()
			if loadCtx.Err() != nil || (timed && !time.Now().Before(deadline)) {
				return
			}
			r.usersStarted.Add(1)
			defer r.usersFinished.Add(1)
			var session *http.Client
			if r.plan.IsJourney() {
				session = r.newSession()
			}
			cursors := make([]int64, len(r.plan.ScenarioPlans))
			iteration := int64(0)
			for loadCtx.Err() == nil && (!timed || time.Now().Before(deadline)) && (r.plan.Iterations == 0 || iteration < int64(r.plan.Iterations)) {
				iteration++
				scheduled := time.Now()
				if iteration == 1 {
					scheduled = due
				}
				var failure string
				if r.plan.IsJourney() {
					id := r.nextID.Add(1) - 1
					selected := r.plan.ScenarioAt(id)
					row, ok := r.datasetRow(id, selected, cursors)
					if !ok {
						return
					}
					r.startJourney(0, selected)
					if row != nil {
						r.datasetStats[selected].assigned.Add(1)
					}
					failure = r.journey(ctx, scheduled, 0, selected, id, int64(user+1), iteration, session, row)
				} else {
					r.start(0)
					failure = r.request(ctx, scheduled, 0)
				}
				if failure != "" && ctx.Err() == nil && r.plan.ErrorAction() == config.StopUser {
					r.usersStopped.Add(1)
					return
				}
				if failure != "" && ctx.Err() == nil && r.testStop != nil {
					r.testStop.trigger()
					return
				}
				// A pause separates iterations; it must not delay the final completion.
				if r.plan.Iterations > 0 && iteration == int64(r.plan.Iterations) {
					return
				}
				if r.plan.UserThinkTime > 0 && !waitUntil(loadCtx, time.Now().Add(r.plan.UserThinkTime)) {
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { users.Wait(); close(done) }()
	select {
	case <-loadCtx.Done():
		return "duration"
	case <-done:
		if loadCtx.Err() != nil || (timed && !time.Now().Before(deadline)) {
			return "duration"
		}
		if r.usersStopped.Load() > 0 {
			return "users_stopped"
		}
		for _, d := range r.datasets() {
			if d.UsersExhausted > 0 {
				return "data_exhausted"
			}
		}
		return "iterations"
	}
}

func slots(d time.Duration, rate int) int64 {
	return int64(math.Ceil(float64(d) * float64(rate) / float64(time.Second)))
}

func arrival(start time.Time, index int64, rate int) time.Time {
	return start.Add(time.Duration(float64(index) * float64(time.Second) / float64(rate)))
}

// Rate workloads use absolute arrival times. When behind, skip old slots and
// record the missed demand instead of issuing a catch-up burst. Never queue a
// request waiting for capacity: that would silently reshape the workload.
func (r *runner) runRate(ctx, loadCtx context.Context, start time.Time) {
	limit := r.capacity
	if limit == 0 {
		limit = r.plan.MaxInFlight
	}
	capacity := make(chan struct{}, limit)
	phaseStart := start
	var baseID int64
	for phaseIndex, phase := range r.plan.Phases {
		end := phaseStart.Add(phase.Duration)
		n := slots(phase.Duration, phase.Rate)
		stride := int64(1)
		if r.shard != nil {
			stride = int64(r.shard.Count)
		}
		for i := r.firstSlot(baseID); i < n; {
			if ctx.Err() != nil {
				return
			}
			now := time.Now()
			if !now.Before(end) {
				r.dropSlots(phaseIndex, i, n, true, baseID)
				break
			}
			due := r.due(phaseStart, phase.Duration, i, phase.Rate, baseID+i)
			if now.Before(due) {
				if !waitUntil(loadCtx, due) {
					if ctx.Err() != nil {
						return
					}
					r.dropSlots(phaseIndex, i, n, true, baseID)
					break
				}
				now = time.Now()
			}
			if !now.Before(end) {
				r.dropSlots(phaseIndex, i, n, true, baseID)
				break
			}
			latest := int64(math.Floor(float64(now.Sub(phaseStart)) * float64(phase.Rate) / float64(time.Second)))
			if latest >= n {
				latest = n - 1
			}
			if latest > i && r.due(phaseStart, phase.Duration, latest, phase.Rate, baseID+latest).After(now) {
				latest--
			}
			if latest > i {
				r.dropSlots(phaseIndex, i, latest, true, baseID)
				i += ((latest - i + stride - 1) / stride) * stride
				if i >= n {
					break
				}
				due = r.due(phaseStart, phase.Duration, i, phase.Rate, baseID+i)
				if due.After(now) {
					continue
				}
			}
			if ctx.Err() != nil {
				return
			}
			select {
			case capacity <- struct{}{}:
				selected := 0
				if r.plan.IsJourney() {
					selected = r.plan.ScenarioAt(baseID + i)
					r.startJourney(phaseIndex, selected)
				} else {
					r.start(phaseIndex)
				}
				r.wg.Add(1)
				go func(phase int, scheduled time.Time, scenario int, id int64) {
					defer r.wg.Done()
					defer func() { <-capacity }()
					if r.plan.IsJourney() {
						r.journey(ctx, scheduled, phase, scenario, id, id+1, 1, r.newSession(), nil)
					} else {
						r.request(ctx, scheduled, phase)
					}
				}(phaseIndex, due, selected, baseID+i)
			default:
				r.dropRange(phaseIndex, 1, false, baseID+i)
			}
			i += stride
		}
		if !waitUntil(loadCtx, end) && ctx.Err() != nil {
			return
		}
		phaseStart = end
		baseID += n
	}
}

func (r *runner) start(phase int) {
	r.total.Start()
	r.phases[phase].Start()
	if r.totalJourneys != nil {
		r.totalJourneys.Start()
		r.phaseJourneys[phase].Start()
	}
}
func (r *runner) dropRange(phase int, n int64, scheduler bool, id int64) {
	if !r.plan.IsJourney() {
		r.total.Drop(n, scheduler)
		r.phases[phase].Drop(n, scheduler)
	}
	if r.totalJourneys != nil {
		r.totalJourneys.Drop(n, scheduler)
		r.phaseJourneys[phase].Drop(n, scheduler)
	}
	if r.plan.IsJourney() {
		for i, count := range r.plan.ScenarioRange(id, n) {
			r.scenarioStats[i].journeys.Drop(count, scheduler)
		}
	}
}

func (r *runner) request(ctx context.Context, scheduled time.Time, phase int) string {
	start := time.Now()
	s := metrics.Sample{StartDelay: start.Sub(scheduled)}
	req, err := http.NewRequestWithContext(ctx, r.plan.Method, r.plan.URL, strings.NewReader(r.plan.Body))
	var trace *requestTrace
	if err == nil {
		req.Header.Set("User-Agent", userAgent)
		for k, v := range r.plan.Headers {
			if strings.EqualFold(k, "Host") {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		var resp *http.Response
		if r.plan.DiagnosticsEnabled() {
			req, trace = traceRequest(req, start, r.total, r.phases[phase])
		}
		resp, err = r.client.Do(req)
		if resp != nil {
			trace.headers(resp.Proto)
			s.Status = resp.StatusCode
			// Bodies are streamed into io.Discard. The +1 detects oversized
			// responses without buffering them or reading indefinitely.
			s.Bytes, err = io.Copy(io.Discard, io.LimitReader(resp.Body, r.plan.MaxResponseBytes+1))
			resp.Body.Close()
			if s.Bytes > r.plan.MaxResponseBytes {
				s.Error = "response_too_large"
			}
		}
	}
	if s.Error == "" && err != nil {
		var netErr net.Error
		switch {
		case errors.Is(err, context.Canceled):
			s.Error = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			s.Error = "timeout"
		case errors.As(err, &netErr) && netErr.Timeout():
			s.Error = "timeout"
		default:
			s.Error = "transport"
		}
	}
	if s.Error == "" && !r.plan.Successful(s.Status) {
		s.Error = "http_status"
	}
	finish := time.Now()
	trace.finish(finish, err != nil || s.Error == "response_too_large")
	s.Latency, s.ScheduledLatency = finish.Sub(start), finish.Sub(scheduled)
	r.total.Complete(s)
	r.phases[phase].Complete(s)
	if r.totalJourneys != nil {
		s.Status = 0
		s.Bytes = 0
		r.totalJourneys.Complete(s)
		r.phaseJourneys[phase].Complete(s)
	}
	return s.Error
}

func waitUntil(ctx context.Context, at time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	d := time.Until(at)
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func (r *Result) evaluate(p config.Plan) {
	completed := !r.Interrupted && (r.Distribution == nil || r.Distribution.Complete) && (r.FailurePolicy == nil || !r.FailurePolicy.TestStopped)
	r.Checks = []Check{
		{Name: "run_completed", Passed: completed, Actual: boolNumber(completed), Limit: 1, Unit: "boolean"},
		{Name: "requests_completed", Passed: r.Completed > 0, Actual: float64(r.Completed), Limit: 1, Unit: "requests"},
	}
	if r.FailurePolicy != nil && r.FailurePolicy.Action == config.StopUser {
		r.Checks = append(r.Checks, Check{Name: "users_stopped_on_error", Passed: r.FailurePolicy.UsersStopped == 0, Actual: float64(r.FailurePolicy.UsersStopped), Limit: 0, Unit: "users"})
	}
	if p.HasTestStopPolicy() {
		r.Checks = append(r.Checks, Check{Name: "test_stopped_on_error", Passed: !r.FailurePolicy.TestStopped, Actual: boolNumber(r.FailurePolicy.TestStopped), Limit: 0, Unit: "boolean"})
	}
	if p.P95Limit > 0 {
		r.Checks = append(r.Checks, Check{Name: "p95", Passed: r.Completed > 0 && r.Latency.P95MS <= float64(p.P95Limit)/1e6, Actual: r.Latency.P95MS, Limit: float64(p.P95Limit) / 1e6, Unit: "ms"})
	}
	for _, check := range []struct {
		name   string
		actual float64
		limit  *float64
		unit   string
	}{
		{"max_error_percent", r.ErrorPercent, p.Thresholds.MaxErrorPercent, "%"},
		{"max_drop_percent", r.arrivalDropPercent(), p.Thresholds.MaxDropPercent, "%"},
	} {
		if check.limit != nil {
			r.Checks = append(r.Checks, Check{Name: check.name, Passed: check.actual <= *check.limit, Actual: check.actual, Limit: *check.limit, Unit: check.unit})
		}
	}
	if v := p.Thresholds.MinStartedRPS; v != nil {
		r.Checks = append(r.Checks, Check{Name: "min_started_rps", Passed: r.StartedRPS >= *v, Actual: r.StartedRPS, Limit: *v, Unit: "req/s"})
	}
	r.Passed = true
	for _, check := range r.Checks {
		r.Passed = r.Passed && check.Passed
	}
}

func boolNumber(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func (r Result) Summary() string {
	status := "PASS"
	if !r.Passed {
		status = "FAIL"
	}
	summary := fmt.Sprintf("%s  %s\n%d completed requests · %.1f started req/s · %.2f%% request errors · %.2f%% arrivals dropped\nRequest latency: p50 %.2f ms · p95 %.2f ms · p99 %.2f ms\n", status, r.Name, r.Completed, r.StartedRPS, r.ErrorPercent, r.arrivalDropPercent(), r.Latency.P50MS, r.Latency.P95MS, r.Latency.P99MS)
	if r.Iterations > 0 {
		summary += fmt.Sprintf("%d users × %d iterations/user · stopped: %s · %.3fs load\n", r.ConcurrencyLimit, r.Iterations, r.StopReason, r.LoadSeconds)
	}
	if f := r.FailurePolicy; f != nil {
		summary += fmt.Sprintf("Failure action: %s", f.Action)
		if f.Action != config.StopTest && f.Action != config.StopTestNow {
			summary += fmt.Sprintf(" · %d users stopped on error", f.UsersStopped)
		}
		summary += "\n"
		if f.TestStopped {
			summary += "Test stopped on error: " + r.StopReason + "\n"
		}
	}
	if s := r.UserSchedule; s != nil {
		summary += fmt.Sprintf("Startup wait: %.3fs / %.3fs planned · user ramp %.3fs · %d/%d users started · %d finished · stopped: %s\n", s.StartupSeconds, s.PlannedStartupSeconds, s.RampUpSeconds, s.Users.Started, s.Users.Planned, s.Users.Finished, r.StopReason)
	}
	for _, d := range r.Datasets {
		summary += fmt.Sprintf("CSV %s: %d rows assigned · %d users exhausted · stopped: %s\n", d.Scenario, d.RowsAssigned, d.UsersExhausted, r.StopReason)
	}
	if r.Insecure {
		summary += "TLS verification disabled for workload HTTPS requests (insecure).\n"
	}
	if r.Workload == "journeys" {
		summary += fmt.Sprintf("%d completed journeys · %.1f started journeys/s · %.2f%% journey errors · p95 %.2f ms\n", r.Journeys.Completed, r.Journeys.StartedPerSecond, r.Journeys.ErrorPercent, r.Journeys.Latency.P95MS)
		for _, s := range r.Scenarios {
			summary += fmt.Sprintf("  %s: %d journeys · %d requests · %.2f%% journey errors\n", s.Name, s.Journeys.Completed, s.requestCount(), s.Journeys.ErrorPercent)
		}
	}
	if r.HTTP != nil {
		p95 := func(timing metrics.Latency) string {
			if timing.Samples == 0 {
				return "unavailable"
			}
			return fmt.Sprintf("%.2f ms", timing.P95MS)
		}
		summary += fmt.Sprintf("HTTP: %d connection acquisitions · %d reused · first header byte p95 %s · body read p95 %s\n", r.HTTP.ConnectionsAcquired, r.HTTP.ReusedConnections, p95(r.HTTP.FirstByte), p95(r.HTTP.BodyRead))
	}
	if r.Generator != nil {
		cpu := "unavailable"
		if r.Generator.CPUAvailable {
			cpu = fmt.Sprintf("%.1f%% average (100%% = one core)", r.Generator.AverageCPUPercent)
		}
		summary += fmt.Sprintf("Generator: CPU %s · peak sampled Go heap %.2f MiB · peak sampled %d goroutines\n", cpu, float64(r.Generator.PeakHeapBytes)/(1<<20), r.Generator.PeakGoroutines)
	}
	if r.Distribution != nil {
		d := r.Distribution
		summary += fmt.Sprintf("Workers: %d · complete measurements %t · %d planned arrivals · %d planned arrivals unreported\n", len(d.Workers), d.Complete, d.PlannedArrivals, d.UnreportedPlannedArrivals)
		for _, worker := range d.Workers {
			summary += fmt.Sprintf("  %s: %s · %d HTTP attempts · capacity %d · assigned %d arrivals\n", worker.Name, worker.Status, worker.Requests.Completed, worker.Capacity, worker.Planned)
		}
		if d.Failure != "" {
			summary += "  " + d.Failure + "\n"
		}
	}
	if t := r.Telemetry; t != nil {
		summary += fmt.Sprintf("Target telemetry: %d recorded scrapes · %d failed · %d incomplete · %d canceled during stop · complete coverage %t (separate from load checks)\n", t.Attempts, t.FailedScrapes, t.IncompleteScrapes, t.CanceledScrapes, t.Complete)
		for _, s := range t.Series {
			if s.Last == nil {
				summary += fmt.Sprintf("  %s: unavailable · %d missing samples\n", s.Metric.Title, s.Missing)
				continue
			}
			summary += fmt.Sprintf("  %s: last valid %.4g %s at %.2fs · %d samples · %d missing", s.Metric.Title, *s.Last, s.Metric.Unit, s.LastElapsedSeconds, s.Samples, s.Missing)
			if s.MeanRate != nil {
				summary += fmt.Sprintf(" · observed mean rate %.4g %s/s", *s.MeanRate, s.Metric.Unit)
			}
			if s.Metric.Kind == "counter" {
				summary += fmt.Sprintf(" · %d resets", s.Resets)
			}
			summary += "\n"
		}
	}
	return summary
}

func (r Result) arrivalDropPercent() float64 {
	if r.Workload == "journeys" {
		return r.Journeys.DropPercent
	}
	return r.DropPercent
}

func (r Result) Arrivals() JourneyMetrics {
	if r.Workload == "journeys" {
		return r.Journeys
	}
	return journeyMetrics(r.Snapshot)
}

// Jitter keeps one arrival in each rate interval. Hashing the global arrival
// ID makes the schedule repeatable without shared random state or large arrays.
func (r *runner) due(start time.Time, duration time.Duration, index int64, rate int, id int64) time.Time {
	base := arrival(start, index, rate)
	if r.plan.ArrivalDistribution != "jitter" {
		return base
	}
	x := uint64(id) + uint64(r.plan.Seed) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	width := min(start.Add(duration).Sub(base), arrival(start, index+1, rate).Sub(base))
	if width <= 1 {
		return base
	}
	offset := time.Duration(float64(x>>11) / float64(uint64(1)<<53) * float64(width))
	if offset >= width {
		offset = width - 1
	}
	return base.Add(offset)
}
