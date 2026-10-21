package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(context.Background(), t.TempDir(), "127.0.0.1:8790", "http://127.0.0.1:8790", Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func request(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+s.host+path, strings.NewReader(body))
	r.Header.Set("X-Gometer-Token", s.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func awaitRun(t *testing.T, s *Server, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := request(s, "GET", "/api/runs/"+id, "")
		var run Run
		if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" && run.Status != "stopping" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Run{}
}
func startRun(t *testing.T, s *Server, body string) string {
	t.Helper()
	w := request(s, "POST", "/api/runs", body)
	if w.Code != 202 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var summary RunSummary
	json.Unmarshal(w.Body.Bytes(), &summary)
	return summary.ID
}

func TestBrowserBoundaryAndStrictValidation(t *testing.T) {
	s := testServer(t)
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	for _, tc := range []struct{ host, origin, token, site string }{
		{s.host, "", "", ""}, {"evil.test:8790", "", s.token, ""}, {s.host, "https://evil.test", s.token, ""}, {s.host, "", s.token, "cross-site"},
	} {
		r := httptest.NewRequest("POST", "http://"+s.host+"/api/runs", strings.NewReader(fmt.Sprintf(`{"url":%q}`, target.URL)))
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Gometer-Token", tc.token)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("boundary code %d", w.Code)
		}
	}
	for _, body := range []string{fmt.Sprintf(`{"url":%q,"typo":1}`, target.URL), `null`, fmt.Sprintf(`{"url":%q} {}`, target.URL), strings.Repeat(" ", config.MaxConfigBytes+1)} {
		w := request(s, "POST", "/api/runs", body)
		if w.Code != 400 {
			t.Fatalf("bad input: %d", w.Code)
		}
	}
	w := request(s, "POST", "/api/validate", fmt.Sprintf(`{"url":%q}`, target.URL))
	if w.Code != 200 {
		t.Fatalf("validate %d %s", w.Code, w.Body)
	}
	if requests.Load() != 0 {
		t.Fatal("validation or rejected input sent traffic")
	}
	for _, path := range []string{"/", "/app.js", "/style.css", "/theme.js", "/theme.css", "/fonts.css", "/gometer-mark.svg", "/favicon.svg", "/favicon.ico", "/apple-touch-icon.png", "/journey-model.js", "/journey-editor.js", "/journey.css", "/csv.js", "/csv.css", "/shortcuts.js", "/shortcuts.css", "/IBMPlexSans-Regular.woff2", "/IBMPlexMono-Regular.woff2", "/BricolageGrotesque-Latin.woff2", "/BricolageGrotesque-LatinExt.woff2", "/HankenGrotesk-Latin.woff2", "/HankenGrotesk-LatinExt.woff2"} {
		w = request(s, "GET", path, "")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset %s: %d %s", path, w.Code, w.Body)
		}
		if strings.HasSuffix(path, ".woff2") && w.Header().Get("Content-Type") != "font/woff2" {
			t.Fatalf("font %s content type: %s", path, w.Header().Get("Content-Type"))
		}
		for extension, kind := range map[string]string{".svg": "image/svg+xml", ".ico": "image/x-icon", ".png": "image/png"} {
			if strings.HasSuffix(path, extension) && w.Header().Get("Content-Type") != kind {
				t.Fatalf("brand asset %s content type: %s", path, w.Header().Get("Content-Type"))
			}
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing CSP")
		}
	}
	if w := request(s, "GET", "/api/runs/../../etc/passwd", ""); w.Code != 404 {
		t.Fatal("invalid run ID accepted")
	}
	for _, address := range []string{"0.0.0.0:8790", "localhost:8790", ":8790", "192.0.2.1:8790"} {
		ln, err := Listen(address, "")
		if err == nil {
			ln.Close()
			t.Fatalf("non-loopback listener allowed: %s", address)
		}
	}
}

