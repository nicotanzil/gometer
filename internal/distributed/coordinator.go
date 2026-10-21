package distributed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gometer/internal/config"
	"gometer/internal/engine"
	"gometer/internal/telemetry"
)

type Options struct {
	AllowHTTPWorkers bool
	Token            string
	CAFile           string
	Client           *http.Client
	OnProgress       func(engine.Progress)
	StartDelay       time.Duration
	PollInterval     time.Duration
	Telemetry        telemetry.Options
}
type node struct {
	engine.WorkerStatus
	id         string
	assignment engine.Assignment
	offset     time.Duration
	status     Status
	part       *engine.Result
	err        error
	failures   int
	terminal   bool
}

// A dedicated client verifies worker TLS and never forwards authentication
// through redirects. Control requests have bounded time and response size.
func controlClient(opts Options) (*http.Client, error) {
	var client http.Client
	if opts.Client != nil {
		client = *opts.Client
	} else {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if opts.CAFile != "" {
			data, err := os.ReadFile(opts.CAFile)
			if err != nil {
				return nil, fmt.Errorf("read worker CA file: %w", err)
			}
			if !roots.AppendCertsFromPEM(data) {
				return nil, fmt.Errorf("worker CA file has no certificates")
			}
		}
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxIdleConns: 64, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 2 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	}
	client.Timeout = 3 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client, nil
}
func rpc(ctx context.Context, client *http.Client, token, url, method string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(r)
	if err != nil {
		return fmt.Errorf("worker control request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("worker control returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, controlLimit+1))
	if err != nil || len(data) > controlLimit {
		return fmt.Errorf("worker control response is incomplete or exceeds 32 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return fmt.Errorf("invalid worker control response")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("invalid trailing worker response")
	}
	return nil
}
func parallel(nodes []*node, fn func(*node)) {
	var group sync.WaitGroup
	for _, n := range nodes {
		group.Add(1)
		go func(n *node) { defer group.Done(); fn(n) }(n)
	}
	group.Wait()
}
func probe(ctx context.Context, client *http.Client, token string, n *node) {
	best := time.Hour
	for range 3 {
		before := time.Now()
		var info Info
		if err := rpc(ctx, client, token, n.URL+"/v1/info", "GET", nil, &info); err != nil {
			n.err = err
			return
		}
		after := time.Now()
		rtt := after.Sub(before)
		if info.Protocol != Protocol || info.Now.IsZero() || info.Busy || strings.TrimSpace(info.Name) == "" || len(info.Name) > 256 {
			n.err = fmt.Errorf("worker is busy or uses an incompatible protocol")
			return
		}
		if rtt < best {
			best = rtt
			n.Name = info.Name
			n.offset = info.Now.Sub(before.Add(rtt / 2))
			n.ClockOffsetMS = float64(n.offset) / 1e6
			n.RoundTripMS = float64(rtt) / 1e6
		}
	}
	if best > 500*time.Millisecond {
		n.err = fmt.Errorf("worker clock probe round trip exceeds 500ms")
	}
}
func cancelJobs(client *http.Client, token string, nodes []*node) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	parallel(nodes, func(n *node) {
		if n.id != "" {
			var ready Ready
			_ = rpc(ctx, client, token, n.URL+"/v1/jobs/"+n.id+"/cancel", "POST", struct{}{}, &ready)
		}
	})
}

func validateFinal(c config.Config, n *node) error {
	s := n.status
	if s.ID != n.id || s.Result == nil || s.Data == nil || s.Result.Assignment == nil || *s.Result.Assignment != n.assignment || s.Result.Name != c.Name || s.Result.URL != c.URL || s.Result.Mode != c.Mode || s.Result.Counts.Completed != s.Data.Requests.Counts.Completed || s.Result.Counts.Started != s.Data.Requests.Counts.Started || s.Result.Counts.Scheduled != s.Data.Requests.Counts.Scheduled {
		return fmt.Errorf("worker result does not match its assignment")
	}
	p, _ := c.Validate()
	if !s.Result.StartedAt.Equal(n.assignment.StartAt) || s.Result.LoadSeconds < 0 || s.Result.LoadSeconds > p.LoadDuration.Seconds() || s.Result.TotalSeconds < s.Result.LoadSeconds || s.Result.TotalSeconds > (p.LoadDuration+max(p.JourneyLimit, p.RequestTimeout)+20*time.Second).Seconds() {
		return fmt.Errorf("worker measurement window is invalid")
	}
	part := *s.Result
	part.Merge = s.Data
	_, err := engine.Combine(c, time.Now(), time.Duration(part.LoadSeconds*float64(time.Second)), time.Duration(part.TotalSeconds*float64(time.Second)), []engine.Result{part}, engine.Distribution{}, part.Interrupted)
	if err != nil {
		return err
	}
	if part.Arrivals().Scheduled > n.Planned || (!part.Interrupted && part.Arrivals().Scheduled != n.Planned) {
		return fmt.Errorf("worker result does not cover its assigned arrivals")
	}
	n.part = &part
	n.Requests = part.Snapshot
	n.Journeys = part.Journeys
	n.Generator = part.Generator
	return nil
}

