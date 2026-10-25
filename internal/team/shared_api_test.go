package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gometer/internal/sharedtest"
	"gometer/internal/workspace"
)

func TestPostgresTeamSharedAuthoringCopyAndPublicExport(t *testing.T) {
	e := environment(t)
	ctx := context.Background()
	admin := e.signIn(t)
	viewer, _ := e.memberSession(t, "viewer", "Reviewer", "viewer")
	operator, operatorID := e.memberSession(t, "operator", "Operator", "operator")
	response, data := e.apiRequest(t, "POST", "/api/secret-references", `{"name":"Credential","source_key":"PRIVATE_SOURCE"}`, admin, nil)
	if response.StatusCode != 201 {
		t.Fatal("reference creation failed")
	}
	var ref workspace.SecretReference
	if json.Unmarshal(data, &ref) != nil {
		t.Fatal("reference not decoded")
	}
	body := fmt.Sprintf(`{"schema_version":1,"public_config":{"name":"Shared checkout","url":"http://127.0.0.1:1","headers":{"Authorization":""}},"secret_bindings":[{"path":"/headers/Authorization","reference_id":%q,"revision":1}]}`, ref.ID)
	for _, headers := range []map[string]string{{"X-Gometer-CSRF": ""}, {"Origin": "https://attacker.example"}, {"Origin": ""}} {
		response, _ = e.apiRequest(t, "POST", "/api/tests", body, operator, headers)
		if response.StatusCode != 403 {
			t.Fatal("unverified authoring accepted")
		}
	}
	response, _ = e.apiRequest(t, "POST", "/api/tests", body, viewer, nil)
	if response.StatusCode != 403 {
		t.Fatal("viewer authored test")
	}
	response, data = e.apiRequest(t, "POST", "/api/tests", body, operator, nil)
	if response.StatusCode != 201 {
		t.Fatalf("create: %d %s", response.StatusCode, data)
	}
	var saved workspace.SharedDefinition
	if json.Unmarshal(data, &saved) != nil || saved.Creator.ID != operatorID || saved.Revision != 1 {
		t.Fatal("server attribution missing")
	}
	location, tag := response.Header.Get("Location"), response.Header.Get("ETag")
	if location != "/api/tests/"+saved.ID+"/document" || tag == "" {
		t.Fatal("location/ETag missing")
	}
	assertPrivateAbsent(t, data, "PRIVATE_SOURCE", "source_key")
	for _, cookie := range []*http.Cookie{viewer, operator, admin} {
		response, data = e.apiRequest(t, "GET", location, "", cookie, nil)
		if response.StatusCode != 200 || !strings.Contains(string(data), "127.0.0.1:1") || !strings.Contains(string(data), ref.ID) {
			t.Fatal("public fields or binding unavailable")
		}
		assertPrivateAbsent(t, data, "PRIVATE_SOURCE", "source_key")
		response, data = e.apiRequest(t, "GET", "/api/tests/"+saved.ID+"/export", "", cookie, nil)
		if response.StatusCode != 200 || response.Header.Get("Content-Disposition") == "" {
			t.Fatal("public export denied")
		}
		if _, _, err := sharedtest.Parse(data); err != nil {
			t.Fatal("export is not a reusable shared document")
		}
		assertPrivateAbsent(t, data, "PRIVATE_SOURCE", "source_key", "created_by")
	}
	response, _ = e.apiRequest(t, "PUT", location, body, operator, nil)
	if response.StatusCode != 428 {
		t.Fatal("missing precondition accepted")
	}
	for _, invalid := range []string{"*", "W/" + tag, strings.Replace(tag, "/1", "/01", 1)} {
		response, _ = e.apiRequest(t, "PUT", location, body, operator, map[string]string{"If-Match": invalid})
		if response.StatusCode != 400 {
			t.Fatal("invalid precondition accepted")
		}
	}
	response, _ = e.apiRequest(t, "PUT", location, body, viewer, map[string]string{"If-Match": tag})
	if response.StatusCode != 403 {
		t.Fatal("viewer edited test")
	}
	response, _ = e.apiRequest(t, "PUT", location, body, operator, map[string]string{"If-Match": tag, "X-Gometer-CSRF": ""})
	if response.StatusCode != 403 {
		t.Fatal("unverified update accepted")
	}
	updatedBody := strings.Replace(body, "Shared checkout", "Updated checkout", 1)
	response, data = e.apiRequest(t, "PUT", location, updatedBody, operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 200 {
		t.Fatalf("update: %d %s", response.StatusCode, data)
	}
	newTag := response.Header.Get("ETag")
	if newTag == tag {
		t.Fatal("revision not advanced")
	}
	response, _ = e.apiRequest(t, "PUT", location, body, operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 409 {
		t.Fatal("stale update overwrote newer edit")
	}
	copyPath := "/api/tests/" + saved.ID + "/copy"
	response, _ = e.apiRequest(t, "POST", copyPath, "", operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 409 {
		t.Fatal("stale copy accepted")
	}
	response, _ = e.apiRequest(t, "POST", copyPath, "", viewer, map[string]string{"If-Match": newTag})
	if response.StatusCode != 403 {
		t.Fatal("viewer copied test")
	}
	response, _ = e.apiRequest(t, "POST", copyPath, "", operator, map[string]string{"If-Match": newTag, "X-Gometer-CSRF": ""})
	if response.StatusCode != 403 {
		t.Fatal("unverified copy accepted")
	}
	response, data = e.apiRequest(t, "POST", copyPath, "", operator, map[string]string{"If-Match": newTag})
	if response.StatusCode != 201 {
		t.Fatalf("copy: %d %s", response.StatusCode, data)
	}
	var copied workspace.SharedDefinition
	if json.Unmarshal(data, &copied) != nil || copied.ID == saved.ID || copied.Source == nil || copied.Source.Revision != 2 {
		t.Fatal("copy identity/provenance incorrect")
	}
	// A reference catalog edit cannot silently change a saved binding.
	response, _ = e.apiRequest(t, "PUT", "/api/secret-references/"+ref.ID, `{"name":"Credential","source_key":"PRIVATE_REPLACEMENT","enabled":true}`, admin, map[string]string{"If-Match": secretETag(ref)})
	if response.StatusCode != 200 {
		t.Fatal("reference revision fixture failed")
	}
	response, _ = e.apiRequest(t, "POST", copyPath, "", operator, map[string]string{"If-Match": newTag})
	if response.StatusCode != 409 {
		t.Fatal("copy silently retargeted a credential")
	}
	// None of these APIs execute a workload, even for a reachable/literal target.
	response, _ = e.apiRequest(t, "POST", "/api/runs", body, operator, nil)
	if response.StatusCode != 404 {
		t.Fatal("execution exposed before queue/executor")
	}
	// Revocation applies to a session already in use.
	adminMember, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.SetMember(ctx, e.wid, adminMember.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "operator", DisplayName: "Operator"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	response, _ = e.apiRequest(t, "PUT", location, updatedBody, operator, map[string]string{"If-Match": newTag})
	if response.StatusCode != 403 {
		t.Fatal("old session retained edit privileges")
	}
}
func TestPostgresTeamSharedDocumentsRejectPrivateAmbiguousAndForeignInput(t *testing.T) {
	e := environment(t)
	ctx := context.Background()
	admin := e.signIn(t)
	valid := `{"schema_version":1,"public_config":{"name":"Shared","url":"https://target.example"},"secret_bindings":[]}`
	for _, body := range []string{
		`{"name":"PRIVATE_INPUT","url":"https://target.example"}`,
		strings.Replace(valid, `"secret_bindings":[]`, `"secret_bindings":[],"actor_id":"PRIVATE_INPUT"`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"SCHEMA_VERSION":1`, 1),
		strings.Replace(valid, `"url":"https://target.example"`, `"url":"https://target.example","headers":{"Authorization":"PRIVATE_INPUT"}`, 1),
		valid + ` {}`, strings.Repeat("a", (1<<20)+1),
	} {
		response, data := e.apiRequest(t, "POST", "/api/tests", body, admin, nil)
		if response.StatusCode != 400 {
			t.Fatalf("invalid input: %d", response.StatusCode)
		}
		assertPrivateAbsent(t, data, "PRIVATE_INPUT")
	}
	response, _ := e.apiRequest(t, "POST", "/api/tests?workspace=other", valid, admin, nil)
	if response.StatusCode != 400 {
		t.Fatal("browser workspace selector accepted")
	}
	response, _ = e.apiRequest(t, "POST", "/api/tests", valid, admin, map[string]string{"Content-Type": "text/plain"})
	if response.StatusCode != 415 {
		t.Fatal("wrong content type accepted")
	}
	current, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := e.db.CreateDefinition(ctx, e.wid, current.ID, json.RawMessage(`{"name":"Private legacy","url":"https://target.example","headers":{"Authorization":"PRIVATE_INPUT"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"document", "export"} {
		response, data := e.apiRequest(t, "GET", "/api/tests/"+legacy.ID+"/"+suffix, "", admin, nil)
		if response.StatusCode != 404 {
			t.Fatal("legacy configuration published")
		}
		assertPrivateAbsent(t, data, "PRIVATE_INPUT")
	}
	other, _ := workspace.NewID()
	otherActor, err := e.db.Bootstrap(ctx, other, "Other", workspace.Identity{Issuer: e.config.Issuer, Subject: "other", DisplayName: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := e.db.CreateSharedDefinition(ctx, other, otherActor, []byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	response, _ = e.apiRequest(t, "GET", "/api/tests/"+hidden.ID+"/document", "", admin, nil)
	if response.StatusCode != 404 {
		t.Fatal("shared document crossed workspace")
	}
}
