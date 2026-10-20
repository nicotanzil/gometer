package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gometer/internal/engine"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLICSVValidationAndRunReports(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	csv := filepath.Join(dir, "people.csv")
	out := filepath.Join(dir, "run.json")
	html := filepath.Join(dir, "run.html")
	os.WriteFile(path, []byte(fmt.Sprintf(`{"url":%q,"vus":2,"iterations":5,"scenarios":[{"name":"Data","steps":[{"url":"/?name={{username}}"}]}]}`, s.URL)), 0600)
	os.WriteFile(csv, []byte("Full Name;ignore\nalice;private-source\nbob;other"), 0600)
	flags := []string{"-config", path, "-csv", csv, "-csv-delimiter", ";", "-csv-columns", `{"Full Name":"username","ignore":""}`, "-csv-sharing", "per_user", "-csv-eof", "stop_user"}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), append([]string{"validate"}, flags...), &stdout, &stderr); code != 0 || attempts.Load() != 0 {
		t.Fatalf("validate: %d %s", code, &stderr)
	}
	args := append([]string{"run"}, flags...)
	args = append(args, "-quiet", "-out", out, "-html", html)
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), args, &stdout, &stderr); code != 0 || attempts.Load() != 4 {
		t.Fatalf("run: %d %s %s", code, &stdout, &stderr)
	}
	var result engine.Result
	data, _ := os.ReadFile(out)
	json.Unmarshal(data, &result)
	page, _ := os.ReadFile(html)
	if result.SchemaVersion != 11 || result.StopReason != "data_exhausted" || result.Datasets[0].RowsAssigned != 4 || result.Datasets[0].UsersExhausted != 2 || !strings.Contains(stdout.String(), "CSV Data: 4 rows assigned") || !strings.Contains(string(page), "CSV data exhausted") || strings.Contains(string(page), "private-source") || strings.Contains(string(data), "private-source") {
		t.Fatalf("report: %s %s", data, page)
	}
	for _, args := range [][]string{
		{"validate", "-config", path, "-csv-eof", "stop_user"},
		{"validate", "-config", path, "-csv", csv, "-csv-scenario", "Missing"},
		{"run", "-config", path, "-csv", csv, "-mode", "rate", "-rate", "1", "-quiet", "-out", "", "-html", ""},
		append(append([]string{"run"}, flags...), "-out", csv, "-html", ""),
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(context.Background(), args, &stdout, &stderr); code != 2 || attempts.Load() != 4 {
			t.Fatalf("preflight %v: %d %s", args, code, &stderr)
		}
	}
}
