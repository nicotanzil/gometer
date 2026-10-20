package cli

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

	"gometer/internal/config"
	"gometer/internal/engine"
	"gometer/internal/telemetry"
)

func TestCLITelemetryCredentialsAndDisableOverride(t *testing.T) {
	const token = "cli-test-exporter-secret"
	t.Setenv("GOMETER_TELEMETRY_TOKEN", token)
	t.Setenv("GOMETER_TELEMETRY_CA", "")
	var load, scrapes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			scrapes.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprint(w, "queue 0\n")
			return
		}
		load.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("exporter credentials reached the workload endpoint")
		}
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := config.Defaults()
	c.URL, c.Mode, c.Rate, c.Duration = target.URL+"/work", "rate", 10, "350ms"
	c.Telemetry = &telemetry.Config{URL: target.URL + "/metrics", Interval: "250ms", Timeout: "100ms", BearerAuth: true, Metrics: []telemetry.Metric{{Name: "queue", Kind: "gauge"}}}
	dir := t.TempDir()
	path, out, html := filepath.Join(dir, "config.json"), filepath.Join(dir, "result.json"), filepath.Join(dir, "result.html")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"run", "-config", path, "-quiet", "-diagnostics=false", "-out", out, "-html", html}
	if code := Run(context.Background(), args, &output, &output); code != 0 || strings.Contains(output.String(), token) {
		t.Fatalf("telemetry CLI: %d %s", code, &output)
	}
	readResult := func() engine.Result {
		t.Helper()
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var r engine.Result
		if err := json.Unmarshal(data, &r); err != nil || strings.Contains(string(data), token) {
			t.Fatal("invalid or credential-bearing result", err)
		}
		return r
	}
	r := readResult()
	if r.SchemaVersion != 5 || r.Telemetry == nil || !r.Telemetry.Complete || *r.Telemetry.Series[0].Last != 0 || r.Completed != load.Load() || r.Generator != nil || r.HTTP != nil {
		t.Fatalf("unexpected telemetry result: %+v", r)
	}
	data, err = os.ReadFile(html)
	if err != nil || strings.Contains(string(data), token) || !strings.Contains(string(data), "Target-server telemetry") {
		t.Fatal("HTML omits telemetry or exposes credentials", err)
	}
	beforeLoad, beforeScrapes := load.Load(), scrapes.Load()
	t.Setenv("GOMETER_TELEMETRY_TOKEN", "")
	output.Reset()
	if code := Run(context.Background(), []string{"validate", "-config", path}, &output, &output); code != 0 || load.Load() != beforeLoad || scrapes.Load() != beforeScrapes {
		t.Fatal("structural validation needs credentials or sends traffic")
	}
	output.Reset()
	if code := Run(context.Background(), args, &output, &output); code != 2 || load.Load() != beforeLoad || scrapes.Load() != beforeScrapes {
		t.Fatal("missing credentials were not rejected before traffic")
	}
	output.Reset()
	args = append(args, "-telemetry=false", "-telemetry-ca", filepath.Join(dir, "absent.pem"))
	if code := Run(context.Background(), args, &output, &output); code != 0 || scrapes.Load() != beforeScrapes {
		t.Fatalf("disabled telemetry still needs credentials, a CA, or sends scrapes: %d %s", code, &output)
	}
	if r := readResult(); r.SchemaVersion != 3 || r.Telemetry != nil || r.Completed != load.Load()-beforeLoad {
		t.Fatal("disabled telemetry invented observations or changed request counts")
	}
}
