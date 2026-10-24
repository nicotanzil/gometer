package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"gometer/internal/distributed"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerCatalogPersistsAndRejectsEquivalentOrigins(t *testing.T) {
	s := testServer(t)
	created := request(s, "POST", "/api/workers", `{"name":"East generator","origin":"https://WORKER.example:443/"}`)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var worker struct{ ID, Name, Origin string }
	if err := json.Unmarshal(created.Body.Bytes(), &worker); err != nil {
		t.Fatal(err)
	}
	if !validID(worker.ID) || worker.Name != "East generator" || worker.Origin != "https://worker.example" {
		t.Fatalf("worker: %+v", worker)
	}
	duplicate := request(s, "POST", "/api/workers", `{"name":"Duplicate","origin":"https://worker.example"}`)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body)
	}
	for _, body := range []string{`{"name":"","origin":"https://worker.example"}`, `{"name":"Bad","origin":"https://user:secret@worker.example"}`, `{"name":"Bad","origin":"https://worker.example/path"}`, `{"name":"Bad","origin":"https://worker.example","token":"secret"}`} {
		if w := request(s, "POST", "/api/workers", body); w.Code != 400 {
			t.Fatalf("bad entry accepted: %d %s", w.Code, w.Body)
		}
	}
	edited := request(s, "PUT", "/api/workers/"+worker.ID, `{"name":"Renamed generator","origin":"https://other.example:9443/"}`)
	if edited.Code != 200 {
		t.Fatalf("edit: %d %s", edited.Code, edited.Body)
	}
	s.Close()
	reopened, err := New(context.Background(), s.store.dir, s.host, s.origin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	list := request(reopened, "GET", "/api/workers", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), worker.ID) || !strings.Contains(list.Body.String(), "Renamed generator") {
		t.Fatalf("catalog did not survive restart: %d %s", list.Code, list.Body)
	}
	removed := request(reopened, "DELETE", "/api/workers/"+worker.ID, "")
	if removed.Code != 200 {
		t.Fatalf("delete: %d %s", removed.Code, removed.Body)
	}
	if w := request(reopened, "GET", "/api/workers", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("catalog not empty: %s", w.Body)
	}
	if w := request(reopened, "DELETE", "/api/workers/"+worker.ID, ""); w.Code != 404 {
		t.Fatalf("missing worker: %d", w.Code)
	}
}

func saveWorker(t *testing.T, s *Server, name, origin string) SavedWorker {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "origin": origin})
	w := request(s, "POST", "/api/workers", string(body))
	if w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var worker SavedWorker
	if err := json.Unmarshal(w.Body.Bytes(), &worker); err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerCheckDoesNotSendWorkAndPersistsObservation(t *testing.T) {
	s := testServer(t)
	s.workerToken = strings.Repeat("t", 32)
	var calls atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/info" || r.Header.Get("Authorization") != "Bearer "+s.workerToken {
			t.Errorf("unexpected request")
		}
		json.NewEncoder(w).Encode(distributed.Info{Protocol: distributed.Protocol, Name: "fixture", Now: time.Now()})
	}))
	defer fixture.Close()
	worker := saveWorker(t, s, "East", fixture.URL)
	configuration := fmt.Sprintf(`{"url":%q,"mode":"rate","rate":1,"duration":"100ms","max_in_flight":2,"workers":[%q]}`, fixture.URL, fixture.URL)
	for _, path := range []string{"/api/validate", "/api/scenarios"} {
		if w := request(s, "POST", path, configuration); w.Code != 200 {
			t.Fatalf("offline configuration: %d %s", w.Code, w.Body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("registration, validation or saving sent traffic")
	}
	w := request(s, "POST", "/api/workers/"+worker.ID+"/check", "")
	if w.Code != 200 {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	var checked SavedWorker
	json.Unmarshal(w.Body.Bytes(), &checked)
	if checked.Check == nil || checked.Check.State != "reachable" || calls.Load() != 1 {
		t.Fatalf("check: %+v calls %d", checked, calls.Load())
	}
	s.Close()
	reopened, err := New(context.Background(), s.store.dir, s.host, s.origin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	list := request(reopened, "GET", "/api/workers", "")
	if !strings.Contains(list.Body.String(), `"reachable"`) || strings.Contains(list.Body.String(), s.workerToken) {
		t.Fatalf("saved check: %s", list.Body)
	}
	edited := request(reopened, "PUT", "/api/workers/"+worker.ID, `{"name":"Changed","origin":"https://other.example"}`)
	if edited.Code != 200 || strings.Contains(edited.Body.String(), `"check"`) {
		t.Fatalf("check survived origin edit: %s", edited.Body)
	}
}

func TestWorkerCheckConcurrentEditAndRemoval(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprint(remove), func(t *testing.T) {
			s := testServer(t)
			s.workerToken = strings.Repeat("t", 32)
			entered, release := make(chan struct{}), make(chan struct{})
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				json.NewEncoder(w).Encode(distributed.Info{Protocol: 1, Name: "fixture", Now: time.Now()})
			}))
			defer fixture.Close()
			worker := saveWorker(t, s, "Before", fixture.URL)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(s, "POST", "/api/workers/"+worker.ID+"/check", "") }()
			<-entered
			if got := request(s, "POST", "/api/workers/"+worker.ID+"/check", ""); got.Code != 409 {
				t.Errorf("duplicate check: %d", got.Code)
			}
			expected := 409
			if remove {
				expected = 404
				request(s, "DELETE", "/api/workers/"+worker.ID, "")
			} else {
				request(s, "PUT", "/api/workers/"+worker.ID, `{"name":"After","origin":"https://after.example"}`)
			}
			close(release)
			if got := <-done; got.Code != expected {
				t.Fatalf("stale check: %d %s", got.Code, got.Body)
			}
			list := request(s, "GET", "/api/workers", "")
			if strings.Contains(list.Body.String(), `"check"`) {
				t.Fatal("stale observation persisted")
			}
		})
	}
}

