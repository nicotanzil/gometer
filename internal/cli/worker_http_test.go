package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerRemoteHTTPStartupRequiresExplicitOperatorOptIn(t *testing.T) {
	t.Setenv("GOMETER_ALLOW_HTTP_WORKERS", "true") // Coordinator permission cannot grant listener permission.
	for _, tc := range []struct {
		name, environment, token string
		args                     []string
		wantCode                 int
		warning                  bool
	}{
		{"default rejection", "", "worker-http-test-token-01234567890123456789", nil, 2, false},
		{"environment opt-in", "true", "worker-http-test-token-01234567890123456789", nil, 0, true},
		{"explicit opt-in", "", "worker-http-test-token-01234567890123456789", []string{"-allow-http"}, 0, true},
		{"flag disables environment opt-in", "true", "worker-http-test-token-01234567890123456789", []string{"-allow-http=false"}, 2, false},
		{"flag overrides malformed environment", "invalid", "worker-http-test-token-01234567890123456789", []string{"-allow-http"}, 0, true},
		{"invalid environment rejected", "invalid", "worker-http-test-token-01234567890123456789", nil, 2, false},
		{"opt-in still requires token", "true", "", nil, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMETER_WORKER_LISTEN", "0.0.0.0:0")
			t.Setenv("GOMETER_WORKER_TOKEN", tc.token)
			if tc.environment != "" {
				t.Setenv("GOMETER_WORKER_ALLOW_HTTP", tc.environment)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var out bytes.Buffer
			code := Run(ctx, append([]string{"worker"}, tc.args...), &out, &out)
			if code != tc.wantCode {
				t.Fatalf("exit=%d want=%d output=%s", code, tc.wantCode, &out)
			}
			warned := strings.Contains(out.String(), "without application TLS")
			if warned != tc.warning {
				t.Fatalf("plaintext warning=%v want=%v: %s", warned, tc.warning, &out)
			}
			if tc.token != "" && strings.Contains(out.String(), tc.token) {
				t.Fatal("startup output disclosed token")
			}
		})
	}
}

func TestCoordinatorRemoteHTTPRequiresIndependentOptIn(t *testing.T) {
	const token = "worker-http-test-token-01234567890123456789"
	t.Setenv("GOMETER_WORKER_TOKEN", token)
	t.Setenv("GOMETER_WORKER_ALLOW_HTTP", "true")
	lines, _ := serveCLI(t, "worker", "-listen", "0.0.0.0:0", "-name", "HTTP fixture")
	address := startupLine(t, lines, "GoMeter worker HTTP fixture: ")
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	// localhost reaches our fixture but is not a literal loopback IP, so requires remote opt-in.
	workerURL := "http://localhost:" + parsed.Port()
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	for _, tc := range []struct {
		name, environment string
		args              []string
		wantCode          int
		warning           bool
		wrongToken        bool
	}{
		{"default rejects opted-in worker", "", nil, 2, false, false},
		{"environment permits HTTP", "true", nil, 0, true, false},
		{"flag permits HTTP", "", []string{"-allow-http-workers"}, 0, true, false},
		{"flag disables environment", "true", []string{"-allow-http-workers=false"}, 2, false, false},
		{"flag overrides invalid environment", "invalid", []string{"-allow-http-workers"}, 0, true, false},
		{"invalid environment rejected", "invalid", nil, 2, false, false},
		{"opt-in cannot bypass authentication", "true", nil, 2, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMETER_ALLOW_HTTP_WORKERS", tc.environment)
			if tc.environment == "" {
				t.Setenv("GOMETER_ALLOW_HTTP_WORKERS", "false")
			}
			if tc.wrongToken {
				t.Setenv("GOMETER_WORKER_TOKEN", "wrong-http-test-token-01234567890123456789")
			}
			before := attempts.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out bytes.Buffer
			args := []string{"run", "-url", target.URL, "-rate", "4", "-duration", "250ms", "-max-in-flight", "2", "-workers", workerURL, "-quiet", "-out", "", "-html", ""}
			code := Run(ctx, append(args, tc.args...), &out, &out)
			if code != tc.wantCode {
				t.Fatalf("exit=%d want=%d output=%s", code, tc.wantCode, &out)
			}
			wantAttempts := int64(0)
			if tc.wantCode == 0 {
				wantAttempts = 1
			}
			if attempts.Load()-before != wantAttempts {
				t.Fatalf("target received %d requests, want %d: %s", attempts.Load()-before, wantAttempts, &out)
			}
			if strings.Contains(out.String(), "without application TLS") != tc.warning {
				t.Fatalf("incorrect plaintext warning: %s", &out)
			}
			if strings.Contains(out.String(), token) {
				t.Fatal("coordinator output disclosed token")
			}
		})
	}
}

