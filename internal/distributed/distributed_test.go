package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

const testToken = "test-worker-token-012345678901234567890123456789"

func testWorker(t *testing.T, name, token string) (*Worker, *httptest.Server) {
	t.Helper()
	w, err := NewWorker(context.Background(), name, token)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(w)
	t.Cleanup(func() { w.Close(); s.Close() })
	return w, s
}

func TestWorkerReservationExpiryAndBrowserOriginBoundary(t *testing.T) {
	w, s := testWorker(t, "A", testToken)
	w.mu.Lock()
	w.reservation = 50 * time.Millisecond
	w.mu.Unlock()
	c := rateConfig("http://127.0.0.1:1")
	c.MaxInFlight = 1
	data, _ := json.Marshal(c)
	client, _ := controlClient(testOptions())
	defer client.CloseIdleConnections()
	var ready Ready
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs", "POST", Prepare{data, 0, 1, 1}, &ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		expired := w.job.State == "expired"
		w.mu.Unlock()
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var info Info
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/info", "GET", nil, &info); err != nil || info.Busy {
		t.Fatal("abandoned reservation did not expire")
	}
	r := httptest.NewRequest("GET", s.URL+"/v1/info", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Origin", "https://outside.example")
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("browser-origin worker control allowed")
	}
	if err := ServeWorker(context.Background(), "0.0.0.0:0", "A", testToken, "", "", false, io.Discard); err == nil {
		t.Fatal("plaintext remote worker listener allowed")
	}
}
func rateConfig(url string, workers ...string) config.Config {
	c := config.Defaults()
	c.URL = url
	c.Mode = "rate"
	c.Duration = "550ms"
	c.Rate = 7
	c.MaxInFlight = 5
	c.Workers = workers
	return c
}
func testOptions() Options {
	return Options{Token: testToken, StartDelay: 200 * time.Millisecond, PollInterval: 25 * time.Millisecond}
}

func TestCoordinatorDividesFleetRateAndCapacity(t *testing.T) {
	_, a := testWorker(t, "A", testToken)
	_, b := testWorker(t, "B", testToken)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(w, "ok") }))
	defer target.Close()
	r, err := Run(context.Background(), rateConfig(target.URL, a.URL, b.URL), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.SchemaVersion != 4 || r.Scheduled != 4 || r.Completed != 4 || attempts.Load() != 4 || r.Latency.Samples != 4 || r.HTTP.FirstByte.Samples != 4 || r.Generator != nil || !r.Distribution.Complete {
		t.Fatalf("fleet totals changed: %+v", r)
	}
	for i, w := range r.Distribution.Workers {
		if w.Planned != 2 || w.Requests.Completed != 2 || w.Capacity != 3-i || w.Generator == nil {
			t.Fatalf("worker metrics: %+v", w)
		}
	}
}

