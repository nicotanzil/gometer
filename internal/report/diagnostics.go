package report

import "gometer/internal/metrics"

type timingRow struct {
	Name string
	metrics.Latency
}

func timingRows(h *metrics.HTTPMetrics) []timingRow {
	if h == nil {
		return nil
	}
	return []timingRow{{"Connection acquisition", h.ConnectionWait}, {"DNS lookup", h.DNS}, {"TCP connect", h.Connect}, {"TLS handshake", h.TLS}, {"Request write", h.Write}, {"First header byte from attempt start", h.FirstByte}, {"Response body read", h.BodyRead}}
}

const diagnosticsHTML = `{{define "diagnostics"}}
{{if .Generator}}<section class="panel" style="margin-top:22px"><h2>Generator process</h2><div class="stats">
<div class="stat"><strong>{{if .Generator.CPUAvailable}}{{f .Generator.AverageCPUPercent}}%{{else}}Unavailable{{end}}</strong><span>Average process CPU · 100% = one core</span></div>
<div class="stat"><strong>{{if .Generator.CPUAvailable}}{{f .Generator.PeakCPUPercent}}%{{else}}Unavailable{{end}}</strong><span>Peak sampled interval CPU</span></div>
<div class="stat"><strong>{{mib .Generator.PeakHeapBytes}} MiB</strong><span>Peak sampled Go heap</span></div>
<div class="stat"><strong>{{mib .Generator.PeakRuntimeBytes}} MiB</strong><span>Peak sampled memory mapped by Go runtime</span></div>
<div class="stat"><strong>{{if .Generator.Final.RSSAvailable}}{{mib .Generator.Final.ProcessPeakRSSBytes}} MiB{{else}}Unavailable{{end}}</strong><span>Process lifetime peak resident memory</span></div>
<div class="stat"><strong>{{.Generator.PeakGoroutines}}</strong><span>Peak sampled goroutines</span></div>
<div class="stat"><strong>{{.Generator.Final.GCCycles}}</strong><span>GC cycles during measurement window</span></div>
<div class="stat"><strong>{{f .Generator.Final.GCPauseMS}} ms</strong><span>Cumulative GC pause time during window</span></div>
<div class="stat"><strong>{{f .Arrivals.StartDelay.P95MS}} ms</strong><span>Workload arrival start delay · p95</span></div>
</div><div class="details"><span>{{.Generator.Platform}}</span><span>{{.Generator.GoVersion}}</span><span>{{.Generator.LogicalCPUs}} logical CPUs · GOMAXPROCS {{.Generator.Final.GOMAXPROCS}}</span><span>{{.Generator.Samples}} resource samples</span></div>
<p class="small">Process CPU includes other work in this GoMeter process, such as dashboard requests. Resources are sampled at start, about once per second, and after drain. Sampled peaks can miss short spikes. Go heap and runtime memory are not resident-memory measurements; resident peak includes earlier runs in this process. These metrics do not prove generator saturation or target-server health.</p></section>{{end}}
{{if .HTTP}}<section class="panel" style="margin-top:22px"><h2>HTTP timing and connections</h2><div class="scroll"><table><thead><tr><th>Observed interval</th><th>Samples</th><th>Mean</th><th>P50</th><th>P95</th><th>Max</th></tr></thead><tbody>
{{range timingRows .HTTP}}<tr><td>{{.Name}}</td><td>{{n .Samples}}</td><td>{{if .Samples}}{{f .MeanMS}} ms{{else}}—{{end}}</td><td>{{if .Samples}}{{f .P50MS}} ms{{else}}—{{end}}</td><td>{{if .Samples}}{{f .P95MS}} ms{{else}}—{{end}}</td><td>{{if .Samples}}{{f .MaxMS}} ms{{else}}—{{end}}</td></tr>{{end}}
</tbody></table></div><div class="details"><span>{{n .HTTP.ConnectionRequests}} acquisition requests</span><span>{{n .HTTP.ConnectionsAcquired}} acquired · {{n .HTTP.ReusedConnections}} reused · {{n .HTTP.IdleReuses}} idle reuses</span><span>{{n .HTTP.DialAttempts}} TCP dials · {{n .HTTP.DialFailures}} failed</span><span>{{n .HTTP.DNSLookups}} DNS lookups · {{n .HTTP.DNSFailures}} failed</span><span>{{n .HTTP.TLSHandshakes}} TLS handshakes · {{n .HTTP.TLSFailures}} failed</span><span>{{n .HTTP.WriteCompletions}} write completions · {{n .HTTP.WriteFailures}} failed</span><span>{{n .HTTP.BodyReadFailures}} body read failures</span><span>{{n .HTTP.UnfinishedEvents}} unfinished or unmatched pending events</span>{{range $protocol,$count := .HTTP.Protocols}}<span>{{$protocol}}: {{n $count}} response headers</span>{{end}}</div>
<p class="small">Timings overlap and have different sample populations. Connection acquisition includes DNS, TCP, TLS, and pool waiting when they occur. First header byte includes time from attempt start and may be informational headers; it is not server processing time. Body read starts when the client returns response headers and includes failed or capped reads. Reused connections have no DNS/TCP/TLS sample. Failed completed events are included; unfinished events and late callbacks after request completion have no duration. Transport retries can produce multiple acquisitions for one logical attempt.</p></section>{{end}}
{{end}}`
