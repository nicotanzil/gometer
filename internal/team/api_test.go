package team

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gometer/internal/workspace"
)

func (e *testEnvironment) apiRequest(t *testing.T, method, path, body string, cookie *http.Cookie, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.app.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", e.app.URL)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
		req.Header.Set("X-Gometer-CSRF", csrfToken(cookie.Value))
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}
func (e *testEnvironment) memberSession(t *testing.T, subject, name, role string) (*http.Cookie, string) {
	t.Helper()
	ctx := context.Background()
	admin, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.db.SetMember(ctx, e.wid, admin.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: subject, DisplayName: name}, role, true)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := e.db.NewSession(ctx, e.wid, e.config.Issuer, subject, time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}, id
}
func assertPrivateAbsent(t *testing.T, data []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if bytes.Contains(data, []byte(value)) {
			t.Fatal("API response disclosed private configuration or source details")
		}
	}
}
func TestPostgresTeamTestsAPIProjectsMetadataAndUsesSessionIdentity(t *testing.T) {
	e := environment(t)
	ctx := context.Background()
	adminCookie := e.signIn(t)
	viewer, _ := e.memberSession(t, "viewer", "Reviewer", "viewer")
	operator, operatorID := e.memberSession(t, "operator", "Load Author", "operator")
	admin, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.db.CreateDefinition(ctx, e.wid, admin.ID, json.RawMessage(`{"name":"Admin smoke","url":"https://target.example/private-path?token=PRIVATE_QUERY","headers":{"Authorization":"PRIVATE_HEADER"},"body":"PRIVATE_BODY"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.db.CreateDefinition(ctx, e.wid, operatorID, json.RawMessage(`{"name":"Operator smoke","url":"https://target.example"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, _ := e.apiRequest(t, "GET", "/api/tests", "", nil, nil)
	if response.StatusCode != 401 {
		t.Fatal("anonymous metadata access accepted")
	}
	for _, cookie := range []*http.Cookie{adminCookie, viewer, operator} {
		response, data := e.apiRequest(t, "GET", "/api/tests", "", cookie, nil)
		if response.StatusCode != 200 {
			t.Fatalf("list: %d %s", response.StatusCode, data)
		}
		var list struct {
			Tests []workspace.Definition `json:"tests"`
		}
		if json.Unmarshal(data, &list) != nil || len(list.Tests) != 2 {
			t.Fatal("test metadata missing")
		}
		assertPrivateAbsent(t, data, "PRIVATE_QUERY", "PRIVATE_HEADER", "PRIVATE_BODY", "private-path", "target.example", `"config"`, `"enabled"`, `"role"`)
	}
	response, data := e.apiRequest(t, "GET", "/api/tests?scope=mine", "", operator, nil)
	var mine struct {
		Tests []workspace.Definition `json:"tests"`
	}
	json.Unmarshal(data, &mine)
	if response.StatusCode != 200 || len(mine.Tests) != 1 || mine.Tests[0].ID != second.ID {
		t.Fatal("My tests did not use the authenticated creator")
	}
	response, data = e.apiRequest(t, "GET", "/api/tests?search=Load%20Author", "", viewer, nil)
	if response.StatusCode != 200 || !bytes.Contains(data, []byte(second.ID)) || bytes.Contains(data, []byte(first.ID)) {
		t.Fatal("teammate search failed")
	}
	response, data = e.apiRequest(t, "GET", "/api/tests/"+first.ID, "", viewer, nil)
	if response.StatusCode != 200 || response.Header.Get("ETag") == "" || !bytes.Contains(data, []byte(admin.ID)) {
		t.Fatal("definition metadata/attribution missing")
	}
	assertPrivateAbsent(t, data, "PRIVATE_QUERY", "PRIVATE_HEADER", "PRIVATE_BODY", "private-path")
	for _, query := range []string{"?actor_id=" + admin.ID, "?workspace_id=" + e.wid, "?scope=mine&scope=all", "?scope=unknown", "?search=" + strings.Repeat("a", 257)} {
		response, _ = e.apiRequest(t, "GET", "/api/tests"+query, "", operator, nil)
		if response.StatusCode != 400 {
			t.Fatal("invalid/identity-selecting filter accepted")
		}
	}
	response, _ = e.apiRequest(t, "POST", "/api/tests", `{"name":"must not write"}`, adminCookie, nil)
	if response.StatusCode != 400 {
		t.Fatal("unclassified configuration write exposed")
	}
	for _, path := range []string{"/api/scenarios", "/api/tests/" + first.ID + "/config", "/api/tests/" + first.ID + "/export", "/download/config/" + first.ID, "/api/runs"} {
		response, _ = e.apiRequest(t, "GET", path, "", adminCookie, nil)
		if response.StatusCode != 404 {
			t.Fatal("raw/copy/execution endpoint exposed")
		}
	}
	other, _ := workspace.NewID()
	otherAdmin, err := e.db.Bootstrap(ctx, other, "Other", workspace.Identity{Issuer: e.config.Issuer, Subject: "other-admin", DisplayName: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := e.db.CreateDefinition(ctx, other, otherAdmin, json.RawMessage(`{"name":"Other workspace","url":"https://target.example"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, _ = e.apiRequest(t, "GET", "/api/tests/"+hidden.ID, "", adminCookie, nil)
	if response.StatusCode != 404 {
		t.Fatal("definition crossed workspace boundary")
	}
	if _, err = e.db.SetMember(ctx, e.wid, admin.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "viewer", DisplayName: "Reviewer"}, "viewer", false); err != nil {
		t.Fatal(err)
	}
	response, _ = e.apiRequest(t, "GET", "/api/tests", "", viewer, nil)
	if response.StatusCode != 401 {
		t.Fatal("disabled member retained API access")
	}
}
func TestPostgresTeamSecretAPIRequiresAdminCSRFAndRevision(t *testing.T) {
	e := environment(t)
	ctx := context.Background()
	admin := e.signIn(t)
	viewer, _ := e.memberSession(t, "viewer", "Reviewer", "viewer")
	operator, _ := e.memberSession(t, "operator", "Operator", "operator")
	create := `{"name":"Checkout credential","source_key":"PRIVATE_MOUNTED_FILENAME"}`
	for _, cookie := range []*http.Cookie{viewer, operator} {
		response, _ := e.apiRequest(t, "POST", "/api/secret-references", create, cookie, nil)
		if response.StatusCode != 403 {
			t.Fatal("non-admin created reference")
		}
	}
	for _, headers := range []map[string]string{{"X-Gometer-CSRF": ""}, {"Origin": ""}, {"Origin": "https://attacker.example"}, {"Sec-Fetch-Site": "cross-site"}} {
		response, _ := e.apiRequest(t, "POST", "/api/secret-references", create, admin, headers)
		if response.StatusCode != 403 {
			t.Fatal("unverified mutation accepted")
		}
	}
	for _, body := range []string{`{"name":"PRIVATE_VALUE","source_key":"token","value":"PRIVATE_VALUE"}`, `{"name":"Token","source_key":"../escape"}`, `{"name":"Token","source_key":"token"} {}`, strings.Repeat("a", 8193)} {
		response, data := e.apiRequest(t, "POST", "/api/secret-references", body, admin, nil)
		if response.StatusCode != 400 {
			t.Fatal("invalid secret payload accepted")
		}
		assertPrivateAbsent(t, data, "PRIVATE_VALUE", "../escape")
	}
	response, data := e.apiRequest(t, "POST", "/api/secret-references", create, admin, nil)
	if response.StatusCode != 201 {
		t.Fatalf("create: %d %s", response.StatusCode, data)
	}
	var ref workspace.SecretReference
	if json.Unmarshal(data, &ref) != nil || ref.Revision != 1 || ref.ID == "" {
		t.Fatal("new reference metadata missing")
	}
	tag := response.Header.Get("ETag")
	path := response.Header.Get("Location")
	if tag == "" || path != "/api/secret-references/"+ref.ID {
		t.Fatal("reference location or revision missing")
	}
	assertPrivateAbsent(t, data, "PRIVATE_MOUNTED_FILENAME", "source_key")
	for _, cookie := range []*http.Cookie{viewer, operator, admin} {
		for _, location := range []string{"/api/secret-references", path} {
			response, data = e.apiRequest(t, "GET", location, "", cookie, nil)
			if response.StatusCode != 200 || !bytes.Contains(data, []byte(ref.ID)) {
				t.Fatal("reference metadata unavailable to member")
			}
			assertPrivateAbsent(t, data, "PRIVATE_MOUNTED_FILENAME", "source_key")
		}
	}
	update := `{"name":"Checkout credential","enabled":false}`
	response, _ = e.apiRequest(t, "PUT", path, update, admin, nil)
	if response.StatusCode != 428 {
		t.Fatal("update without base revision accepted")
	}
	for _, invalid := range []string{"*", "W/" + tag, strings.Replace(tag, "/1", "/01", 1), `"secret/00000000000000000000000000000000/1"`} {
		response, _ = e.apiRequest(t, "PUT", path, update, admin, map[string]string{"If-Match": invalid})
		if response.StatusCode != 400 {
			t.Fatal("invalid/weak reference ETag accepted")
		}
	}
	response, _ = e.apiRequest(t, "PUT", path, update, operator, map[string]string{"If-Match": tag})
	if response.StatusCode != 403 {
		t.Fatal("operator retargeted reference")
	}
	response, data = e.apiRequest(t, "PUT", path, update, admin, map[string]string{"If-Match": tag})
	if response.StatusCode != 200 {
		t.Fatalf("update: %d %s", response.StatusCode, data)
	}
	assertPrivateAbsent(t, data, "PRIVATE_MOUNTED_FILENAME", "PRIVATE_ROTATED_FILENAME", "source_key")
	var changed workspace.SecretReference
	json.Unmarshal(data, &changed)
	if changed.Revision != 2 || changed.Enabled || changed.ID != ref.ID {
		t.Fatal("reference update identity/state invalid")
	}
	response, _ = e.apiRequest(t, "PUT", path, update, admin, map[string]string{"If-Match": tag})
	if response.StatusCode != 409 {
		t.Fatal("stale catalog update accepted")
	}
	for _, headers := range []map[string]string{{"If-Match": response.Header.Get("ETag"), "Origin": ""}, {"If-Match": tag, "X-Gometer-CSRF": ""}} {
		response, _ = e.apiRequest(t, "PUT", path, update, admin, headers)
		if response.StatusCode != 403 {
			t.Fatal("PUT bypassed origin/CSRF checks")
		}
	}
	response, data = e.apiRequest(t, "GET", path, "", admin, nil)
	response, data = e.apiRequest(t, "PUT", path, `{"name":"Checkout credential","enabled":true}`, admin, map[string]string{"If-Match": response.Header.Get("ETag")})
	if response.StatusCode != 200 {
		t.Fatal("admin could not reenable without reading the private source")
	}
	var restored workspace.SecretReference
	json.Unmarshal(data, &restored)
	member, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	if key, err := e.db.SecretSourceKey(ctx, e.wid, member.ID, ref.ID, restored.Revision); err != nil || key != "PRIVATE_MOUNTED_FILENAME" {
		t.Fatal("metadata update changed hidden source binding")
	}
	current, err := e.db.ResolveMember(ctx, e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.SetMember(ctx, e.wid, current.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "other-admin", DisplayName: "Other"}, "admin", true); err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.SetMember(ctx, e.wid, current.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "alice-subject", DisplayName: "Alice Team"}, "operator", true); err != nil {
		t.Fatal(err)
	}
	response, _ = e.apiRequest(t, "POST", "/api/secret-references", `{"name":"Cannot promote","source_key":"token"}`, admin, nil)
	if response.StatusCode != 403 {
		t.Fatal("stale browser admin role retained catalog write access")
	}
}
