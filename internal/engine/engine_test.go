package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
	"gometer/internal/metrics"
)

func testConfig(url string) config.Config {
	c := config.Defaults()
	c.URL, c.Mode, c.Rate, c.Duration = url, "rate", 20, "250ms"
	c.Timeout = "1s"
	return c
}

func runTest(t *testing.T, c config.Config) Result {
	t.Helper()
	r, err := Run(context.Background(), c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertAccounting(t, r.Snapshot)
	for _, p := range r.Phases {
		assertAccounting(t, p.Snapshot)
	}
	return r
}

func assertAccounting(t *testing.T, s metrics.Snapshot) {
	t.Helper()
	if s.Scheduled != s.Started+s.DroppedCapacity+s.DroppedScheduler || s.Started != s.Completed || s.Completed != s.Succeeded+s.Failed || s.InFlight != 0 || s.Latency.Samples != s.Completed {
		t.Fatalf("broken accounting: %+v", s)
	}
}

func TestRateArrivalsAndFullResponseTiming(t *testing.T) {
	var received atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		io.WriteString(w, "hello ")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Millisecond):
		}
		io.WriteString(w, "world")
	}))
	defer s.Close()
	r := runTest(t, testConfig(s.URL))
	if r.Scheduled != 5 || r.Started != 5 || received.Load() != 5 || r.BytesRead != 55 || r.Latency.MinMS < 12 || !r.Passed {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestCapacityDropsKeepOfferedDemandVisible(t *testing.T) {
	var received atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(65 * time.Millisecond):
		}
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 50
	c.Duration = "200ms"
	c.MaxInFlight = 1
	r := runTest(t, c)
	if r.Scheduled != 10 || r.DroppedCapacity == 0 || r.PeakInFlight != 1 || r.Started != received.Load() || r.Passed {
		t.Fatalf("capacity loss hidden: %+v", r)
	}
}

func TestUsersRespectConcurrency(t *testing.T) {
	var active, peak atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Mode = "users"
	c.Rate = 0
	c.VUs = 3
	c.ThinkTime = "5ms"
	c.Duration = "150ms"
	r := runTest(t, c)
	if peak.Load() > 3 || peak.Load() < 2 || r.PeakInFlight > 3 || r.Completed == 0 || !r.Passed {
		t.Fatalf("invalid users workload: %+v; server peak=%d", r, peak.Load())
	}
}

func TestRequestsDrainAfterPlannedLoadEnd(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 1
	c.Duration = "20ms"
	r := runTest(t, c)
	if r.Completed != 1 || r.Failed != 0 || r.LoadSeconds > .03 || r.TotalSeconds < .09 || r.Latency.P95MS < 90 || !r.Passed {
		t.Fatalf("request did not drain: %+v", r)
	}
}

func TestCancellationStopsActiveRequestsAndFailsRun(t *testing.T) {
	started := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	c := testConfig(s.URL)
	c.Duration = "5s"
	r, err := Run(ctx, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertAccounting(t, r.Snapshot)
	if !r.Interrupted || r.Passed || r.Errors["canceled"] == 0 || r.TotalSeconds > 1 {
		t.Fatalf("unexpected cancellation: %+v", r)
	}
}

func TestTimeoutIncludesResponseBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		fmt.Fprint(w, "late body")
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 1
	c.Duration = "20ms"
	c.Timeout = "40ms"
	r := runTest(t, c)
	if r.Errors["timeout"] != 1 || r.Statuses[200] != 1 || r.Passed {
		t.Fatalf("body timeout hidden: %+v", r)
	}
	if r.HTTP == nil || r.HTTP.BodyRead.Samples != 1 || r.HTTP.BodyReadFailures != 1 {
		t.Fatalf("failed body read missing from timings: %+v", r.HTTP)
	}
}

func TestRedirectIsOneAttemptAndCanBeExpected(t *testing.T) {
	var final atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			final.Add(1)
			fmt.Fprint(w, "ok")
			return
		}
		http.Redirect(w, r, "/final", 302)
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 1
	c.Duration = "20ms"
	r := runTest(t, c)
	if final.Load() != 0 || r.Statuses[302] != 1 || r.Errors["http_status"] != 1 || r.Passed {
		t.Fatalf("redirect behavior: %+v", r)
	}
	c.SuccessStatuses = []int{302}
	r = runTest(t, c)
	if final.Load() != 0 || !r.Passed {
		t.Fatalf("explicit success status ignored: %+v", r)
	}
}

func TestRequestBodyHeadersAndResponseLimit(t *testing.T) {
	var correct atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		correct.Store(r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" && r.Host == "example.test" && string(body) == `{"ok":true}`)
		io.WriteString(w, strings.Repeat("x", 100))
	}))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 1
	c.Duration = "20ms"
	c.Method = "POST"
	c.Body = `{"ok":true}`
	c.Headers = map[string]string{"Content-Type": "application/json", "Host": "example.test"}
	c.MaxResponseBytes = 10
	r := runTest(t, c)
	if !correct.Load() || r.Errors["response_too_large"] != 1 || r.BytesRead != 11 || r.Passed {
		t.Fatalf("request or size limit incorrect: %+v", r)
	}
	if r.HTTP == nil || r.HTTP.BodyRead.Samples != 1 || r.HTTP.BodyReadFailures != 1 {
		t.Fatalf("response cap missing from timings: %+v", r.HTTP)
	}
}

func TestPhasesIncludePauseAndRecovery(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer s.Close()
	c := testConfig(s.URL)
	c.Rate = 0
	c.Stages = []config.Stage{{Name: "baseline", Duration: "100ms", Rate: 10}, {Name: "pause", Duration: "80ms", Rate: 0}, {Name: "recovery", Duration: "100ms", Rate: 10}}
	r := runTest(t, c)
	if r.Scheduled != 2 || len(r.Phases) != 3 || r.Phases[1].Scheduled != 0 || r.Phases[2].Completed != 1 || !r.Passed {
		t.Fatalf("phase accounting incorrect: %+v", r)
	}
}

func TestLateSchedulerDropsPastPhaseWithoutBurst(t *testing.T) {
	p, err := testConfig("http://localhost").Validate()
	if err != nil {
		t.Fatal(err)
	}
	r := runner{plan: p, total: metrics.New(), phases: []*metrics.Collector{metrics.New()}}
	r.runRate(context.Background(), context.Background(), time.Now().Add(-time.Second))
	s := r.total.Snapshot(p.LoadDuration)
	if s.Scheduled != 5 || s.Started != 0 || s.DroppedScheduler != 5 {
		t.Fatalf("past arrivals were replayed: %+v", s)
	}
}

func TestThresholdsAndEmptyRun(t *testing.T) {
	p, err := testConfig("http://localhost").Validate()
	if err != nil {
		t.Fatal(err)
	}
	r := Result{}
	r.evaluate(p)
	if r.Passed {
		t.Fatal("empty run passed")
	}
	p.P95Limit = 100 * time.Millisecond
	v := 20.0
	p.Thresholds.MinStartedRPS = &v
	r = Result{Snapshot: metrics.Snapshot{Counts: metrics.Counts{Completed: 10}, StartedRPS: 19, Latency: metrics.Latency{P95MS: 101}}}
	r.evaluate(p)
	failed := map[string]bool{}
	for _, check := range r.Checks {
		if !check.Passed {
			failed[check.Name] = true
		}
	}
	if !failed["p95"] || !failed["min_started_rps"] || r.Passed {
		t.Fatalf("thresholds ignored: %+v", r.Checks)
	}
}