func TestRunFreezesWorkerCatalogIdentity(t *testing.T) {
	s := testServer(t)
	s.workerToken = strings.Repeat("t", 32)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(distributed.Info{Protocol: 1, Name: "fixture", Now: time.Now(), Busy: true})
	}))
	defer fixture.Close()
	worker := saveWorker(t, s, "Original identity", fixture.URL)
	configuration := fmt.Sprintf(`{"url":"http://127.0.0.1:1","mode":"rate","rate":1,"duration":"100ms","max_in_flight":2,"workers":[%q]}`, fixture.URL)
	id := startRun(t, s, configuration)
	request(s, "PUT", "/api/workers/"+worker.ID, `{"name":"Changed identity","origin":"https://changed.example"}`)
	request(s, "DELETE", "/api/workers/"+worker.ID, "")
	run := awaitRun(t, s, id)
	if len(run.WorkerSnapshots) != 1 || run.WorkerSnapshots[0].ID != worker.ID || run.WorkerSnapshots[0].Name != "Original identity" || run.WorkerSnapshots[0].Origin != fixture.URL || run.Config.Workers[0] != fixture.URL {
		t.Fatalf("run changed after catalog edit: %+v", run)
	}
	if run.Status != "error" {
		t.Fatalf("busy worker silently skipped: %s", run.Status)
	}
	s.mu.Lock()
	summary := s.store.runList()[0]
	s.mu.Unlock()
	if summary.WorkerSnapshots[0].Name != "Original identity" {
		t.Fatal("summary identity changed")
	}
}

func TestWorkerCatalogFailureDoesNotPublishUnsavedChanges(t *testing.T) {
	s := testServer(t)
	worker := saveWorker(t, s, "Original", "https://worker.example")
	dir := filepath.Join(s.store.dir, "workers")
	if err := os.Rename(dir, dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(s, "PUT", "/api/workers/"+worker.ID, `{"name":"Unsaved","origin":"https://new.example"}`)
	if w.Code != 500 {
		t.Fatalf("write failure: %d", w.Code)
	}
	w = request(s, "DELETE", "/api/workers/"+worker.ID, "")
	if w.Code != 500 {
		t.Fatalf("delete failure: %d", w.Code)
	}
	list := request(s, "GET", "/api/workers", "")
	if strings.Contains(list.Body.String(), "Unsaved") || !strings.Contains(list.Body.String(), "Original") {
		t.Fatal("failed mutation published")
	}
}

func TestWorkerCatalogBoundaryAndCapacity(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{`null`, `{} {}`, `{"name":"Bad\u0000name","origin":"https://worker.example"}`, `{"name":"Bad","origin":"https://worker.example?x=1"}`, `{"name":"Bad","origin":"https://worker.example#x"}`, strings.Repeat(" ", 4097)} {
		if w := request(s, "POST", "/api/workers", body); w.Code != 400 {
			t.Fatalf("bad catalog entry: %d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://"+s.host+"/api/workers", strings.NewReader(`{"name":"Cross-site","origin":"https://worker.example"}`))
	r.Header.Set("X-Gometer-Token", s.token)
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin registration accepted: %d", w.Code)
	}
	r = httptest.NewRequest("GET", "http://"+s.host+"/api/workers", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("unauthenticated catalog read: %d", w.Code)
	}
	for i := range maxSavedWorkers {
		saveWorker(t, s, fmt.Sprintf("Worker %d", i), fmt.Sprintf("https://worker%d.example", i))
	}
	if w := request(s, "POST", "/api/workers", `{"name":"Overflow","origin":"https://overflow.example"}`); w.Code != 409 {
		t.Fatalf("catalog capacity: %d", w.Code)
	}
	if w := request(s, "POST", "/api/workers/not-an-id/check", ""); w.Code != 404 {
		t.Fatalf("malformed ID: %d", w.Code)
	}
}
