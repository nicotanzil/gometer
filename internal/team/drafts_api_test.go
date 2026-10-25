package team

import (
	"encoding/json"
	"strings"
	"testing"

	"gometer/internal/workspace"
)

func TestPostgresTeamDraftAPIIsPrivateConditionalAndCSRFProtected(t *testing.T) {
	e := environment(t)
	admin := e.signIn(t)
	operator, _ := e.memberSession(t, "operator", "Operator", "operator")
	viewer, _ := e.memberSession(t, "viewer", "Viewer", "viewer")
	body := `{"document":{"schema_version":1,"public_config":{"name":"Private draft","url":"https://target.example"},"secret_bindings":[]}}`
	response, _ := e.apiRequest(t, "POST", "/api/drafts", body, operator, map[string]string{"X-Gometer-CSRF": ""})
	if response.StatusCode != 403 {
		t.Fatal("unverified draft accepted")
	}
	response, _ = e.apiRequest(t, "POST", "/api/drafts", body, viewer, nil)
	if response.StatusCode != 403 {
		t.Fatal("viewer wrote draft")
	}
	for _, raw := range []string{strings.Replace(body, `"document":`, `"actor_id":"PRIVATE_INPUT","document":`, 1), `{"document":{},"document":{}}`, body + ` {}`, `{"document":{"url":"PRIVATE_INPUT"}}`} {
		response, data := e.apiRequest(t, "POST", "/api/drafts", raw, operator, nil)
		if response.StatusCode != 400 {
			t.Fatal("invalid or ambiguous draft accepted")
		}
		assertPrivateAbsent(t, data, "PRIVATE_INPUT")
	}
	response, data := e.apiRequest(t, "POST", "/api/drafts", body, operator, nil)
	if response.StatusCode != 201 {
		t.Fatalf("create: %d %s", response.StatusCode, data)
	}
	var draft workspace.Draft
	if json.Unmarshal(data, &draft) != nil {
		t.Fatal("draft decode failed")
	}
	path, tag := response.Header.Get("Location"), response.Header.Get("ETag")
	response, data = e.apiRequest(t, "GET", path, "", admin, nil)
	if response.StatusCode != 404 {
		t.Fatal("admin read another author's draft")
	}
	assertPrivateAbsent(t, data, "Private draft")
	response, _ = e.apiRequest(t, "PUT", path, body, admin, map[string]string{"If-Match": tag})
	if response.StatusCode != 404 {
		t.Fatal("admin updated another author's draft")
	}
	response, data = e.apiRequest(t, "GET", "/api/drafts", "", operator, nil)
	if response.StatusCode != 200 || strings.Contains(string(data), "public_config") {
		t.Fatal("draft list did not project metadata")
	}
	response, _ = e.apiRequest(t, "PUT", path, body, operator, nil)
	if response.StatusCode != 428 {
		t.Fatal("unconditional update accepted")
	}
	response, _ = e.apiRequest(t, "PUT", path, body, operator, map[string]string{"If-Match": "W/" + tag})
	if response.StatusCode != 400 {
		t.Fatal("weak ETag accepted")
	}
	response, _ = e.apiRequest(t, "PUT", path, body, operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 200 {
		t.Fatal("conditional update failed")
	}
	current := response.Header.Get("ETag")
	response, _ = e.apiRequest(t, "PUT", path, body, operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 409 {
		t.Fatal("stale draft overwritten")
	}
	response, _ = e.apiRequest(t, "DELETE", path, "", operator, map[string]string{"If-Match": current, "X-Gometer-CSRF": ""})
	if response.StatusCode != 403 {
		t.Fatal("unverified delete accepted")
	}
	response, _ = e.apiRequest(t, "DELETE", path, "", operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 409 {
		t.Fatal("stale draft deleted")
	}
	response, _ = e.apiRequest(t, "DELETE", path, "", operator, map[string]string{"If-Match": current})
	if response.StatusCode != 204 {
		t.Fatal("conditional deletion failed")
	}
	response, _ = e.apiRequest(t, "GET", path, "", operator, nil)
	if response.StatusCode != 404 {
		t.Fatal("deleted draft retained")
	}
}
