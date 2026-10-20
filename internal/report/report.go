// Package report produces portable artifacts with no external assets.
package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gometer/internal/config"
	"gometer/internal/design"
	"gometer/internal/engine"
	"gometer/internal/telemetry"
)

func JSON(w io.Writer, result engine.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func HTML(w io.Writer, result engine.Result) error { return page.Execute(w, result) }

// CheckPath catches most output failures before traffic starts.
func CheckPath(path string) error {
	if path == "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("output path is a directory: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gometer-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	return f.Close()
}

// Write replaces an artifact atomically, so a failed render cannot truncate a
// previous report. Files may contain sensitive URLs and are owner-readable.
func Write(path string, render func(io.Writer) error) error {
	if path == "" {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gometer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := render(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

var page = template.Must(template.New("report").Funcs(template.FuncMap{
	"designCSS":      func() template.CSS { return template.CSS(design.ReportCSS) },
	"brandMark":      func() template.HTML { return template.HTML(design.LogoSVG) },
	"f":              func(v float64) string { return fmt.Sprintf("%.2f", v) },
	"f1":             func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"mib":            func(v uint64) string { return fmt.Sprintf("%.2f", float64(v)/(1<<20)) },
	"timingRows":     timingRows,
	"telemetryValue": telemetryValue,
	"errorAction": func(action string) string {
		return map[string]string{config.NextIteration: "Start next iteration", config.ContinueJourney: "Continue journey", config.StopUser: "Stop this user", config.StopTest: "Stop test gracefully", config.StopTestNow: "Stop test immediately"}[action]
	},
	"stopReason": func(reason string) string {
		return map[string]string{"data_exhausted": "CSV data exhausted", "iterations": "iterations completed", "duration": "duration reached", "interrupted": "interrupted", "users_stopped": "users stopped after errors", "test_stopped": "test stopped gracefully after error", "test_stopped_now": "test stopped immediately after error"}[reason]
	},
	"telemetryRate":   telemetryRate,
	"telemetryLabels": telemetryLabels,
	"lastIndex":       func(points []telemetry.Snapshot) int { return len(points) - 1 },
	"n": func(v int64) string {
		s := fmt.Sprintf("%d", v)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return s
	},
	"dropped": func(s, c int64) int64 { return s + c },
	"drain": func(total, load float64, schedule *engine.UserSchedule) string {
		startup := 0.0
		if schedule != nil {
			startup = schedule.StartupSeconds
		}
		return fmt.Sprintf("%.2f", max(0, total-load-startup))
	},
	"label": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
	"width": func(actual float64, target int) float64 {
		if target <= 0 {
			return 0
		}
		v := 100 * actual / float64(target)
		if v > 100 {
			v = 100
		}
		if v < 0 {
			v = 0
		}
		return v
	},
}).Parse(telemetryHTML + workersHTML + diagnosticsHTML + htmlPage))

const htmlPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Name}} · GoMeter</title>
<style>
:root{color-scheme:dark;--bg:#0c121c;--panel:#141d2b;--border:#273345;--muted:#96a7be;--text:#edf3fb;--mint:#7ce4bd;--red:#ff938a}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.6 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:1160px;margin:auto;padding:38px 32px 48px}
header{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid var(--border);padding-bottom:22px}.brand{font-size:24px;font-weight:800;letter-spacing:-1px}.brand span{color:var(--mint)}.tag{font-size:11px;letter-spacing:2px;text-transform:uppercase;color:var(--muted)}
.hero{padding:32px 0 25px}.eyebrow{font-size:12px;letter-spacing:1.7px;color:var(--muted);text-transform:uppercase}h1{font-size:34px;line-height:1.25;letter-spacing:-1px;margin:12px 0}h2{font-size:18px;letter-spacing:-.3px;margin:0 0 18px}.target{font:13px/1.6 ui-monospace,SFMono-Regular,monospace;color:var(--muted);overflow-wrap:anywhere}.badge{display:inline-block;font-size:12px;font-weight:700;letter-spacing:1px;border-radius:6px;padding:5px 10px;background:#173c33;color:var(--mint);vertical-align:middle;margin-left:12px}.badge.fail{background:#422622;color:var(--red)}
.cards{display:grid;grid-template-columns:repeat(4,1fr);gap:14px;margin-bottom:22px}.card,.panel{background:var(--panel);border:1px solid var(--border);border-radius:12px;padding:22px}.card-label{color:var(--muted);font-size:12px}.value{font-size:30px;letter-spacing:-1px;font-weight:650;margin:5px 0}.unit{font-size:14px;font-weight:400;color:var(--muted);letter-spacing:0}.note{color:var(--muted);font-size:12px}.grid{display:grid;grid-template-columns:1.7fr 1fr;gap:18px;margin-bottom:22px}.panel{min-width:0}table{width:100%;border-collapse:collapse;text-align:left;font-variant-numeric:tabular-nums}th{font-size:11px;color:var(--muted);font-weight:500;text-transform:uppercase;letter-spacing:.8px}th,td{padding:11px 10px;border-bottom:1px solid var(--border)}th:first-child,td:first-child{padding-left:0}tbody tr:last-child td{border:0}td{font-size:13px}td.num{text-align:right}.scroll{overflow-x:auto}.phase-name{font-weight:650}.bar{height:5px;background:#283a4b;border-radius:4px;margin-top:7px;min-width:100px}.fill{height:100%;background:var(--mint);border-radius:4px}.good{color:var(--mint)}.bad{color:var(--red)}.check{display:flex;align-items:flex-start;gap:10px;padding:10px 0;border-bottom:1px solid var(--border)}.check:last-child{border:0}.check-symbol{font-weight:800}.check-text{flex:1;font-size:13px}.check-value{font-size:12px;color:var(--muted);white-space:nowrap}
.stats{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}.stat{padding:6px 0}.stat strong{display:block;font-size:20px;letter-spacing:-.5px}.stat span{font-size:12px;color:var(--muted)}.details{display:flex;flex-wrap:wrap;gap:12px 28px;margin-top:16px;color:var(--muted);font-size:12px}.warning{background:#33251c;border:1px solid #6b4930;color:#ffd2a1;padding:14px 18px;border-radius:9px;font-size:13px;margin-bottom:22px}footer{color:var(--muted);font-size:12px;padding-top:24px;display:flex;justify-content:space-between;gap:20px}.small{font-size:12px;color:var(--muted);margin-top:12px}
@media(max-width:900px){.grid{grid-template-columns:1fr}.cards{grid-template-columns:repeat(2,1fr)}}@media(max-width:520px){main{padding:22px 16px}.card{padding:16px}.value{font-size:25px}h1{font-size:27px}.tag{display:none}.stats{grid-template-columns:repeat(2,1fr)}footer{display:block}.check-value{white-space:normal}}
{{designCSS}}
</style></head><body><main>{{$traffic := .Arrivals}}
<header><div class="brand">{{brandMark}}<span>GoMeter</span></div><div class="tag">Find your system's limits</div></header>
<section class="hero"><div class="eyebrow">HTTP stress test · {{.Mode}} mode · {{if eq .Workload "journeys"}}journey workload{{else}}request workload{{end}}</div><h1>{{.Name}}<span class="badge {{if not .Passed}}fail{{end}}">{{if .Interrupted}}INTERRUPTED{{else if .Passed}}PASS{{else}}FAIL{{end}}</span></h1><div class="target">{{if ne .Workload "journeys"}}{{.Method}} {{end}}{{.URL}}</div><div class="details"><span>{{.StartedAt.Format "2006-01-02 15:04:05 UTC"}}</span><span>{{f .LoadSeconds}}s load · {{drain .TotalSeconds .LoadSeconds .UserSchedule}}s {{if or .Distribution .Telemetry}}drain + collection{{else}}drain{{end}}</span><span>{{.ConcurrencyLimit}} concurrent {{if eq .Workload "journeys"}}journey{{else}}request{{end}} limit</span><span>{{.ArrivalDistribution}} arrivals</span></div></section>
{{if .Datasets}}<div class="details"><span>Stopped: {{stopReason .StopReason}}</span>{{range .Datasets}}<span>CSV {{.Scenario}} · {{.Name}} · {{.Rows}} source rows · {{.Sharing}} · {{.OnEOF}} · {{.RowsAssigned}} rows assigned · {{.UsersExhausted}} users finished at EOF</span>{{end}}</div>{{end}}
{{if .UserSchedule}}<div class="details"><span>{{f .UserSchedule.StartupSeconds}}s startup wait / {{f .UserSchedule.PlannedStartupSeconds}}s planned</span><span>{{f .UserSchedule.RampUpSeconds}}s user ramp</span><span>{{.UserSchedule.Users.Started}} / {{.UserSchedule.Users.Planned}} users started · {{.UserSchedule.Users.Finished}} finished</span><span>Stopped: {{stopReason .StopReason}}</span></div>{{end}}
{{if .FailurePolicy}}<div class="details"><span>Failure action: {{errorAction .FailurePolicy.Action}}</span>{{if or (eq .FailurePolicy.Action "stop_test") (eq .FailurePolicy.Action "stop_test_now")}}<span>Error-triggered test stop: {{if .FailurePolicy.TestStopped}}yes{{else}}no{{end}}</span>{{else}}<span>{{.FailurePolicy.UsersStopped}} users stopped on error</span>{{end}}<span>Stopped: {{stopReason .StopReason}}</span></div>{{end}}
{{if .Iterations}}<div class="details"><span>{{.ConcurrencyLimit}} users × {{.Iterations}} iterations per user</span><span>Stopped: {{if eq .StopReason "iterations"}}iterations completed{{else if eq .StopReason "duration"}}duration reached{{else}}{{stopReason .StopReason}}{{end}}</span>{{if .PlannedLoadSeconds}}<span>{{f .PlannedLoadSeconds}}s admission limit</span>{{end}}</div>{{end}}
{{if .Insecure}}<div class="warning">TLS verification disabled for workload HTTPS requests. Server certificates and hostnames were not verified.</div>{{end}}
{{if or $traffic.DroppedCapacity $traffic.DroppedScheduler}}<div class="warning">{{n (dropped $traffic.DroppedCapacity $traffic.DroppedScheduler)}} scheduled {{if eq .Workload "journeys"}}journeys{{else}}requests{{end}} were not started. Latency describes only the work that ran.</div>{{end}}
{{if .Interrupted}}<div class="warning">This run was interrupted. Future arrivals were not scheduled, and active requests were canceled. These results describe a partial test.</div>{{end}}
{{if and .FailurePolicy .FailurePolicy.TestStopped}}<div class="warning">This test stopped because work failed. New users and iterations were stopped. {{if eq .FailurePolicy.Action "stop_test"}}Other admitted journeys drained under their deadlines.{{else}}Active workload requests and journey pauses were canceled.{{end}} These results describe a partial test.</div>{{end}}
{{template "workers" .}}
<div class="cards"><div class="card"><div class="card-label">Started HTTP attempts</div><div class="value">{{f1 .StartedRPS}} <span class="unit">req/s</span></div><div class="note">{{n .Started}} logical request attempts</div></div><div class="card"><div class="card-label">95th percentile request latency</div><div class="value">{{f .Latency.P95MS}} <span class="unit">ms</span></div><div class="note">Full response, including failed attempts</div></div><div class="card"><div class="card-label">Request errors</div><div class="value {{if .Failed}}bad{{end}}">{{f .ErrorPercent}}<span class="unit">%</span></div><div class="note">{{n .Failed}} of {{n .Completed}} completed</div></div><div class="card"><div class="card-label">Missed demand</div><div class="value {{if or $traffic.DroppedCapacity $traffic.DroppedScheduler}}bad{{end}}">{{f $traffic.DropPercent}}<span class="unit">%</span></div><div class="note">{{n (dropped $traffic.DroppedCapacity $traffic.DroppedScheduler)}} of {{n $traffic.Scheduled}} workload arrivals</div></div></div>
{{if eq .Workload "journeys"}}<div class="cards"><div class="card"><div class="card-label">Journey arrivals</div><div class="value">{{f1 .Journeys.StartedPerSecond}} <span class="unit">journeys/s</span></div><div class="note">{{n .Journeys.Started}} started journeys</div></div><div class="card"><div class="card-label">Journey latency · p95</div><div class="value">{{f .Journeys.Latency.P95MS}} <span class="unit">ms</span></div><div class="note">Entire sequence, verification, and pauses</div></div><div class="card"><div class="card-label">Journey errors</div><div class="value {{if .Journeys.Failed}}bad{{end}}">{{f .Journeys.ErrorPercent}}<span class="unit">%</span></div><div class="note">{{n .Journeys.Failed}} of {{n .Journeys.Completed}} completed journeys</div></div><div class="card"><div class="card-label">Journey start delay · p95</div><div class="value">{{f .Journeys.StartDelay.P95MS}} <span class="unit">ms</span></div><div class="note">Intended arrival to journey start</div></div></div>{{end}}
<div class="grid"><section class="panel"><h2>Traffic phases</h2><div class="scroll"><table><thead><tr><th>Phase</th><th>Target/s</th><th>Started/s</th><th>Errors</th><th>Dropped</th><th>P95</th></tr></thead><tbody>{{range .Phases}}{{$rate:=.StartedRPS}}{{$errors:=.ErrorPercent}}{{$drops:=.DropPercent}}{{$latency:=.Latency.P95MS}}{{if eq $.Workload "journeys"}}{{$rate = .Journeys.StartedPerSecond}}{{$errors = .Journeys.ErrorPercent}}{{$drops = .Journeys.DropPercent}}{{$latency = .Journeys.Latency.P95MS}}{{end}}<tr><td class="phase-name">{{.Name}}<div class="bar"><div class="fill" style="width:{{width $rate .TargetRate}}%"></div></div></td><td>{{if .TargetRate}}{{.TargetRate}}{{else}}—{{end}}</td><td>{{f1 $rate}}</td><td class="{{if $errors}}bad{{end}}">{{f $errors}}%</td><td>{{f $drops}}%</td><td>{{f $latency}} ms</td></tr>{{end}}</tbody></table></div><p class="small">This table measures {{if eq .Workload "journeys"}}journeys{{else}}requests{{end}}. Bars show started arrivals relative to the target. Requests in a journey belong to its arrival phase, including steps completed during drain.</p></section>
<section class="panel"><h2>Acceptance checks</h2>{{range .Checks}}<div class="check"><span class="check-symbol {{if .Passed}}good{{else}}bad{{end}}">{{if .Passed}}✓{{else}}✕{{end}}</span><span class="check-text">{{label .Name}}</span><span class="check-value">{{f .Actual}} / {{f .Limit}} {{.Unit}}</span></div>{{end}}<p class="small">Maximum thresholds require actual ≤ limit. Minimum throughput and completed requests require actual ≥ limit.</p></section></div>
<section class="panel"><h2>Measurement details</h2><div class="stats"><div class="stat"><strong>{{n $traffic.Scheduled}}</strong><span>Scheduled workload arrivals</span></div><div class="stat"><strong>{{n .Completed}}</strong><span>Completed request attempts</span></div><div class="stat"><strong>{{n .Succeeded}}</strong><span>Successful requests</span></div><div class="stat"><strong>{{n $traffic.DroppedCapacity}}</strong><span>Missed: concurrency limit</span></div><div class="stat"><strong>{{n $traffic.DroppedScheduler}}</strong><span>Missed: scheduler behind</span></div><div class="stat"><strong>{{n .PeakInFlight}}</strong><span>{{if .Distribution}}Sum of worker request peaks · upper bound{{else}}Peak requests in flight{{end}}</span></div><div class="stat"><strong>{{f .Latency.P50MS}} ms</strong><span>Median response latency</span></div><div class="stat"><strong>{{f .Latency.P99MS}} ms</strong><span>99th percentile latency</span></div><div class="stat"><strong>{{f .StartDelay.P95MS}} ms</strong><span>95th percentile request start delay</span></div><div class="stat"><strong>{{f .ScheduledLatency.P95MS}} ms</strong><span>95th percentile request scheduled latency</span></div><div class="stat"><strong>{{n .BytesRead}}</strong><span>Response body bytes read</span></div><div class="stat"><strong>{{f .TotalSeconds}}s</strong><span>Load plus response drain{{if or .Distribution .Telemetry}} and collection{{end}}</span></div></div><div class="details">{{range $status,$count := .Statuses}}<span>HTTP {{$status}}: {{n $count}}</span>{{end}}{{range $error,$count := .Errors}}<span class="bad">{{label $error}}: {{n $count}}</span>{{end}}</div></section>
{{range .Scenarios}}<section class="panel" style="margin-top:22px"><h2>{{.Name}} <span class="badge {{if not .Passed}}fail{{end}}">{{if not .Passed}}CHECKS FAILED{{else if not .Journeys.Completed}}NO SAMPLES{{else if not .Checks}}NO CHECKS{{else}}CHECKS PASSED{{end}}</span></h2><div class="details"><span>Weight {{.Weight}}</span><span>{{n .Journeys.Completed}} completed journeys</span><span>{{f .Journeys.ErrorPercent}}% journey errors</span><span>{{f .Journeys.DropPercent}}% missed arrivals</span><span>P95 journey {{f .Journeys.Latency.P95MS}} ms</span></div><div class="scroll"><table><thead><tr><th>Step</th><th>Method</th><th>Attempts</th><th>Skipped journeys</th><th>Errors</th><th>P95</th></tr></thead><tbody>{{range .Steps}}<tr><td>{{.Name}}</td><td>{{.Method}}</td><td>{{n .Completed}}</td><td>{{n .Skipped}}{{if .SkippedCondition}} ({{n .SkippedCondition}} conditional){{end}}</td><td class="{{if .Failed}}bad{{end}}">{{f .ErrorPercent}}%{{range $category,$count:=.Errors}}<div class="note">{{label $category}}: {{n $count}}</div>{{end}}</td><td>{{if .Latency.Samples}}{{f .Latency.P95MS}} ms{{else}}—{{end}}</td></tr>{{end}}</tbody></table></div>{{if .Controls}}<h3>Control flow</h3><div class="scroll"><table><thead><tr><th>Control</th><th>Kind</th><th>Visits</th><th>True / False</th><th>Iterations</th><th>Failed</th></tr></thead><tbody>{{range .Controls}}<tr><td>{{.Name}}</td><td>{{.Kind}}</td><td>{{n .Entered}}</td><td>{{n .ConditionTrue}} / {{n .ConditionFalse}}</td><td>{{n .Iterations}}</td><td>{{n .Failed}}</td></tr>{{end}}</tbody></table></div>{{end}}{{range $category,$count:=.Journeys.Errors}}<div class="small bad">Journey {{label $category}}: {{n $count}}</div>{{end}}<p class="small">Attempts count HTTP executions, including repeated loop requests. Skipped journeys never attempted that request node; conditional skips identify an unchosen path or a zero-iteration body. Other skips follow failure or cancellation. Control counts have no HTTP latency.</p></section>{{end}}
{{template "diagnostics" .}}
{{template "telemetry" .}}
<footer><span>GoMeter · report schema {{.SchemaVersion}} · standalone report</span><span>Latency quantiles use bounded histograms with ≤1% bucket error plus 1 ns rounding.<br>Throughput averages use the load window; response drain is separate.</span></footer>
</main></body></html>`
