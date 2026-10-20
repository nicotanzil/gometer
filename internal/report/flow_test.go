package report

import (
	"bytes"
	"strings"
	"testing"

	"gometer/internal/engine"
)

func TestFlowReportExplainsAttemptsAndConditionalSkips(t *testing.T) {
	r := engine.Result{SchemaVersion: 6, Workload: "journeys", Scenarios: []engine.ScenarioResult{{Name: "Flow", Steps: []engine.StepResult{{Name: "Unchosen", Method: "GET", Skipped: 1, SkippedCondition: 1}}, Controls: []engine.ControlResult{{Name: "<branch>", Kind: "branch", ControlCounts: engine.ControlCounts{Entered: 1, Completed: 1, ConditionTrue: 1}}}}}}
	var out bytes.Buffer
	if err := HTML(&out, r); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Skipped journeys", "1 conditional", "&lt;branch&gt;", "True / False", "Control counts have no HTTP latency"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("missing %q", text)
		}
	}
	if strings.Contains(out.String(), "<branch>") {
		t.Fatal("control name was not escaped")
	}
}