func TestWorkersExecuteAndMergeNestedControls(t *testing.T) {
	_, a := testWorker(t, "Flow A", testToken)
	_, b := testWorker(t, "Flow B", testToken)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(w, "ok") }))
	defer target.Close()
	count := 3
	value := "1"
	c := rateConfig(target.URL, a.URL, b.URL)
	c.Rate = 4
	c.Duration = "500ms"
	c.MaxInFlight = 2
	c.Scenarios = []config.Scenario{{Name: "Flow", Steps: []config.Step{{Name: "Repeat", Loop: &config.Loop{Count: &count, Steps: []config.Step{{Name: "Choose", If: &config.Branch{Condition: config.Condition{Variable: "journey_id", Value: &value}, Then: []config.Step{{Name: "First", URL: "/first"}}, Else: []config.Step{{Name: "Other", URL: "/other"}}}}}}}}}}
	r, err := Run(context.Background(), c, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateFlow(p); err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.SchemaVersion != 6 || r.Completed != 6 || attempts.Load() != 6 || r.Journeys.Completed != 2 || r.Scenarios[0].Controls[0].Iterations != 6 || r.Scenarios[0].Controls[1].ConditionTrue != 3 || r.Scenarios[0].Controls[1].ConditionFalse != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestJitteredJourneyMixRetainsGlobalIDsAndWeights(t *testing.T) {
	_, a := testWorker(t, "A", testToken)
	_, b := testWorker(t, "B", testToken)
	var mu sync.Mutex
	seen := map[string]int{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := rateConfig(target.URL, a.URL, b.URL)
	c.Rate = 20
	c.Duration = "450ms"
	c.ArrivalDistribution = "jitter"
	three, one := 3, 1
	c.Scenarios = []config.Scenario{{Name: "Health", Weight: &three, Steps: []config.Step{{URL: "/health/{{journey_id}}"}}}, {Name: "Work", Weight: &one, Steps: []config.Step{{URL: "/work/{{journey_id}}"}}}}
	r, err := Run(context.Background(), c, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.Journeys.Completed != 9 || r.Completed != 9 || r.Scenarios[0].Journeys.Completed != 7 || r.Scenarios[1].Journeys.Completed != 2 || len(seen) != 9 {
		t.Fatalf("global mix changed: %+v, %v", r, seen)
	}
	for _, n := range seen {
		if n != 1 {
			t.Fatal("duplicate journey ID")
		}
	}
}

func TestUnavailablePreflightSendsNoTargetTraffic(t *testing.T) {
	w, a := testWorker(t, "A", testToken)
	_, b := testWorker(t, "B", strings.Repeat("x", 40))
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer target.Close()
	if _, err := Run(context.Background(), rateConfig(target.URL, a.URL, b.URL), testOptions()); err == nil {
		t.Fatal("unauthenticated worker accepted")
	}
	w.mu.Lock()
	reserved := w.job != nil
	w.mu.Unlock()
	if attempts.Load() != 0 || reserved {
		t.Fatal("preflight dispatched or reserved work before all workers were ready")
	}
}

func TestWorkerLostAfterAdmissionFailsWithoutInventingCounts(t *testing.T) {
	_, a := testWorker(t, "A", testToken)
	w, err := NewWorker(context.Background(), "B", testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var attempts atomic.Int64
	b := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/jobs/") && attempts.Load() > 0 {
			conn, _, err := out.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.ServeHTTP(out, r)
	}))
	defer b.Close()
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(out, "ok") }))
	defer target.Close()
	c := rateConfig(target.URL, a.URL, b.URL)
	c.Duration = "2s"
	c.Rate = 20
	r, err := Run(context.Background(), c, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || r.Interrupted || r.Checks[0].Passed || r.Distribution.Complete || r.Distribution.UnreportedPlannedArrivals == 0 || r.Distribution.Workers[1].Status != "unreachable" || r.Completed > attempts.Load() || r.Started != r.Completed {
		t.Fatalf("lost worker was treated as measured traffic: %+v actual=%d", r, attempts.Load())
	}
}

func TestCoordinatorCancellationCollectsPartialResults(t *testing.T) {
	_, a := testWorker(t, "A", testToken)
	received := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer target.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-received:
			cancel()
		case <-ctx.Done():
		}
	}()
	c := rateConfig(target.URL, a.URL)
	c.Duration = "2s"
	c.Rate = 10
	r, err := Run(ctx, c, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Interrupted || r.Passed || r.Started != r.Completed || r.Failed != 1 || r.Distribution.Complete || r.Distribution.Workers[0].Status != "interrupted" {
		t.Fatalf("cancellation lost partial work: %+v", r)
	}
}

func TestWorkerAuthenticationReservationAndIdempotentCommit(t *testing.T) {
	w, s := testWorker(t, "A", testToken)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(w, "ok") }))
	defer target.Close()
	c := rateConfig(target.URL)
	c.MaxInFlight = 1
	c.Rate = 1
	c.Duration = "150ms"
	data, _ := json.Marshal(c)
	client, _ := controlClient(testOptions())
	defer client.CloseIdleConnections()
	var info Info
	if err := rpc(context.Background(), client, "wrong", s.URL+"/v1/info", "GET", nil, &info); err == nil {
		t.Fatal("missing authentication accepted")
	}
	var ready Ready
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs", "POST", Prepare{data, 0, 1, 1}, &ready); err != nil {
		t.Fatal(err)
	}
	var other Ready
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs", "POST", Prepare{data, 0, 1, 1}, &other); err == nil {
		t.Fatal("concurrent reservation accepted")
	}
	if attempts.Load() != 0 {
		t.Fatal("reservation sent target traffic")
	}
	commit := Commit{time.Now().Add(150 * time.Millisecond).UTC()}
	for range 2 {
		if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs/"+ready.ID+"/start", "POST", commit, &other); err != nil {
			t.Fatal(err)
		}
	}
	w.mu.Lock()
	done := w.job.done
	w.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish")
	}
	if attempts.Load() != 1 {
		t.Fatal("duplicate commit dispatched duplicate traffic")
	}
}