func TestDashboardHTTPStartupPermissionIsRuntimeOnly(t *testing.T) {
	const token = "dashboard-http-test-token-01234567890123456789"
	t.Setenv("GOMETER_WORKER_TOKEN", token)
	lines, _ := serveCLI(t, "worker", "-listen", "0.0.0.0:0", "-name", "HTTP fixture", "-allow-http")
	address, _ := url.Parse(startupLine(t, lines, "GoMeter worker HTTP fixture: "))
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); fmt.Fprint(w, "ok") }))
	defer target.Close()
	body := fmt.Sprintf(`{"name":"Stored HTTP preset","url":%q,"mode":"rate","duration":"250ms","rate":4,"max_in_flight":2,"workers":[%q]}`, target.URL, "http://localhost:"+address.Port())
	workspace := filepath.Join(t.TempDir(), "dashboard")
	t.Setenv("GOMETER_UI_LISTEN", "127.0.0.1:0")
	t.Setenv("GOMETER_UI_DATA", workspace)
	client := &http.Client{Timeout: 3 * time.Second}
	var completedBundle []byte
	for _, tc := range []struct {
		name, environment string
		args              []string
		allowed, execute  bool
	}{
		{"environment grants permission", "true", nil, true, true},
		{"restart does not inherit preset permission", "false", nil, false, false},
		{"explicit false wins", "true", []string{"-allow-http-workers=false"}, false, false},
		{"explicit true overrides invalid environment", "invalid", []string{"-allow-http-workers"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMETER_ALLOW_HTTP_WORKERS", tc.environment)
			lines, stop := serveCLI(t, append([]string{"ui"}, tc.args...)...)
			defer stop()
			if tc.allowed {
				warning := startupLine(t, lines, "WARNING: ")
				if !strings.Contains(warning, "without application TLS") {
					t.Fatal("startup omitted plaintext warning")
				}
			}
			base := startupLine(t, lines, "GoMeter dashboard: ")
			response, err := client.Get(base)
			if err != nil {
				t.Fatal(err)
			}
			html, _ := io.ReadAll(response.Body)
			response.Body.Close()
			match := regexp.MustCompile(`<meta name="gometer-token" content="([^"]+)"`).FindSubmatch(html)
			if len(match) != 2 {
				t.Fatalf("dashboard did not render token: %s", html)
			}
			if strings.Contains(string(html), `id="http-worker-permission"`) != tc.allowed {
				t.Fatal("dashboard permission warning disagrees with startup")
			}
			if strings.Contains(string(html), token) {
				t.Fatal("dashboard disclosed worker token")
			}
			call := func(method, path, payload string) (int, []byte) {
				t.Helper()
				req, _ := http.NewRequest(method, base+path, strings.NewReader(payload))
				req.Header.Set("X-Gometer-Token", string(match[1]))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", base)
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				data, _ := io.ReadAll(res.Body)
				return res.StatusCode, data
			}
			status, data := call("POST", "/api/scenarios", body)
			if status != 200 {
				t.Fatalf("save: %d %s", status, data)
			}
			status, data = call("POST", "/api/validate", body)
			want := 400
			if tc.allowed {
				want = 200
			}
			if status != want {
				t.Fatalf("validation: %d %s", status, data)
			}
			status, data = call("GET", "/api/state", "")
			var state struct {
				Allow     bool              `json:"allow_http_workers"`
				Scenarios []json.RawMessage `json:"scenarios"`
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if status != 200 || state.Allow != tc.allowed || len(state.Scenarios) == 0 {
				t.Fatalf("runtime capabilities/persistence: %s", data)
			}
			if !tc.allowed {
				if len(completedBundle) > 0 {
					before := attempts.Load()
					status, data = call("POST", "/api/runs/import", string(completedBundle))
					if status != 201 || attempts.Load() != before {
						t.Fatalf("import changed permission or sent traffic: %d %s", status, data)
					}
				}
				if status, data = call("POST", "/api/runs", body); status != 400 {
					t.Fatalf("execution bypassed permission: %d %s", status, data)
				}
			}
			if tc.execute {
				before := attempts.Load()
				status, data = call("POST", "/api/runs", body)
				if status != 202 {
					t.Fatalf("start: %d %s", status, data)
				}
				var run struct{ ID, Status string }
				if err := json.Unmarshal(data, &run); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					_, data = call("GET", "/api/runs/"+run.ID, "")
					if err := json.Unmarshal(data, &run); err != nil {
						t.Fatal(err)
					}
					if run.Status != "running" && run.Status != "stopping" {
						break
					}
					time.Sleep(25 * time.Millisecond)
				}
				completedBundle = append([]byte(nil), data...)
				if run.Status != "passed" || attempts.Load()-before != 1 {
					t.Fatalf("authenticated dashboard run: %s, attempts=%d", data, attempts.Load()-before)
				}
			}
		})
	}
}

