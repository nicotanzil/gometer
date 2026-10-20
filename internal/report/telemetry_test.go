package report

import (
	"bytes"
	"strings"
	"testing"

	"gometer/internal/engine"
	"gometer/internal/telemetry"
)

func TestTelemetryReportEscapesSeriesAndDistinguishesZeroFromMissing(t *testing.T) {
	zero := 0.0
	r := engine.Result{Name: "Telemetry", Telemetry: &telemetry.Summary{
		URL: "https://exporter.example/metrics", Attempts: 2, IncompleteScrapes: 2,
		Series: []telemetry.Series{
			{Metric: telemetry.Metric{Name: "queue", Title: "<script>queue</script>", Kind: "gauge", Labels: map[string]string{"path": "<img src=x>"}}, Samples: 2, Last: &zero, Min: &zero, Mean: &zero, Max: &zero},
			{Metric: telemetry.Metric{Name: "jobs_total", Title: "Jobs", Kind: "counter", Unit: "jobs"}, Missing: 2},
		},
		Timeline: []telemetry.Snapshot{{Values: []telemetry.Value{{Value: &zero}, {Issue: "selected series missing"}}}},
	}}
	var out bytes.Buffer
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"incomplete scrape coverage", "2 recorded scrapes", "0 / 0 / 0", "<td>—</td>", "Jobs: selected series missing", "&lt;script&gt;queue&lt;/script&gt;", `\u003cimg src=x\u003e`, "drain + collection"} {
		if !strings.Contains(html, want) {
			t.Fatalf("telemetry report omits %q", want)
		}
	}
	if strings.Contains(html, "<img src=x>") || strings.Contains(html, r.Telemetry.Series[0].Metric.Title) {
		t.Fatal("HTML executed exporter metadata")
	}
	r.Telemetry = nil
	out.Reset()
	if err := HTML(&out, r); err != nil || strings.Contains(out.String(), "Target-server telemetry") {
		t.Fatal("unconfigured telemetry appears in report", err)
	}
}
