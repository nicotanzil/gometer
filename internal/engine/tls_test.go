package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gometer/internal/config"
	"gometer/internal/telemetry"
)

func TestWorkloadTLSOptInAndIsolation(t *testing.T) {
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			fmt.Fprint(w, "queue 1\n")
			return
		}
		fmt.Fprint(w, "ok")
	}))
	target.EnableHTTP2 = true
	target.StartTLS()
	defer target.Close()
	for _, journey := range []bool{false, true} {
		for _, insecure := range []bool{false, true, false} {
			c := testConfig(target.URL)
			c.Duration, c.Rate, c.MaxInFlight = "40ms", 1, 1
			c.Insecure = insecure
			if journey {
				c.Scenarios = []config.Scenario{{Name: "TLS journey", Steps: []config.Step{{Name: "First", URL: "/"}, {Name: "Second", URL: "/"}}}}
			}
			r := runTest(t, c)
			if r.Insecure != insecure || r.Passed != insecure || r.Started < 1 {
				t.Fatalf("journey=%t insecure=%t: %+v", journey, insecure, r)
			}
			if insecure && (r.Failed != 0 || r.HTTP.Protocols["HTTP/2.0"] != r.Completed || !strings.Contains(r.Summary(), "TLS verification disabled")) {
				t.Fatalf("insecure workload failed or was not labeled: %+v", r)
			}
			if !insecure && (r.Failed != r.Completed || strings.Contains(r.Summary(), "TLS verification disabled")) {
				t.Fatal("verified default or subsequent run contaminated")
			}
		}
	}
	// Workload opt-in must not make the telemetry client's self-signed endpoint trusted.
	c := testConfig(target.URL)
	c.Duration, c.Rate, c.Insecure = "40ms", 1, true
	c.Telemetry = &telemetry.Config{URL: target.URL + "/metrics", Metrics: []telemetry.Metric{{Name: "queue", Kind: "gauge"}}}
	r, err := Run(context.Background(), c, Options{})
	if err != nil || !r.Passed || r.Telemetry == nil || r.Telemetry.Complete || r.Telemetry.FailedScrapes != r.Telemetry.Attempts || r.Telemetry.Attempts < 1 {
		t.Fatalf("telemetry TLS isolation: %v %+v", err, r)
	}
}