func Run(ctx context.Context, c config.Config, opts Options) (engine.Result, error) {
	p, err := c.Validate()
	if err != nil {
		return engine.Result{}, err
	}
	if err := config.ValidateWorkerTransport(c.Workers, opts.AllowHTTPWorkers); err != nil {
		return engine.Result{}, err
	}
	if len(c.Workers) == 0 {
		return engine.Result{}, fmt.Errorf("distributed runs need worker URLs")
	}
	if err := ValidToken(opts.Token); err != nil {
		return engine.Result{}, err
	}
	var targetSampler *telemetry.Sampler
	if p.TargetTelemetry != nil {
		targetSampler, err = telemetry.New(p.TargetTelemetry.Config, opts.Telemetry)
		if err != nil {
			return engine.Result{}, err
		}
		defer targetSampler.Close()
	}
	client, err := controlClient(opts)
	if err != nil {
		return engine.Result{}, err
	}
	if opts.Client == nil {
		defer client.CloseIdleConnections()
	}
	if opts.StartDelay == 0 {
		opts.StartDelay = 3 * time.Second
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	if opts.StartDelay < 50*time.Millisecond || opts.StartDelay > 10*time.Second || opts.PollInterval < 10*time.Millisecond || opts.PollInterval > 2*time.Second {
		return engine.Result{}, fmt.Errorf("invalid worker coordination interval")
	}
	var nodes []*node
	var planned int64
	for i, url := range c.Workers {
		capacity := c.MaxInFlight / len(c.Workers)
		if i < c.MaxInFlight%len(c.Workers) {
			capacity++
		}
		assigned := engine.PlannedForWorker(p, i, len(c.Workers))
		planned += assigned
		nodes = append(nodes, &node{WorkerStatus: engine.WorkerStatus{URL: strings.TrimSuffix(url, "/"), Index: i, Capacity: capacity, Planned: assigned, Status: "preparing"}})
	}
	parallel(nodes, func(n *node) { probe(ctx, client, opts.Token, n) })
	for _, n := range nodes {
		if n.err != nil {
			return engine.Result{}, fmt.Errorf("worker %d preflight: %w", n.Index+1, n.err)
		}
	}
	local := c
	local.Workers = nil
	local.Telemetry = nil
	data, err := json.Marshal(local)
	if err != nil {
		return engine.Result{}, err
	}
	parallel(nodes, func(n *node) {
		var ready Ready
		n.err = rpc(ctx, client, opts.Token, n.URL+"/v1/jobs", "POST", Prepare{data, n.Index, len(nodes), n.Capacity}, &ready)
		if n.err == nil {
			_, idErr := hex.DecodeString(ready.ID)
			if len(ready.ID) != 32 || idErr != nil {
				n.err = fmt.Errorf("worker reservation ID is invalid")
			} else {
				n.id = ready.ID
				n.Status = "prepared"
			}
		}
	})
	for _, n := range nodes {
		if n.err != nil {
			cancelJobs(client, opts.Token, nodes)
			return engine.Result{}, fmt.Errorf("worker %d reservation: %w", n.Index+1, n.err)
		}
	}
	start := time.Now().Add(opts.StartDelay)
	if targetSampler != nil {
		targetSampler.Start(ctx, start)
	}
	parallel(nodes, func(n *node) {
		n.assignment = engine.Assignment{Index: n.Index, Count: len(nodes), Capacity: n.Capacity, StartAt: start.Add(n.offset).UTC()}
		var ready Ready
		n.err = rpc(ctx, client, opts.Token, n.URL+"/v1/jobs/"+n.id+"/start", "POST", Commit{n.assignment.StartAt}, &ready)
		if n.err == nil && ready.ID != n.id {
			n.err = fmt.Errorf("worker commit ID mismatch")
		}
		if n.err == nil {
			n.Status = "scheduled"
		}
	})
	aborted := false
	userCanceled := false
	failure := ""
	var abortAt, stopPolling time.Time
	abort := func(reason string) {
		if !aborted {
			aborted = true
			abortAt = time.Now()
			failure = reason
			stopPolling = abortAt.Add(6 * time.Second)
			cancelJobs(client, opts.Token, nodes)
		}
	}
	for _, n := range nodes {
		if n.err != nil {
			n.Error = n.err.Error()
			abort("not all workers acknowledged the coordinated start")
		}
	}
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()
	lastProgress := time.Time{}
	deadline := start.Add(p.LoadDuration + max(p.RequestTimeout, p.JourneyLimit) + 15*time.Second)
	for {
		if ctx.Err() != nil {
			userCanceled = true
			abort("distributed run canceled")
		}
		pollCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		parallel(nodes, func(n *node) {
			if !n.terminal {
				var status Status
				n.err = rpc(pollCtx, client, opts.Token, n.URL+"/v1/jobs/"+n.id, "GET", nil, &status)
				if n.err == nil {
					n.status = status
				}
			}
		})
		cancel()
		allDone := true
		for _, n := range nodes {
			if n.terminal {
				continue
			}
			if n.err != nil {
				n.failures++
				if n.failures >= 3 {
					n.terminal = true
					n.Status = "unreachable"
					n.Error = n.err.Error()
					abort("a worker became unavailable; its final traffic counts are unknown")
				}
			} else {
				n.failures = 0
				if n.status.ID != n.id {
					n.terminal = true
					n.Status = "invalid"
					n.Error = "worker job ID mismatch"
					abort("a worker returned an invalid job")
				} else {
					n.Status = n.status.State
					if progress := n.status.Progress; progress != nil {
						n.Requests = progress.Requests
						n.Journeys = progress.Journeys
					}
					switch n.status.State {
					case "finished", "interrupted":
						n.terminal = true
						if err := validateFinal(c, n); err != nil {
							n.Status = "invalid"
							n.Error = err.Error()
							abort("a worker returned invalid final metrics")
						} else if n.status.Result.Interrupted {
							n.Error = n.status.Error
							if !aborted {
								abort("a worker interrupted its assigned schedule")
							}
						}
					case "canceled", "expired":
						n.terminal = true
						n.Error = n.status.Error
						abort("a worker ended without final measurements")
					case "prepared", "scheduled", "running", "stopping":
					default:
						n.terminal = true
						n.Status = "invalid"
						n.Error = "unknown worker state"
						abort("a worker returned an invalid state")
					}
				}
			}
			allDone = allDone && n.terminal
		}
		if opts.OnProgress != nil && time.Since(lastProgress) >= time.Second && !time.Now().Before(start) {
			elapsed := max(time.Since(start), time.Duration(0))
			load := min(elapsed, p.LoadDuration)
			if aborted {
				load = min(max(abortAt.Sub(start), time.Duration(0)), p.LoadDuration)
			}
			var states []engine.LiveData
			var statuses []engine.WorkerStatus
			for _, n := range nodes {
				statuses = append(statuses, n.WorkerStatus)
				if n.part != nil {
					states = append(states, n.part.Merge.LiveData)
				} else if !n.terminal && n.status.Progress != nil && n.status.Progress.Live != nil {
					states = append(states, *n.status.Progress.Live)
				}
			}
			requests, journeys, err := engine.MergeLive(states, load)
			if err != nil {
				abort("a worker returned invalid live metrics")
			} else {
				phaseIndex := 0
				var end time.Duration
				for i, phase := range p.Phases {
					phaseIndex = i
					end += phase.Duration
					if load < end {
						break
					}
				}
				workload := "requests"
				if p.IsJourney() {
					workload = "journeys"
				}
				progress := engine.Progress{Elapsed: load, WallElapsed: elapsed, PhaseIndex: phaseIndex, PhaseName: p.Phases[phaseIndex].Name, TargetRate: p.Phases[phaseIndex].Rate, Draining: aborted || elapsed >= p.LoadDuration, Snapshot: requests, Journeys: journeys, Workload: workload, Workers: statuses}
				if targetSampler != nil {
					progress.Telemetry = targetSampler.Live()
				}
				opts.OnProgress(progress)
			}
			lastProgress = time.Now()
		}
		if allDone {
			break
		}
		if time.Now().After(deadline) {
			abort("worker completion deadline exceeded")
		}
		if aborted && time.Now().After(stopPolling) {
			for _, n := range nodes {
				if !n.terminal {
					n.terminal = true
					n.Status = "unavailable"
					n.Error = "final metrics were not received before cleanup ended"
				}
			}
			break
		}
		if aborted {
			<-ticker.C
		} else {
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
		}
	}
	var parts []engine.Result
	var statuses []engine.WorkerStatus
	complete := !aborted
	for _, n := range nodes {
		statuses = append(statuses, n.WorkerStatus)
		if n.part != nil {
			parts = append(parts, *n.part)
		} else {
			complete = false
		}
	}
	load := min(max(time.Since(start), time.Duration(0)), p.LoadDuration)
	if aborted {
		load = min(max(abortAt.Sub(start), time.Duration(0)), p.LoadDuration)
	}
	result, err := engine.Combine(c, start, load, max(time.Since(start), time.Duration(0)), parts, engine.Distribution{Complete: complete, PlannedArrivals: planned, PeakInFlightUpperBound: true, Failure: failure, Workers: statuses}, userCanceled)
	if err == nil && targetSampler != nil {
		value := targetSampler.Finish()
		result.Telemetry = &value
		result.SchemaVersion = max(result.SchemaVersion, 5)
		result.TotalSeconds = max(result.TotalSeconds, time.Since(start).Seconds())
	}
	return result, err
}
