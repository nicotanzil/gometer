package report

import (
	"encoding/json"
	"fmt"
	"gometer/internal/telemetry"
)

func telemetryValue(value *float64, unit string) string {
	if value == nil {
		return "—"
	}
	if unit == "bytes" {
		return fmt.Sprintf("%.3g MiB", *value/(1<<20))
	}
	if unit != "" {
		return fmt.Sprintf("%.4g %s", *value, unit)
	}
	return fmt.Sprintf("%.4g", *value)
}
func telemetryRate(value *float64, unit string) string {
	if value == nil {
		return "—"
	}
	if unit == "" {
		unit = "units"
	}
	if unit == "bytes" {
		return fmt.Sprintf("%.3g MiB/s", *value/(1<<20))
	}
	return fmt.Sprintf("%.4g %s/s", *value, unit)
}
func telemetryLabels(m telemetry.Metric) string {
	if len(m.Labels) == 0 {
		return ""
	}
	data, _ := json.Marshal(m.Labels)
	return string(data)
}

const telemetryHTML = `{{define "telemetry"}}{{with .Telemetry}}
<section class="panel" style="margin-top:22px"><h2>Target-server telemetry · {{if .Complete}}complete scrape coverage{{else}}incomplete scrape coverage{{end}}</h2>
<div class="target">{{.URL}}</div><div class="details"><span>{{.Attempts}} recorded scrapes · {{.FailedScrapes}} failed · {{.IncompleteScrapes}} incomplete · {{.CanceledScrapes}} canceled during stop</span><span>{{f .IntervalSeconds}}s interval · {{f .TimeoutSeconds}}s timeout</span><span>{{len .Timeline}} retained observations{{if .Truncated}} · latest 900{{end}}</span></div>
<div class="scroll"><table><thead><tr><th>Selected series</th><th>Last valid</th><th>Observed at</th><th>Gauge min / mean / max</th><th>Counter increase</th><th>Observed mean rate</th><th>Samples / gaps / resets</th></tr></thead><tbody>{{range .Series}}<tr><td><strong>{{.Metric.Title}}</strong><div class="note">{{.Metric.Name}} {{telemetryLabels .Metric}}</div></td><td>{{telemetryValue .Last .Metric.Unit}}</td><td>{{if .Last}}{{f .LastElapsedSeconds}}s{{else}}—{{end}}</td><td>{{if eq .Metric.Kind "gauge"}}{{telemetryValue .Min .Metric.Unit}} / {{telemetryValue .Mean .Metric.Unit}} / {{telemetryValue .Max .Metric.Unit}}{{else}}—{{end}}</td><td>{{telemetryValue .ObservedIncrease .Metric.Unit}}</td><td>{{telemetryRate .MeanRate .Metric.Unit}}{{if .MeanRate}}<div class="note">over {{f .CoveredSeconds}}s of continuous observations</div>{{end}}</td><td>{{.Samples}} / {{.Missing}} / {{.Resets}}</td></tr>{{end}}</tbody></table></div>
<p class="small">Server metrics come from the selected exporter and can include unrelated traffic. Last valid observations can predate the final scrape. Gauges use an unweighted sample mean and sampled extrema. Counter increases and rates exclude gaps and reset intervals; neither is a count of this run's requests. Sampling starts near admission and ends after drain. Missing samples are unknown. Scrapes add observer load and do not change load acceptance checks. No automatic target-health verdict is made.</p>
{{if .Timeline}}{{$last := index .Timeline (lastIndex .Timeline)}}{{if $last.Error}}<p class="bad">Latest scrape: {{$last.Error}}</p>{{else}}{{range $i, $value := $last.Values}}{{if $value.Issue}}<p class="bad">{{(index $.Telemetry.Series $i).Metric.Title}}: {{$value.Issue}}</p>{{end}}{{end}}{{end}}{{end}}
</section>{{end}}{{end}}`
