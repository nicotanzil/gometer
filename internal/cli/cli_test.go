package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gometer/internal/distributed"
	"gometer/internal/engine"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIProducesReportsAndUsesFailureExitCode(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "unavailable", 503) }))
	defer s.Close()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"run", "-url", s.URL, "-rate", "1", "-duration", "20ms", "-quiet", "-out", filepath.Join(dir, "run.json"), "-html", filepath.Join(dir, "run.html")}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "FAIL") {
		t.Fatalf("code %d, stdout %s, stderr %s", code, &stdout, &stderr)
	}
	for _, name := range []string{"run.json", "run.html"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Fatalf("missing report %s: %v", name, err)
		}
	}
}

func TestUIRequiresHTTPSOriginForRemoteListener(t *testing.T) {
	for _, origin := range []string{"", "http://gometer.example.test"} {
		args := []string{"ui", "-listen", "0.0.0.0:8790"}
		if origin != "" {
			args = append(args, "-public-origin", origin)
		}
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output); code != 2 || !strings.Contains(output.String(), "-public-origin") {
			t.Fatalf("remote dashboard configuration accepted origin %q: code=%d output=%s", origin, code, output.String())
		}
	}
}

func TestCLIFiniteIterations(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	for _, duration := range []string{"", "1h"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "finite.json")
		args := []string{"run", "-url", s.URL, "-vus", "10", "-iterations", "5", "-quiet", "-out", path, "-html", filepath.Join(dir, "finite.html")}
		if duration != "" {
			args = append(args, "-duration", duration)
		}
		var out bytes.Buffer
		before := attempts.Load()
		if code := Run(context.Background(), args, &out, &out); code != 0 {
			t.Fatalf("CLI code=%d: %s", code, &out)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var r engine.Result
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		if attempts.Load()-before != 50 || r.Iterations != 5 || r.StopReason != "iterations" || (r.PlannedLoadSeconds > 0) != (duration != "") {
			t.Fatalf("CLI iteration flags: %+v", r)
		}
	}
}

func TestCLIUserRampAndStartupReports(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "ramp.json")
	html := filepath.Join(dir, "ramp.html")
	var out bytes.Buffer
	code := Run(context.Background(), []string{"run", "-url", s.URL, "-vus", "3", "-iterations", "2", "-ramp-up", "150ms", "-startup-delay", "30ms", "-quiet", "-out", path, "-html", html}, &out, &out)
	if code != 0 {
		t.Fatalf("CLI=%d: %s", code, &out)
	}
	data, _ := os.ReadFile(path)
	var r engine.Result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(html)
	if attempts.Load() != 6 || r.SchemaVersion != 8 || r.UserSchedule.StartupSeconds != .03 || r.UserSchedule.Users.Started != 3 || !strings.Contains(out.String(), "Startup wait:") || !strings.Contains(string(page), "startup wait / ") {
		t.Fatalf("schedule artifacts: %+v %s", r, &out)
	}
}

func TestCLIFailureActionReports(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 503) }))
	defer s.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "failure.json")
	html := filepath.Join(dir, "failure.html")
	var out bytes.Buffer
	code := Run(context.Background(), []string{"run", "-url", s.URL, "-vus", "2", "-iterations", "3", "-on-error", "stop_user", "-quiet", "-out", path, "-html", html}, &out, &out)
	data, _ := os.ReadFile(path)
	var r engine.Result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(html)
	if code != 1 || r.Completed != 2 || r.StopReason != "users_stopped" || r.FailurePolicy.UsersStopped != 2 || !strings.Contains(out.String(), "2 users stopped on error") || !strings.Contains(string(page), "Failure action: Stop this user") || !strings.Contains(string(page), "users stopped after errors") {
		t.Fatalf("failure artifacts: code=%d %+v %s", code, r, &out)
	}
}

func TestCLITestStopReports(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 503) }))
	defer s.Close()
	for _, action := range []string{"stop_test", "stop_test_now"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "stop.json")
		html := filepath.Join(dir, "stop.html")
		var out bytes.Buffer
		code := Run(context.Background(), []string{"run", "-url", s.URL, "-vus", "4", "-iterations", "3", "-ramp-up", "1h", "-on-error", action, "-quiet", "-out", path, "-html", html}, &out, &out)
		data, _ := os.ReadFile(path)
		var r engine.Result
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		page, _ := os.ReadFile(html)
		if code != 1 || r.Interrupted || r.SchemaVersion != 10 || r.Completed != 1 || !r.FailurePolicy.TestStopped || !strings.Contains(out.String(), "Test stopped on error:") || !strings.Contains(string(page), "Error-triggered test stop: yes") || !strings.Contains(string(page), "test stopped") {
			t.Fatalf("test-stop reports: code=%d %+v %s", code, r, &out)
		}
	}
}

func TestCLIDistributedReportsAndTokenHandling(t *testing.T) {
	token := "cli-test-worker-token-012345678901234567890123456789"
	t.Setenv("GOMETER_WORKER_TOKEN", token)
	worker, err := distributed.NewWorker(context.Background(), "CLI worker", token)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	w := httptest.NewServer(worker)
	defer w.Close()
	target := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) { fmt.Fprint(out, "ok") }))
	defer target.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.json")
	var output bytes.Buffer
	code := Run(context.Background(), []string{"run", "-url", target.URL, "-rate", "1", "-duration", "50ms", "-max-in-flight", "1", "-workers", w.URL, "-quiet", "-out", path, "-html", filepath.Join(dir, "fleet.html")}, &output, &output)
	if code != 0 || strings.Contains(output.String(), token) {
		t.Fatalf("distributed CLI: %d %s", code, &output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result engine.Result
	if err := json.Unmarshal(data, &result); err != nil || result.SchemaVersion != 4 || !result.Distribution.Complete || result.Completed != 1 {
		t.Fatalf("distributed report: %s %v", data, err)
	}
	t.Setenv("GOMETER_WORKER_TOKEN", "")
	output.Reset()
	if code := Run(context.Background(), []string{"worker", "-listen", "127.0.0.1:0"}, &output, &output); code != 2 || !strings.Contains(output.String(), "GOMETER_WORKER_TOKEN") {
		t.Fatal("worker started without required credentials")
	}
}

func TestInvalidOutputIsRejectedBeforeTraffic(t *testing.T) {
	var requests atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); fmt.Fprint(w, "ok") }))
	defer s.Close()
	var output bytes.Buffer
	code := Run(context.Background(), []string{"run", "-url", s.URL, "-duration", "20ms", "-out", t.TempDir(), "-html", ""}, &output, &output)
	if code != 2 || requests.Load() != 0 {
		t.Fatalf("code=%d, requests=%d, output=%s", code, requests.Load(), &output)
	}
}

func TestValidateDoesNotSendTraffic(t *testing.T) {
	var requests atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"url":%q}`, s.URL)), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"validate", "-config", path}, &output, &output); code != 0 || requests.Load() != 0 {
		t.Fatalf("code=%d, requests=%d, output=%s", code, requests.Load(), &output)
	}
}