func TestRemoteDashboardRequiresExactHTTPSOrigin(t *testing.T) {
	for _, origin := range []string{"", "http://gometer.example.test", "https://gometer.example.test/app", "https://user@gometer.example.test", "https://gometer.example.test?next=/"} {
		if ln, err := Listen("0.0.0.0:8790", origin); err == nil {
			ln.Close()
			t.Fatalf("remote listener accepted origin %q", origin)
		}
	}
	ln, err := Listen("0.0.0.0:0", "https://gometer.example.test")
	if err != nil {
		t.Fatalf("valid private HTTPS origin rejected: %v", err)
	}
	ln.Close()
}

func TestPublicOriginProtectsDashboardHostOriginAndDownloadCookie(t *testing.T) {
	const host = "gometer.example.test"
	s, err := New(context.Background(), t.TempDir(), host, "https://"+host, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	r := httptest.NewRequest("GET", "https://"+host+"/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("public origin request: %d %s", w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != "gometer-download" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/download/" {
		t.Fatalf("invalid dashboard download cookie: %+v", cookie)
	}

	for _, tc := range []struct{ host, origin string }{
		{"other.example.test", "https://" + host},
		{host, "https://other.example.test"},
	} {
		r := httptest.NewRequest("POST", "https://"+host+"/api/validate", strings.NewReader(`{}`))
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Gometer-Token", s.token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("accepted mismatched host/origin %q %q: %d", tc.host, tc.origin, w.Code)
		}
	}
}

func TestRunPersistsProgressAndReportsAndSurvivesReload(t *testing.T) {
	s := testServer(t)
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer target.Close()
	body := fmt.Sprintf(`{"name":"<script>test</script>","url":%q,"mode":"rate","rate":4,"duration":"1250ms","max_in_flight":2,"scenarios":[{"name":"Flow","steps":[{"url":"/one"},{"url":"/two"}]}]}`, target.URL)
	id := startRun(t, s, body)
	// A fresh browser request sees the active run. Run context is independent of
	// the POST request lifetime and only one generator can run at a time.
	if w := request(s, "POST", "/api/runs", body); w.Code != 409 {
		t.Fatal("concurrent run accepted")
	}
	state := request(s, "GET", "/api/state", "")
	if !strings.Contains(state.Body.String(), id) {
		t.Fatal("reload lost active run")
	}
	run := awaitRun(t, s, id)
	if run.Status != "passed" || run.Result == nil || run.Result.Journeys.Completed != 5 || run.Result.Completed != 10 || requests.Load() != 10 {
		t.Fatalf("wrong results: %+v requests=%d", run, requests.Load())
	}
	if len(run.Samples) < 3 || run.Samples[1].Phase == "" || run.Samples[1].ElapsedSeconds < .9 {
		t.Fatalf("missing live sample: %+v", run.Samples)
	}
	if run.Result.SchemaVersion != 3 || run.Result.Generator == nil || run.Result.HTTP == nil || run.Result.HTTP.FirstByte.Samples != 10 || run.Result.Scenarios[0].Steps[0].HTTP.FirstByte.Samples != 5 || run.Samples[1].Generator == nil {
		t.Fatal("diagnostics missing or using journey counts for HTTP samples")
	}
	bundle, _ := json.Marshal(run)
	imported := request(s, "POST", "/api/runs/import", string(bundle))
	if imported.Code != 201 {
		t.Fatalf("import %d %s", imported.Code, imported.Body)
	}
	var copySummary RunSummary
	json.Unmarshal(imported.Body.Bytes(), &copySummary)
	if copySummary.ID == id || requests.Load() != 10 {
		t.Fatal("import reused an ID or sent traffic")
	}
	copyRun, err := s.store.getRun(copySummary.ID)
	if err != nil || !copyRun.Imported || copyRun.Result.Completed != 10 || copyRun.Result.Generator == nil || copyRun.Result.HTTP.FirstByte.Samples != 10 || copyRun.Samples[1].Generator == nil {
		t.Fatal("import did not preserve measurements")
	}
	// Schema 2 bundles predate optional diagnostics. Decode separately so this
	// compatibility fixture cannot alter the original run through shared pointers.
	var legacy Run
	if err := json.Unmarshal(bundle, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Result.SchemaVersion = 2
	legacy.Result.Generator = nil
	legacy.Result.HTTP = nil
	for i := range legacy.Result.Phases {
		legacy.Result.Phases[i].HTTP = nil
	}
	for i := range legacy.Result.Scenarios {
		for j := range legacy.Result.Scenarios[i].Steps {
			legacy.Result.Scenarios[i].Steps[j].HTTP = nil
		}
	}
	for i := range legacy.Samples {
		legacy.Samples[i].Generator = nil
		legacy.Samples[i].Requests.HTTP = nil
	}
	legacy.Config.Diagnostics = nil
	legacyBundle, _ := json.Marshal(legacy)
	legacyResponse := request(s, "POST", "/api/runs/import", string(legacyBundle))
	if legacyResponse.Code != 201 || requests.Load() != 10 {
		t.Fatalf("schema 2 import: %d %s", legacyResponse.Code, legacyResponse.Body)
	}
	run.Result.Completed++
	invalid, _ := json.Marshal(run)
	if w := request(s, "POST", "/api/runs/import", string(invalid)); w.Code != 400 {
		t.Fatal("inconsistent imported counts accepted")
	}
	run.Result.Completed--
	for _, suffix := range []string{"report.json", "report.html", "export", "config"} {
		w := request(s, "GET", "/api/runs/"+id+"/"+suffix, "")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("download %s %d %s", suffix, w.Code, w.Body)
		}
		if suffix == "report.html" && strings.Contains(w.Body.String(), "<script>test</script>") {
			t.Fatal("report did not escape name")
		}
		if suffix == "report.html" && !strings.Contains(w.Header().Get("Content-Security-Policy"), "font-src data:") {
			t.Fatal("report CSP blocks its bundled fonts")
		}
	}
	root := request(s, "GET", "/", "")
	r := httptest.NewRequest("GET", "http://"+s.host+"/download/"+id+"/export", nil)
	for _, cookie := range root.Result().Cookies() {
		r.AddCookie(cookie)
	}
	wDownload := httptest.NewRecorder()
	s.ServeHTTP(wDownload, r)
	if wDownload.Code != 200 || !strings.Contains(wDownload.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("native download failed")
	}
	if w := request(s, "GET", "/download/"+id+"/export", ""); w.Code != 403 {
		t.Fatal("download without cookie accepted")
	}
	info, err := os.Stat(filepath.Join(s.store.dir, "runs", id+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
	reopened, err := New(context.Background(), s.store.dir, s.host, s.origin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	w := request(reopened, "GET", "/api/runs/"+id, "")
	var saved Run
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Result == nil || saved.Result.Completed != 10 || len(saved.Samples) != len(run.Samples) || saved.Result.Generator == nil || saved.Result.HTTP.FirstByte.Samples != 10 || saved.Samples[1].Generator == nil {
		t.Fatal("saved run did not survive restart")
	}
	if w := request(reopened, "DELETE", "/api/runs/"+id, ""); w.Code != 200 {
		t.Fatal("delete failed")
	}
	if w := request(reopened, "GET", "/api/runs/"+id, ""); w.Code != 404 {
		t.Fatal("deleted run remains")
	}
}

func TestStopAndShutdownSaveInterruptedResults(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			s := testServer(t)
			received := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case received <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			}))
			defer target.Close()
			id := startRun(t, s, fmt.Sprintf(`{"url":%q,"mode":"rate","rate":1,"duration":"5s"}`, target.URL))
			select {
			case <-received:
			case <-time.After(3 * time.Second):
				t.Fatal("request not received")
			}
			if w := request(s, "DELETE", "/api/runs/"+id, ""); w.Code != 409 {
				t.Fatal("active run deleted")
			}
			if shutdown {
				s.Close()
			} else {
				if w := request(s, "POST", "/api/runs/"+id+"/stop", ""); w.Code != 202 {
					t.Fatal("stop failed")
				}
			}
			run := awaitRun(t, s, id)
			if run.Status != "interrupted" || run.Result == nil || !run.Result.Interrupted || run.Result.Started != run.Result.Completed || run.Result.Failed != 1 {
				t.Fatalf("inconsistent partial results %+v", run)
			}
			var saved Run
			if err := readRecord(filepath.Join(s.store.dir, "runs", id+".json"), &saved); err != nil || saved.Status != "interrupted" {
				t.Fatalf("partial results not saved: %v", err)
			}
		})
	}
}