func TestValidateHTTPWorkerEnvironmentAndFlagPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.json")
	if err := os.WriteFile(path, []byte(`{"url":"http://127.0.0.1:1","mode":"rate","rate":1,"workers":["http://localhost:8801"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		environment string
		args        []string
		code        int
	}{
		{"false", nil, 2}, {"true", nil, 0}, {"true", []string{"-allow-http-workers=false"}, 2}, {"invalid", []string{"-allow-http-workers"}, 0}, {"invalid", nil, 2}, {"", nil, 2},
	} {
		t.Setenv("GOMETER_ALLOW_HTTP_WORKERS", tc.environment)
		var out bytes.Buffer
		code := Run(context.Background(), append([]string{"validate", "-config", path}, tc.args...), &out, &out)
		if code != tc.code {
			t.Fatalf("validate environment=%q exit=%d want=%d: %s", tc.environment, code, tc.code, &out)
		}
	}
}

func TestWorkerHTTPOptInDoesNotMaskInvalidTLS(t *testing.T) {
	t.Setenv("GOMETER_WORKER_ALLOW_HTTP", "true")
	t.Setenv("GOMETER_WORKER_TOKEN", "worker-http-test-token-01234567890123456789")
	for _, args := range [][]string{
		{"-tls-cert", "missing.crt"}, {"-tls-key", "missing.key"}, {"-tls-cert", "missing.crt", "-tls-key", "missing.key"},
	} {
		var out bytes.Buffer
		code := Run(context.Background(), append([]string{"worker", "-listen", "0.0.0.0:0"}, args...), &out, &out)
		if code != 2 || !strings.Contains(out.String(), "TLS") {
			t.Fatalf("invalid TLS fell back to HTTP: exit=%d %s", code, &out)
		}
	}
}

func TestHTTPStartupRejectsMalformedBooleanSettings(t *testing.T) {
	for _, tc := range []struct{ command, variable string }{
		{"worker", "GOMETER_WORKER_ALLOW_HTTP"}, {"run", "GOMETER_ALLOW_HTTP_WORKERS"}, {"validate", "GOMETER_ALLOW_HTTP_WORKERS"}, {"ui", "GOMETER_ALLOW_HTTP_WORKERS"},
	} {
		for _, value := range []string{"", "invalid-secret-value"} {
			t.Run(tc.command+value, func(t *testing.T) {
				t.Setenv(tc.variable, value)
				var out bytes.Buffer
				code := Run(context.Background(), []string{tc.command}, &out, &out)
				if code != 2 || !strings.Contains(out.String(), tc.variable) || strings.Contains(out.String(), "invalid-secret-value") {
					t.Fatalf("malformed boolean: exit=%d %s", code, &out)
				}
			})
		}
	}
}