func TestWorkerLeaseExpiresWithoutHeartbeat(t *testing.T) {
	w, s := testWorker(t, "A", testToken)
	w.mu.Lock()
	w.lease = 100 * time.Millisecond
	w.mu.Unlock()
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer target.Close()
	c := rateConfig(target.URL)
	c.Duration = "5s"
	c.Rate = 1
	c.MaxInFlight = 1
	data, _ := json.Marshal(c)
	client, _ := controlClient(testOptions())
	defer client.CloseIdleConnections()
	var ready Ready
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs", "POST", Prepare{data, 0, 1, 1}, &ready); err != nil {
		t.Fatal(err)
	}
	if err := rpc(context.Background(), client, testToken, s.URL+"/v1/jobs/"+ready.ID+"/start", "POST", Commit{time.Now().Add(50 * time.Millisecond).UTC()}, &ready); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	done := w.job.done
	w.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker continued after coordinator lease expired")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.job.State != "interrupted" || w.job.Error != "coordinator lease expired" || w.job.Result.Started != w.job.Result.Completed {
		t.Fatalf("lease loss: %+v", w.job.Status)
	}
}

func TestWorkerTLSAndRedirectsDoNotLeakAuthentication(t *testing.T) {
	w, err := NewWorker(context.Background(), "TLS", testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s := httptest.NewTLSServer(w)
	defer s.Close()
	var leaked atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Add(1)
		}
		fmt.Fprint(out, "ok")
	}))
	defer target.Close()
	c := rateConfig(target.URL, s.URL)
	c.Rate = 1
	c.Duration = "50ms"
	c.MaxInFlight = 1
	c.Insecure = true // Workload TLS opt-in never disables worker verification.
	untrusted := testOptions()
	untrusted.AllowHTTPWorkers = true // HTTP opt-in must not weaken HTTPS verification.
	if _, err := Run(context.Background(), c, untrusted); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	opts := testOptions()
	opts.Client = s.Client()
	opts.AllowHTTPWorkers = true
	r, err := Run(context.Background(), c, opts)
	if err != nil || !r.Passed {
		t.Fatalf("trusted worker TLS: %v %+v", err, r)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) { http.Redirect(out, r, target.URL, 302) }))
	defer redirect.Close()
	c.Workers = []string{redirect.URL}
	if _, err := Run(context.Background(), c, testOptions()); err == nil || leaked.Load() != 0 {
		t.Fatal("worker authentication followed a redirect")
	}
}

func TestInsecureWorkloadRunsOnWorker(t *testing.T) {
	_, worker := testWorker(t, "TLS workload", testToken)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	c := rateConfig(target.URL, worker.URL)
	c.Rate, c.Duration, c.MaxInFlight, c.Insecure = 1, "50ms", 1, true
	r, err := Run(context.Background(), c, testOptions())
	if err != nil || !r.Passed || !r.Insecure || !r.Distribution.Complete || r.Completed != 1 {
		t.Fatalf("insecure worker workload: %v %+v", err, r)
	}
}

func TestCoordinatorEnforcesHTTPPermissionBeforeContactingWorkers(t *testing.T) {
	worker, err := NewWorker(context.Background(), "HTTP", testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	var control, workload atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { control.Add(1); worker.ServeHTTP(w, r) }))
	defer fixture.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { workload.Add(1); fmt.Fprint(w, "ok") }))
	defer target.Close()
	parsed, _ := url.Parse(fixture.URL)
	c := rateConfig(target.URL, "http://localhost:"+parsed.Port())
	c.Duration, c.Rate = "250ms", 4
	if _, err := Run(context.Background(), c, testOptions()); err == nil || control.Load() != 0 || workload.Load() != 0 {
		t.Fatalf("default policy sent traffic: %v control=%d workload=%d", err, control.Load(), workload.Load())
	}
	opts := testOptions()
	opts.AllowHTTPWorkers = true
	result, err := Run(context.Background(), c, opts)
	if err != nil || !result.Passed || workload.Load() != 1 {
		t.Fatalf("explicit HTTP permission: %v result=%+v requests=%d", err, result, workload.Load())
	}
}
