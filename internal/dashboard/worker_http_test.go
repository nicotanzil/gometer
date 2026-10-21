package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSavedHTTPWorkerConfigurationCannotGrantTransportPermission(t *testing.T) {
	s := testServer(t)
	body := `{"name":"HTTP preset","url":"http://127.0.0.1:1","mode":"rate","rate":1,"duration":"1s","max_in_flight":1,"workers":["http://localhost:8801"]}`
	saved := request(s, "POST", "/api/scenarios", body)
	if saved.Code != 200 {
		t.Fatalf("save inert preset: %d %s", saved.Code, saved.Body)
	}
	var preset SavedScenario
	if err := json.Unmarshal(saved.Body.Bytes(), &preset); err != nil {
		t.Fatal(err)
	}
	reopened := request(s, "GET", "/api/scenarios/"+preset.ID, "")
	if reopened.Code != 200 {
		t.Fatalf("reopen preset: %d %s", reopened.Code, reopened.Body)
	}
	for _, path := range []string{"/api/validate", "/api/runs"} {
		denied := request(s, "POST", path, body)
		if denied.Code != 400 || !strings.Contains(denied.Body.String(), "GOMETER_ALLOW_HTTP_WORKERS") {
			t.Fatalf("saved configuration bypassed startup permission at %s: %d %s", path, denied.Code, denied.Body)
		}
	}
	injected := strings.TrimSuffix(body, "}") + `,"allow_http_workers":true}`
	if response := request(s, "POST", "/api/scenarios", injected); response.Code != 400 {
		t.Fatalf("configuration accepted startup permission: %d %s", response.Code, response.Body)
	}
	state := request(s, "GET", "/api/state", "")
	var workspace struct {
		Runs []RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if len(workspace.Runs) != 0 {
		t.Fatal("rejected run was reserved in history")
	}
}
