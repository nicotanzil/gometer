package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"gometer/internal/engine"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// CLI tests use an isolated application environment, not a developer's deployment.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GOMETER_") {
			if err := os.Unsetenv(name); err != nil {
				panic(err)
			}
		}
	}
	os.Exit(m.Run())
}

func TestEnvironmentConfigFileCanBeValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, []byte(`{"name":"Environment preset","url":"http://127.0.0.1:1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMETER_CONFIG", path)
	var out bytes.Buffer
	code := Run(context.Background(), []string{"validate"}, &out, &out)
	if code != 0 || !strings.Contains(out.String(), "Valid: Environment preset") {
		t.Fatalf("environment config: exit=%d output=%s", code, &out)
	}
}

func TestEnvironmentConfigFileRunAndPrecedence(t *testing.T) {
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
	}))
	defer target.Close()
	dir := t.TempDir()
	environmentPath := filepath.Join(dir, "environment.json")
	flagPath := filepath.Join(dir, "flag.json")
	for path, name := range map[string]string{environmentPath: "Environment preset", flagPath: "Flag preset"} {
		body := fmt.Sprintf(`{"name":%q,"url":%q,"vus":1,"iterations":2,"duration":""}`, name, target.URL)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, environment, wantName string
		args                        []string
		wantRequests                int64
	}{
		{"environment", environmentPath, "Environment preset", nil, 2},
		{"flag overrides environment", environmentPath, "Flag preset", []string{"-config", flagPath}, 2},
		{"flag overrides invalid environment", filepath.Join(dir, "missing.json"), "Flag preset", []string{"-config", flagPath}, 2},
		{"explicit empty clears environment", filepath.Join(dir, "missing.json"), "HTTP test", []string{"-config", "", "-url", target.URL, "-vus", "1", "-iterations", "1"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMETER_CONFIG", tc.environment)
			path := filepath.Join(t.TempDir(), "result.json")
			args := append([]string{"run", "-quiet", "-out", path, "-html", ""}, tc.args...)
			before := attempts.Load()
			var out bytes.Buffer
			code := Run(context.Background(), args, &out, &out)
			if code != 0 {
				t.Fatalf("run: exit=%d output=%s", code, &out)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var result engine.Result
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if result.Name != tc.wantName || result.Completed != tc.wantRequests || attempts.Load()-before != tc.wantRequests || !result.Passed {
				t.Fatalf("unexpected selected workload: name=%q completed=%d observed=%d", result.Name, result.Completed, attempts.Load()-before)
			}
		})
	}
}

// Start the public CLI and observe its startup output; no server internals are mocked.
func serveCLI(t *testing.T, args ...string) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	lines := make(chan string, 32)
	finished := make(chan int, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	go func() {
		code := Run(ctx, args, writer, writer)
		writer.Close()
		finished <- code
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			defer reader.Close()
			select {
			case code := <-finished:
				if code != 0 {
					t.Errorf("service exited with code %d", code)
				}
			case <-time.After(10 * time.Second):
				t.Error("service did not shut down")
			}
		})
	}
	t.Cleanup(stop)
	return lines, stop
}

func startupLine(t *testing.T, lines <-chan string, prefix string) string {
	t.Helper()
	var observed []string
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("service ended before %q: %s", prefix, strings.Join(observed, "\n"))
			}
			observed = append(observed, line)
			if strings.HasPrefix(line, prefix) {
				return strings.TrimPrefix(line, prefix)
			}
		case <-timer.C:
			t.Fatalf("service did not announce %q: %s", prefix, strings.Join(observed, "\n"))
		}
	}
}

func TestEnvironmentDashboardStartsAndReopensWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	workspace := filepath.Join(dir, "environment-workspace")
	t.Setenv("GOMETER_UI_LISTEN", "127.0.0.1:0")
	t.Setenv("GOMETER_UI_DATA", workspace)
	t.Setenv("GOMETER_UI_PUBLIC_ORIGIN", "")
	lines, stop := serveCLI(t, "ui")
	url := startupLine(t, lines, "GoMeter dashboard: ")
	if data := startupLine(t, lines, "Saved scenarios and runs: "); data != workspace {
		t.Fatalf("workspace=%q, want %q", data, workspace)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	load := func(url string) string {
		t.Helper()
		response, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		match := regexp.MustCompile(`<meta name="gometer-token" content="([^"]+)"`).FindSubmatch(body)
		if response.StatusCode != 200 || len(match) != 2 {
			t.Fatalf("dashboard response: %d", response.StatusCode)
		}
		return string(match[1])
	}
	token := load(url)
	request, _ := http.NewRequest("POST", url+"/api/scenarios", strings.NewReader(`{"name":"Environment workspace preset","url":"http://127.0.0.1:1"}`))
	request.Header.Set("Origin", url)
	request.Header.Set("X-Gometer-Token", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("save preset: %d", response.StatusCode)
	}
	stop()
	lines, _ = serveCLI(t, "ui")
	url = startupLine(t, lines, "GoMeter dashboard: ")
	token = load(url)
	request, _ = http.NewRequest("GET", url+"/api/state", nil)
	request.Header.Set("X-Gometer-Token", token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state struct {
		Scenarios []struct {
			Name string `json:"name"`
		} `json:"scenarios"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Scenarios) != 1 || state.Scenarios[0].Name != "Environment workspace preset" {
		t.Fatalf("saved preset did not survive environment-configured restart: %+v", state)
	}
}

func TestEnvironmentDashboardOriginValidation(t *testing.T) {
	for _, tc := range []struct {
		name, listen, origin, data, want string
		code                             int
	}{
		{"loopback rejects public origin", "127.0.0.1:0", "https://gometer.example.test", "workspace", "only valid with a non-loopback", 2},
		{"remote requires origin", "0.0.0.0:0", "", "workspace", "non-loopback dashboard listeners require", 2},
		{"remote requires HTTPS", "0.0.0.0:0", "http://gometer.example.test", "workspace", "must be an HTTPS origin", 2},
		{"trusted private ingress", "0.0.0.0:0", "https://gometer.example.test", "workspace", "GoMeter dashboard: https://gometer.example.test", 0},
		{"empty data is explicit", "127.0.0.1:0", "", "", "usage: gometer ui", 2},
		{"empty listen is explicit", "", "", "workspace", "listen address must include", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("GOMETER_UI_LISTEN", tc.listen)
			t.Setenv("GOMETER_UI_PUBLIC_ORIGIN", tc.origin)
			t.Setenv("GOMETER_UI_DATA", tc.data)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var out bytes.Buffer
			code := Run(ctx, []string{"ui"}, &out, &out)
			if code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("startup: exit=%d want=%d output=%s", code, tc.code, &out)
			}
		})
	}
}

func TestEnvironmentDashboardExplicitFlagsWin(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GOMETER_UI_LISTEN", "invalid-listener")
	t.Setenv("GOMETER_UI_PUBLIC_ORIGIN", "invalid-origin")
	t.Setenv("GOMETER_UI_DATA", "")
	workspace := filepath.Join(t.TempDir(), "flag-workspace")
	lines, _ := serveCLI(t, "ui", "-listen", "127.0.0.1:0", "-public-origin", "", "-data", workspace)
	url := startupLine(t, lines, "GoMeter dashboard: ")
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("explicit listener not applied: %s", url)
	}
	if data := startupLine(t, lines, "Saved scenarios and runs: "); data != workspace {
		t.Fatalf("explicit workspace not applied: %s", data)
	}
}
