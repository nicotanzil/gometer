package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/distributed"
)

func TestDashboardDistributedRunPersistenceAndImport(t *testing.T) {
	s := testServer(t)
	s.workerToken = "dashboard-test-token-012345678901234567890123456789"
	worker, err := distributed.NewWorker(context.Background(), "Dashboard worker", s.workerToken)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	w := httptest.NewServer(worker)
	defer w.Close()
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(out, "ok") }))
	defer target.Close()
	body := fmt.Sprintf(`{"name":"Fleet","url":%q,"mode":"rate","rate":5,"duration":"1250ms","max_in_flight":2,"workers":[%q]}`, target.URL, w.URL)
	if response := request(s, "POST", "/api/validate", body); response.Code != 200 || attempts.Load() != 0 {
		t.Fatal("worker validation sent traffic or rejected configuration")
	}
	id := startRun(t, s, body)
	run := awaitRun(t, s, id)
	if run.Status != "passed" || run.Result == nil || run.Result.SchemaVersion != 4 || run.Result.Completed != 7 || !run.Result.Distribution.Complete || len(run.Samples) < 3 || len(run.Samples[1].Workers) != 1 || attempts.Load() != 7 {
		t.Fatalf("distributed dashboard results: %+v", run)
	}
	bundle, _ := json.Marshal(run)
	if strings.Contains(string(bundle), s.workerToken) || strings.Contains(string(bundle), `"sum_ns"`) {
		t.Fatal("bundle exported credentials or transport histogram state")
	}
	response := request(s, "POST", "/api/runs/import", string(bundle))
	if response.Code != 201 || attempts.Load() != 7 {
		t.Fatalf("schema 4 import: %d %s", response.Code, response.Body)
	}
	reopened, err := openStore(s.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.getRun(id)
	if err != nil || saved.Result.Distribution.Workers[0].Name != "Dashboard worker" || len(saved.Samples[1].Workers) != 1 {
		t.Fatal("worker results did not survive restart")
	}
}

func TestDashboardMissingWorkerTokenFailsBeforeReservation(t *testing.T) {
	s := testServer(t)
	s.workerToken = ""
	body := `{"url":"http://127.0.0.1:8787/work","mode":"rate","rate":2,"workers":["http://127.0.0.1:8801"]}`
	if response := request(s, "POST", "/api/validate", body); response.Code != 200 {
		t.Fatal("validation requires authentication or network access")
	}
	if response := request(s, "POST", "/api/runs", body); response.Code != 400 || len(s.store.runs) != 0 {
		t.Fatal("missing token reserved a dashboard run")
	}
}

func TestDistributedStopTimelineCanBeImportedAfterSlowCleanup(t *testing.T) {
	s := testServer(t)
	s.workerToken = "dashboard-test-token-012345678901234567890123456789"
	worker, err := distributed.NewWorker(context.Background(), "Slow cleanup worker", s.workerToken)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	w := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		worker.ServeHTTP(out, r)
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			// Final metrics must use the stopped load window even when a
			// control acknowledgement takes longer than a progress interval.
			time.Sleep(1100 * time.Millisecond)
		}
	}))
	defer w.Close()
	received := make(chan struct{}, 1)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		select {
		case received <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer target.Close()
	id := startRun(t, s, fmt.Sprintf(`{"name":"Stopped fleet","url":%q,"mode":"rate","rate":1,"duration":"5s","max_in_flight":1,"workers":[%q]}`, target.URL, w.URL))
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not send its first request")
	}
	if response := request(s, "POST", "/api/runs/"+id+"/stop", ""); response.Code != 202 {
		t.Fatal("distributed stop failed")
	}
	run := awaitRun(t, s, id)
	if run.Status != "interrupted" || run.Result == nil || run.Result.Completed != 1 || run.Result.Failed != 1 || run.Result.Distribution.Complete || len(run.Samples) < 3 {
		t.Fatalf("invalid interrupted worker result: %+v", run)
	}
	for _, sample := range run.Samples {
		if sample.LoadSeconds > run.Result.LoadSeconds {
			t.Fatalf("cleanup extended the load timeline: sample=%v result=%v", sample.LoadSeconds, run.Result.LoadSeconds)
		}
	}
	bundle, _ := json.Marshal(run)
	response := request(s, "POST", "/api/runs/import", string(bundle))
	if response.Code != 201 || attempts.Load() != 1 {
		t.Fatalf("interrupted fleet import: %d %s", response.Code, response.Body)
	}
}
