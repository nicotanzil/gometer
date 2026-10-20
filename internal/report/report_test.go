package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gometer/internal/diagnostics"
	"gometer/internal/engine"
	"gometer/internal/metrics"
)

func TestReportFontsAreSelfContained(t *testing.T) {
	var out bytes.Buffer
	if err := HTML(&out, engine.Result{Name: "Portable report"}); err != nil {
		t.Fatal(err)
	}
	urls := regexp.MustCompile(`url\("([^"\)]+)"\)`).FindAllStringSubmatch(out.String(), -1)
	if len(urls) == 0 {
		t.Fatal("report has no bundled fonts")
	}
	for _, match := range urls {
		data, ok := strings.CutPrefix(match[1], "data:font/woff2;base64,")
		if !ok {
			t.Fatalf("report depends on a font server: %s", match[1])
		}
		font, err := base64.StdEncoding.DecodeString(data)
		if err != nil || !bytes.HasPrefix(font, []byte("wOF2")) {
			t.Fatal("report contains an invalid WOFF2 asset")
		}
	}
}

func TestReportsEscapeUntrustedNamesAndKeepCounts(t *testing.T) {
	r := engine.Result{Name: `<script>alert("x")</script>`, URL: "http://localhost/?x=<svg>", Passed: true}
	r.Scheduled = 12
	r.Started = 10
	r.DroppedCapacity = 2
	var html, js bytes.Buffer
	if err := HTML(&html, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), r.Name) || !strings.Contains(html.String(), "&lt;script&gt;") {
		t.Fatal("HTML did not escape test name")
	}
	if err := JSON(&js, r); err != nil {
		t.Fatal(err)
	}
	var decoded engine.Result
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != r.Name || decoded.Scheduled != 12 || decoded.DroppedCapacity != 2 {
		t.Fatalf("JSON changed result: %+v", decoded)
	}
}

func TestAtomicWritePreservesPreviousReportOnRenderFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(path, []byte("previous report"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Write(path, func(w io.Writer) error { io.WriteString(w, "partial"); return errors.New("render failed") })
	if err == nil {
		t.Fatal("render failure ignored")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "previous report" {
		t.Fatal("previous report was truncated")
	}
	if err := Write(path, func(w io.Writer) error { _, err := io.WriteString(w, "new report"); return err }); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "new report" {
		t.Fatal("report was not replaced")
	}
}

func TestJourneyReportUsesJourneyMissedDemand(t *testing.T) {
	r := engine.Result{Name: "Journey report", Workload: "journeys"}
	r.Journeys = engine.JourneyMetrics{Scheduled: 100, Started: 60, DroppedCapacity: 40, DropPercent: 40}
	var out bytes.Buffer
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"40 scheduled journeys", "40.00", "journeys/s", "Journey latency"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report omits %q", want)
		}
	}
}

func TestDiagnosticsReportDistinguishesAbsentTimingsAndCPU(t *testing.T) {
	r := engine.Result{Name: "Diagnostics", Generator: &diagnostics.Summary{Platform: "test/host", PeakHeapBytes: 2 << 20}}
	r.HTTP = &metrics.HTTPMetrics{ConnectionsAcquired: 2, ReusedConnections: 1, BodyRead: metrics.Latency{Samples: 2, MeanMS: 30, P50MS: 30, P95MS: 31, MaxMS: 31}, Protocols: map[string]int64{"HTTP/1.1": 2}}
	var out bytes.Buffer
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Generator process", ">Unavailable</strong>", "2.00 MiB", "HTTP/1.1: 2 response headers", "<td>DNS lookup</td><td>0</td><td>—</td>", "<td>Response body read</td><td>2</td><td>30.00 ms</td>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("diagnostic report omits %q", want)
		}
	}
	r.Generator, r.HTTP = nil, nil
	out.Reset()
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Generator process") || strings.Contains(out.String(), "HTTP timing and connections") {
		t.Fatal("old or disabled report contains invented diagnostics")
	}
}

func TestDistributedReportLabelsUnknownTrafficAndPeakBounds(t *testing.T) {
	r := engine.Result{Name: "Fleet", Workload: "requests", Distribution: &engine.Distribution{PlannedArrivals: 100, UnreportedPlannedArrivals: 50, PeakInFlightUpperBound: true, Failure: "Worker unavailable", Workers: []engine.WorkerStatus{{Name: "<worker>", URL: "https://worker.example", Status: "unreachable", Planned: 50}}}}
	var out bytes.Buffer
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Distributed workers", "INCOMPLETE", "50 planned arrivals remain unreported", "Unknown", "&lt;worker&gt;", "Sum of worker request peaks · upper bound"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report omits %q", want)
		}
	}
}
