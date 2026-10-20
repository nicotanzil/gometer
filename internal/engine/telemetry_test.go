package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gometer/internal/config"
	"gometer/internal/telemetry"
)

func TestTargetTelemetryIsSeparateFromRequestAndGeneratorMetrics(t *testing.T) {
	var attempts, scrapes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			scrapes.Add(1)
			if r.Header.Get("Authorization") != "" {
				t.Error("target request credentials forwarded to telemetry")
			}
			w.Header().Set("Content-Type", "text/plain;version=0.0.4")
			fmt.Fprintf(w, "# TYPE jobs_total counter\njobs_total %d\n# TYPE queue gauge\nqueue 0\n", attempts.Load())
			return
		}
		attempts.Add(1)
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := config.Defaults()
	c.URL = target.URL
	c.Mode = "rate"
	c.Rate = 10
	c.Duration = "550ms"
	c.Headers = map[string]string{"Authorization": "Bearer workload-only-secret"}
	disabled := false
	c.Diagnostics = &disabled
	c.Telemetry = &telemetry.Config{URL: target.URL + "/metrics", Interval: "250ms", Metrics: []telemetry.Metric{{Name: "jobs_total", Kind: "counter"}, {Name: "queue", Kind: "gauge"}}}
	r, err := Run(context.Background(), c, Options{Telemetry: telemetry.Options{Token: "unused-telemetry-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.SchemaVersion != 5 || r.Completed != 6 || r.Completed != attempts.Load() || r.HTTP != nil || r.Generator != nil || !r.Telemetry.Complete || (int(scrapes.Load()) < r.Telemetry.Attempts || int(scrapes.Load()) > r.Telemetry.Attempts+r.Telemetry.CanceledScrapes) || *r.Telemetry.Series[0].Last != 6 || *r.Telemetry.Series[1].Last != 0 {
		t.Fatalf("telemetry changed load accounting: %+v", r)
	}
	if err := telemetry.ValidateSummary(c.Telemetry, r.Telemetry, r.StartedAt, r.TotalSeconds); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "secret") {
		t.Fatal("report contains runtime credentials")
	}
	if !strings.Contains(r.Summary(), "Target telemetry:") {
		t.Fatal("CLI summary omitted telemetry")
	}
}
func TestTelemetryOutageDoesNotChangeLoadVerdictAndSetupErrorsSendNoTraffic(t *testing.T) {
	var attempts, scrapes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			scrapes.Add(1)
			http.Error(w, "private exporter error", 503)
			return
		}
		attempts.Add(1)
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	c := config.Defaults()
	c.URL = target.URL
	c.Mode = "rate"
	c.Rate = 1
	c.Duration = "50ms"
	c.Telemetry = &telemetry.Config{URL: target.URL + "/metrics", Metrics: []telemetry.Metric{{Name: "queue", Kind: "gauge"}}}
	if _, err := c.Validate(); err != nil || attempts.Load() != 0 || scrapes.Load() != 0 {
		t.Fatal("validation performed I/O")
	}
	c.Telemetry.BearerAuth = true
	if _, err := Run(context.Background(), c, Options{}); err == nil || attempts.Load() != 0 || scrapes.Load() != 0 {
		t.Fatal("credential setup failure dispatched work")
	}
	c.Telemetry.BearerAuth = false
	r, err := Run(context.Background(), c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.Completed != 1 || r.HTTP.FirstByte.Samples != 1 || r.Telemetry.Complete || r.Telemetry.Attempts < 1 || r.Telemetry.FailedScrapes != r.Telemetry.Attempts || r.Telemetry.Series[0].Last != nil {
		t.Fatalf("outage invented observations or failed load checks: %+v", r)
	}
	if err := telemetry.ValidateSummary(c.Telemetry, r.Telemetry, r.StartedAt, r.TotalSeconds); err != nil {
		t.Fatal(err)
	}
}
