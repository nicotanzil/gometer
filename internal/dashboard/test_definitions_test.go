package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func revisionRequest(s *Server, method, path, body, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+s.host+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gometer-Token", s.token)
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestDefinitionRevisionRaceCopyAndRestart(t *testing.T) {
	s := testServer(t)
	body := `{"name":"Shared definition","url":"http://127.0.0.1:1","mode":"rate","rate":1,"duration":"1s"}`
	created := request(s, "POST", "/api/scenarios", body)
	if created.Code != 200 {
		t.Fatalf("create: %s", created.Body)
	}
	var saved SavedScenario
	json.Unmarshal(created.Body.Bytes(), &saved)
	if saved.Revision != 1 || saved.CreatedAt == nil {
		t.Fatal("missing creation metadata")
	}
	path := "/api/scenarios/" + saved.ID
	if got := request(s, "PUT", path, body); got.Code != 428 {
		t.Fatalf("missing precondition: %d", got.Code)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- revisionRequest(s, "PUT", path, body, `"1"`).Code }()
	}
	wg.Wait()
	close(codes)
	successes, conflicts := 0, 0
	for code := range codes {
		switch code {
		case 200:
			successes++
		case 409:
			conflicts++
		default:
			t.Fatalf("race code: %d", code)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("race: %d successes, %d conflicts", successes, conflicts)
	}
	if got := revisionRequest(s, "POST", path+"/copy", "", `"1"`); got.Code != 409 {
		t.Fatal("stale copy basis accepted")
	}
	copied := revisionRequest(s, "POST", path+"/copy", "", `"2"`)
	if copied.Code != 201 {
		t.Fatalf("copy: %d %s", copied.Code, copied.Body)
	}
	var copy SavedScenario
	json.Unmarshal(copied.Body.Bytes(), &copy)
	if copy.ID == saved.ID || copy.Revision != 1 || copy.Source == nil || copy.Source.ID != saved.ID || copy.Source.Revision != 2 {
		t.Fatalf("copy provenance: %+v", copy)
	}
	changed := strings.Replace(body, "Shared definition", "Independent copy", 1)
	if got := revisionRequest(s, "PUT", "/api/scenarios/"+copy.ID, changed, `"1"`); got.Code != 200 {
		t.Fatalf("copy update: %s", got.Body)
	}
	s.Close()
	reopened, err := New(context.Background(), s.store.dir, s.host, s.origin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	original := request(reopened, "GET", path, "")
	json.Unmarshal(original.Body.Bytes(), &saved)
	if saved.Name != "Shared definition" || saved.Revision != 2 || original.Header().Get("ETag") != `"2"` {
		t.Fatal("original modified or metadata lost")
	}
	result := request(reopened, "GET", "/api/scenarios/"+copy.ID, "")
	json.Unmarshal(result.Body.Bytes(), &copy)
	if copy.Name != "Independent copy" || copy.Source.Revision != 2 || copy.Revision != 2 {
		t.Fatal("copy metadata lost on restart")
	}
	if len(reopened.store.runs) != 0 {
		t.Fatal("definition mutations created runs")
	}
}

func TestLegacyDefinitionAndFailedWriteRetainRevision(t *testing.T) {
	s := testServer(t)
	body := `{"name":"Legacy","url":"http://127.0.0.1:1"}`
	created := request(s, "POST", "/api/scenarios", body)
	var saved SavedScenario
	json.Unmarshal(created.Body.Bytes(), &saved)
	deleteLegacy := map[string]any{}
	json.Unmarshal(created.Body.Bytes(), &deleteLegacy)
	delete(deleteLegacy, "revision")
	delete(deleteLegacy, "created_at")
	legacy, _ := json.Marshal(deleteLegacy)
	file := filepath.Join(s.store.dir, "scenarios", saved.ID+".json")
	if err := os.WriteFile(file, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err := New(context.Background(), s.store.dir, s.host, s.origin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := "/api/scenarios/" + saved.ID
	got := request(s, "GET", path, "")
	if got.Header().Get("ETag") != `"1"` {
		t.Fatal("legacy revision unavailable")
	}
	dir := filepath.Join(s.store.dir, "scenarios")
	os.Rename(dir, dir+"-offline")
	os.WriteFile(dir, []byte("blocked"), 0600)
	if got := revisionRequest(s, "PUT", path, body, `"1"`); got.Code != 500 {
		t.Fatal("failed persistence returned success")
	}
	if got := request(s, "GET", path, ""); got.Header().Get("ETag") != `"1"` {
		t.Fatal("failed write advanced revision")
	}
}

func TestDefinitionPreviewsOmitSensitiveConfiguration(t *testing.T) {
	s := testServer(t)
	body := `{"name":"Preview test","url":"http://127.0.0.1:1/PRIVATE?secret=QUERYTOKEN","method":"POST","headers":{"Authorization":"HEADERSECRET"},"body":"BODYSECRET"}`
	created := request(s, "POST", "/api/scenarios", body)
	if created.Code != 200 {
		t.Fatalf("create: %s", created.Body)
	}
	var saved SavedScenario
	json.Unmarshal(created.Body.Bytes(), &saved)
	for _, path := range []string{"/api/tests", "/api/tests/" + saved.ID} {
		w := request(s, "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("preview: %s", w.Body)
		}
		for _, secret := range []string{"PRIVATE", "QUERYTOKEN", "HEADERSECRET", "BODYSECRET", "Authorization"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("preview exposed sensitive field: %s", secret)
			}
		}
		if !strings.Contains(w.Body.String(), "http://127.0.0.1:1") {
			t.Fatal("origin missing")
		}
	}
}
