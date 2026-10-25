package team

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
	"gometer/internal/identity"
	"gometer/internal/workspace"
)

type codeRecord struct {
	challenge string
	claims    map[string]any
}
type testProvider struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	callback string
	mu       sync.Mutex
	codes    map[string]codeRecord
	subject  string
	calls    atomic.Int64
}

func (p *testProvider) serve(w http.ResponseWriter, r *http.Request) {
	issuer := p.server.URL + "/realms/team"
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/realms/team/.well-known/openid-configuration":
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/auth", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/certs", "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/realms/team/certs":
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
	case "/realms/team/auth":
		q := r.URL.Query()
		if q.Get("redirect_uri") != p.callback || q.Get("client_id") != "gometer" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "invalid authorization fixture", 400)
			return
		}
		code, _ := workspace.NewBrowserToken()
		p.mu.Lock()
		p.codes[code] = codeRecord{q.Get("code_challenge"), map[string]any{"iss": issuer, "sub": p.subject, "aud": "gometer", "azp": "gometer", "nonce": q.Get("nonce"), "name": "Alice Team", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix()}}
		p.mu.Unlock()
		http.Redirect(w, r, p.callback+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusSeeOther)
	case "/realms/team/token":
		p.calls.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "gometer" || password != "FIXTURE_CLIENT_SECRET" || r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("redirect_uri") != p.callback {
			http.Error(w, "invalid token fixture", 401)
			return
		}
		p.mu.Lock()
		record, exists := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !exists || base64.RawURLEncoding.EncodeToString(hash[:]) != record.challenge {
			http.Error(w, "invalid PKCE fixture", 400)
			return
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture"})
		payload, _ := json.Marshal(record.claims)
		message := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
		digest := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
		if err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id_token": message + "." + base64.RawURLEncoding.EncodeToString(signature), "access_token": "FIXTURE_ACCESS_TOKEN", "refresh_token": "FIXTURE_REFRESH_TOKEN", "token_type": "Bearer", "expires_in": 300})
	default:
		http.NotFound(w, r)
	}
}

type testEnvironment struct {
	app     *httptest.Server
	client  *http.Client
	db      *workspace.Database
	oidc    *testProvider
	config  identity.Config
	key     []byte
	wid     string
	handler atomic.Pointer[Server]
}