func TestScenarioRoundTripPreservesDisabledChecks(t *testing.T) {
	s := testServer(t)
	body := `{"name":"Saved","url":"http://127.0.0.1:8787","thresholds":{"max_error_percent":null,"max_drop_percent":null},"scenarios":[{"name":"Flow","steps":[{"url":"/"}]}],"journey_thresholds":{"max_error_percent":null}}`
	w := request(s, "POST", "/api/scenarios", body)
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	var saved SavedScenario
	json.Unmarshal(w.Body.Bytes(), &saved)
	data, _ := json.Marshal(saved.Config)
	reloaded, err := config.Load(bytes.NewReader(data))
	if err != nil || reloaded.Thresholds.MaxErrorPercent != nil || reloaded.Thresholds.MaxDropPercent != nil || reloaded.JourneyThresholds.MaxErrorPercent != nil {
		t.Fatalf("disabled checks changed: %s %v", data, err)
	}
	exported := request(s, "POST", "/api/config-export", body)
	if exported.Code != 200 {
		t.Fatalf("export %d %s", exported.Code, exported.Body)
	}
	var link map[string]string
	json.Unmarshal(exported.Body.Bytes(), &link)
	r := httptest.NewRequest("GET", "http://"+s.host+link["path"], nil)
	for _, cookie := range request(s, "GET", "/", "").Result().Cookies() {
		r.AddCookie(cookie)
	}
	download := httptest.NewRecorder()
	s.ServeHTTP(download, r)
	if download.Code != 200 {
		t.Fatalf("config download %d", download.Code)
	}
	exportedConfig, err := config.Load(download.Body)
	if err != nil || exportedConfig.Thresholds.MaxErrorPercent != nil || exportedConfig.JourneyThresholds.MaxErrorPercent != nil {
		t.Fatal("export altered disabled thresholds")
	}
	updated := strings.Replace(body, `"Saved"`, `"Updated"`, 1)
	if w := request(s, "PUT", "/api/scenarios/"+saved.ID, updated); w.Code != 200 {
		t.Fatal("update failed")
	}
	reopened, err := openStore(s.store.dir)
	if err != nil || reopened.scenarios[saved.ID].Name != "Updated" {
		t.Fatalf("library did not persist: %v", err)
	}
	if w := request(s, "DELETE", "/api/scenarios/"+saved.ID, ""); w.Code != 200 {
		t.Fatal("delete scenario failed")
	}
}

func TestRecoveryAndBoundedTimeline(t *testing.T) {
	s := testServer(t)
	id, _ := newID()
	run := Run{ID: id, Name: "Incomplete", Status: "running", CreatedAt: time.Now()}
	if err := s.store.write("runs", id, run); err != nil {
		t.Fatal(err)
	}
	for i := range maxSamples + 10 {
		appendSample(&run, Sample{ElapsedSeconds: float64(i)})
	}
	if len(run.Samples) != maxSamples || run.Samples[0].ElapsedSeconds != 10 {
		t.Fatal("timeline grew without a bound")
	}
	reopened, err := openStore(s.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.getRun(id)
	if err != nil || saved.Status != "abandoned" || saved.Result != nil {
		t.Fatalf("incomplete run recovery: %+v %v", saved, err)
	}
}

func TestUnwritableRunDirectoryFailsBeforeTraffic(t *testing.T) {
	s := testServer(t)
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer target.Close()
	path := filepath.Join(s.store.dir, "runs")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/runs", fmt.Sprintf(`{"url":%q}`, target.URL))
	if w.Code != 500 || attempts.Load() != 0 {
		t.Fatalf("code=%d attempts=%d", w.Code, attempts.Load())
	}
}
