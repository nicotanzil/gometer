package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gometer/examples"
	"gometer/internal/jmx"
)

func jmxSource(t *testing.T) string {
	t.Helper()
	b, err := examples.Files.ReadFile("jmx/http-smoke.jmx")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func jmxBody(t *testing.T, source string, accept bool) string {
	t.Helper()
	b, err := json.Marshal(migrationRequest{MigrationSource: MigrationSource{Source: source, Filename: "example.jmx", Options: jmx.Options{AcceptRuntime: accept}}, Name: "Migrated test"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestJMXGuidedImportPersistenceAndNoTraffic(t *testing.T) {
	s := testServer(t)
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	src := strings.Replace(jmxSource(t), "/work?item=${item}", target.URL+"/work", 1)
	// Analysis may find a cross-origin blocker, but must never visit the target.
	w := request(s, "POST", "/api/jmx/analyze", jmxBody(t, src, true))
	if w.Code != 200 {
		t.Fatalf("analyze: %d %s", w.Code, w.Body)
	}
	blocked := strings.Replace(jmxSource(t), "startnextloop", "unknown", 1)
	w = request(s, "POST", "/api/jmx/convert", jmxBody(t, blocked, true))
	if w.Code != 409 {
		t.Fatalf("blocked conversion: %d %s", w.Code, w.Body)
	}
	w = request(s, "POST", "/api/jmx/migrations", jmxBody(t, blocked, true))
	if w.Code != 200 {
		t.Fatalf("draft: %d %s", w.Code, w.Body)
	}
	var draft MigrationDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Source != blocked || len(s.store.scenarios) != 0 {
		t.Fatal("draft lost its source or entered executable library")
	}
	if w = request(s, "POST", "/api/runs", w.Body.String()); w.Code != 400 {
		t.Fatalf("draft was accepted as a run: %d", w.Code)
	}
	w = request(s, "POST", "/api/jmx/convert", jmxBody(t, jmxSource(t), false))
	if w.Code != 409 {
		t.Fatal("runtime review bypassed")
	}
	w = request(s, "POST", "/api/jmx/convert", jmxBody(t, jmxSource(t), true))
	if w.Code != 201 {
		t.Fatalf("convert: %d %s", w.Code, w.Body)
	}
	var saved SavedScenario
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Migration == nil || saved.Migration.Source != jmxSource(t) || saved.Config.Name != "Migrated test" || saved.Config.Cookies == nil || *saved.Config.Cookies {
		t.Fatal("saved source/settings mismatch")
	}
	for _, kindID := range []struct{ kind, id string }{{"scenarios", saved.ID}, {"migrations", draft.ID}} {
		info, err := os.Stat(filepath.Join(s.store.dir, kindID.kind, kindID.id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("source record permissions: %o", info.Mode().Perm())
		}
	}
	// Subsequent builder edits retain the original source without claiming that
	// the edited configuration is still equivalent to the JMX.
	saved.Config.Name = "Edited after import"
	b, _ := json.Marshal(saved.Config)
	w = request(s, "PUT", "/api/scenarios/"+saved.ID, string(b))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	reloaded, err := New(context.Background(), s.store.dir, s.host, s.origin)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	w = request(reloaded, "GET", "/api/jmx/migrations/"+draft.ID, "")
	if w.Code != 200 {
		t.Fatalf("draft reload: %d %s", w.Code, w.Body)
	}
	w = request(reloaded, "GET", "/api/scenarios/"+saved.ID, "")
	var reopened SavedScenario
	json.Unmarshal(w.Body.Bytes(), &reopened)
	if reopened.Config.Name != "Edited after import" || reopened.Migration == nil || reopened.Migration.Source != jmxSource(t) {
		t.Fatal("source provenance lost after edit/restart")
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("import generated %d target requests", targetCalls.Load())
	}
}
func TestJMXImportStrictInputAndAuthorization(t *testing.T) {
	s := testServer(t)
	valid := jmxBody(t, jmxSource(t), true)
	for _, body := range []string{`null`, `{}`, valid + ` {}`, strings.Replace(valid, `"name":"Migrated test"`, `"config":{},"name":"Migrated test"`, 1), jmxBody(t, `<!DOCTYPE test>`+jmxSource(t), true), jmxBody(t, strings.Repeat(" ", jmx.MaxSourceBytes+1), true)} {
		w := request(s, "POST", "/api/jmx/analyze", body)
		if w.Code != 400 {
			t.Fatalf("bad input: %d %s", w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "http://"+s.host+"/api/jmx/convert", strings.NewReader(valid))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || len(s.store.scenarios) != 0 {
		t.Fatal("JMX endpoint bypassed dashboard token")
	}
	w = request(s, "PUT", "/api/jmx/migrations/"+strings.Repeat("a", 32), valid)
	if w.Code != 404 {
		t.Fatal("upserted nonexistent draft")
	}
	w = request(s, "POST", "/api/jmx/migrations", valid)
	var draft MigrationDraft
	json.Unmarshal(w.Body.Bytes(), &draft)
	w = request(s, "GET", "/api/state", "")
	if !strings.Contains(w.Body.String(), `"migrations":[{`) {
		t.Fatal("draft missing from workspace state")
	}
	w = request(s, "DELETE", "/api/jmx/migrations/"+draft.ID, "")
	if w.Code != 200 || len(s.store.migrations) != 0 {
		t.Fatal("draft delete failed")
	}
	w = request(s, "GET", "/api/jmx/example", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "jmeterTestPlan") {
		t.Fatal("embedded sample unavailable")
	}
	for _, path := range []string{"/jmx.js", "/jmx.css"} {
		w = request(s, "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("asset %s: %d", path, w.Code)
		}
	}
}