func environment(t *testing.T) *testEnvironment {
	t.Helper()
	dsn := os.Getenv("GOMETER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || !strings.HasPrefix(u.Path, "/gometer_test_") {
		t.Fatal("refusing a non-fixture database")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	defer connection.Close(ctx)
	if _, err = connection.Exec(ctx, `DROP SCHEMA IF EXISTS gometer CASCADE`); err != nil {
		t.Fatal("fixture reset failed")
	}
	db, err := workspace.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := &testEnvironment{db: db, key: bytes.Repeat([]byte{9}, 32)}
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("GOMETER_TEAM_HTTP_TRACE") == "1" {
			t.Logf("browser request: %s %s origin=%q site=%q mode=%q dest=%q", r.Method, r.URL.Path, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Sec-Fetch-Mode"), r.Header.Get("Sec-Fetch-Dest"))
		}
		e.handler.Load().ServeHTTP(w, r)
	}))
	e.app = app
	t.Cleanup(app.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &testProvider{key: key, callback: app.URL + CallbackPath, codes: map[string]codeRecord{}, subject: "alice-subject"}
	p.server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	e.oidc = p
	ca := filepath.Join(t.TempDir(), "oidc-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	e.config = identity.Config{Issuer: p.server.URL + "/realms/team", ClientID: "gometer", ClientSecret: "FIXTURE_CLIENT_SECRET", RedirectURI: p.callback, CAFile: ca}
	e.wid, _ = workspace.NewID()
	if _, err = db.Bootstrap(ctx, e.wid, "Fixture team", workspace.Identity{Issuer: e.config.Issuer, Subject: "alice-subject", DisplayName: "Alice Team"}); err != nil {
		t.Fatal(err)
	}
	e.restart(t)
	e.client = app.Client()
	e.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return e
}
func (e *testEnvironment) restart(t *testing.T) {
	t.Helper()
	db, err := workspace.Open(context.Background(), os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	provider, err := identity.New(context.Background(), e.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)
	handler, err := New(db, provider, Options{Origin: e.app.URL, WorkspaceID: e.wid, LoginKey: e.key})
	if err != nil {
		t.Fatal(err)
	}
	e.handler.Store(handler)
}
func (e *testEnvironment) request(t *testing.T, method, path, body string, cookies ...*http.Cookie) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, e.app.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if method == "POST" {
		request.Header.Set("Origin", e.app.URL)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if path != "/" && strings.HasPrefix(path, CallbackPath) {
		request.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := e.client.Do(request)
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
func findCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}
func (e *testEnvironment) start(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	response, _ := e.request(t, "POST", "/auth/login", "")
	if response.StatusCode != 303 {
		t.Fatalf("begin: %d", response.StatusCode)
	}
	cookie := findCookie(t, response, loginCookie)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatal("insecure login cookie")
	}
	client := e.oidc.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	authorized, err := client.Get(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorized.Body.Close()
	callback, err := url.Parse(authorized.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return cookie, callback.RequestURI()
}
func (e *testEnvironment) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	browser, callback := e.start(t)
	response, data := e.request(t, "GET", callback, "", browser)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/" {
		t.Fatalf("callback: %d %s", response.StatusCode, data)
	}
	cookie := findCookie(t, response, sessionCookie)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" {
		t.Fatal("insecure session cookie")
	}
	return cookie
}

func TestPostgresTeamLoginSurvivesInstancesAndLogoutRequiresCSRF(t *testing.T) {
	e := environment(t)
	browser, callback := e.start(t)
	e.restart(t)
	response, data := e.request(t, "GET", callback, "", browser)
	if response.StatusCode != 303 {
		t.Fatalf("restored callback: %d %s", response.StatusCode, data)
	}
	cookie := findCookie(t, response, sessionCookie)
	e.restart(t)
	response, data = e.request(t, "GET", "/api/session", "", cookie)
	if response.StatusCode != 200 {
		t.Fatalf("session: %d %s", response.StatusCode, data)
	}
	var session struct {
		workspace.Session
		CSRF string `json:"csrf_token"`
	}
	if json.Unmarshal(data, &session) != nil || session.Member.Role != "admin" || session.CSRF == "" {
		t.Fatal("session metadata missing")
	}
	for _, secret := range []string{cookie.Value, "FIXTURE_CLIENT_SECRET", "FIXTURE_ACCESS_TOKEN", "FIXTURE_REFRESH_TOKEN"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("credentials leaked in session metadata")
		}
	}
	for _, path := range []string{"/api/state", "/api/tests/00000000000000000000000000000000/config", "/api/scenarios", "/download/config/00000000000000000000000000000000"} {
		response, _ = e.request(t, "GET", path, "", cookie)
		if response.StatusCode != 404 {
			t.Fatal("personal or unimplemented team API exposed")
		}
	}
	response, _ = e.request(t, "POST", "/auth/logout", "csrf_token=wrong", cookie)
	if response.StatusCode != 403 {
		t.Fatal("logout accepted bad CSRF")
	}
	response, _ = e.request(t, "GET", "/api/session", "", cookie)
	if response.StatusCode != 200 {
		t.Fatal("failed CSRF changed session")
	}
	response, _ = e.request(t, "POST", "/auth/logout", url.Values{"csrf_token": {session.CSRF}}.Encode(), cookie)
	if response.StatusCode != 303 || findCookie(t, response, sessionCookie).MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	response, _ = e.request(t, "GET", "/api/session", "", cookie)
	if response.StatusCode != 401 {
		t.Fatal("revoked session reused")
	}
}
func TestPostgresTeamCallbackRequiresBrowserStateAndSingleConsumption(t *testing.T) {
	e := environment(t)
	browser, callback := e.start(t)
	response, _ := e.request(t, "GET", callback, "")
	if response.StatusCode != 400 || e.oidc.calls.Load() != 0 {
		t.Fatal("callback without initiating browser exchanged code")
	}
	// Missing browser cookie must not consume a different browser's flow.
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, _ := e.request(t, "GET", callback, "", browser)
			statuses <- response.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == 303 {
			successes++
		} else if status != 400 {
			t.Fatalf("callback status: %d", status)
		}
	}
	if successes != 1 || e.oidc.calls.Load() != 1 {
		t.Fatal("callback exchanged code more than once")
	}
	browser, callback = e.start(t)
	q, _ := url.Parse(callback)
	values := q.Query()
	values.Set("state", "wrong")
	q.RawQuery = values.Encode()
	response, _ = e.request(t, "GET", q.RequestURI(), "", browser)
	if response.StatusCode != 400 || e.oidc.calls.Load() != 1 {
		t.Fatal("wrong state exchanged code")
	}
	response, _ = e.request(t, "GET", callback, "", browser)
	if response.StatusCode != 400 {
		t.Fatal("failed flow replayed")
	}
}
func TestPostgresTeamRejectsForgedOriginsAndUnregisteredAccounts(t *testing.T) {
	e := environment(t)
	for _, tc := range []struct{ origin, host, site string }{{"https://attacker.example", "", ""}, {"", "", ""}, {e.app.URL, "attacker.example", ""}, {e.app.URL, "", "cross-site"}} {
		request, _ := http.NewRequest("POST", e.app.URL+"/auth/login", nil)
		request.Header.Set("Origin", tc.origin)
		request.Header.Set("Sec-Fetch-Site", tc.site)
		if tc.host != "" {
			request.Host = tc.host
		}
		response, err := e.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("forged origin/host accepted")
		}
	}
	cookie := e.signIn(t)
	e.oidc.mu.Lock()
	e.oidc.subject = "unknown-directory-user"
	e.oidc.mu.Unlock()
	browser, callback := e.start(t)
	response, _ := e.request(t, "GET", callback, "", browser)
	if response.StatusCode != 403 {
		t.Fatal("unknown directory account auto-enrolled")
	}
	for _, c := range response.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("unknown member received session")
		}
	}
	member, err := e.db.ResolveMember(context.Background(), e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.SetMember(context.Background(), e.wid, member.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "second-admin", DisplayName: "Second"}, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.SetMember(context.Background(), e.wid, member.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "alice-subject", DisplayName: "Alice Team"}, "operator", false)
	if err != nil {
		t.Fatal(err)
	}
	response, _ = e.request(t, "GET", "/api/session", "", cookie)
	if response.StatusCode != 401 {
		t.Fatal("disabled member retained browser access")
	}
}
func TestPostgresTeamBrowser(t *testing.T) {
	if os.Getenv("GOMETER_TEAM_BROWSER") != "1" {
		t.Skip("optional Playwright verification")
	}
	e := environment(t)
	admin, err := e.db.ResolveMember(context.Background(), e.wid, e.config.Issuer, "alice-subject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.SetMember(context.Background(), e.wid, admin.ID, workspace.Identity{Issuer: e.config.Issuer, Subject: "browser-viewer", DisplayName: "Browser viewer"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	viewerToken, _, err := e.db.NewSession(context.Background(), e.wid, e.config.Issuer, "browser-viewer", time.Now().Add(5*time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "../../scripts/test-team-browser.cjs")
	command.Env = append(os.Environ(), "GOMETER_TEAM_FIXTURE_URL="+e.app.URL, "GOMETER_TEAM_VIEWER_TOKEN="+viewerToken)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	if e.oidc.calls.Load() != 1 {
		t.Fatal("browser did not complete exactly one OIDC exchange")
	}
}
