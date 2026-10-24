package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"

	"gometer/internal/workspace"
	"strings"
	"testing"
)

func TestWorkspaceServeInputDoesNotExposeSecrets(t *testing.T) {
	t.Setenv("GOMETER_DATABASE_URL", "postgres://PRIVATEUSER:PRIVATEPASSWORD@remote.example/db?sslmode=disable")
	t.Setenv("GOMETER_LOGIN_KEY", "PRIVATE_INVALID_KEY")
	t.Setenv("GOMETER_OIDC_CLIENT_SECRET", "PRIVATE_CLIENT_SECRET")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"workspace", "serve", "-help"}, &out, &errOut); code != 0 {
		t.Fatal("help required runtime credentials")
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"workspace", "serve"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "GOMETER_LOGIN_KEY") {
		t.Fatal("missing configuration not explained")
	}
	t.Setenv("GOMETER_LOGIN_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv("GOMETER_WORKSPACE_ID", strings.Repeat("1", 32))
	t.Setenv("GOMETER_TEAM_PUBLIC_ORIGIN", "https://gometer.example")
	t.Setenv("GOMETER_OIDC_REDIRECT_URI", "https://gometer.example/auth/callback")
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"workspace", "serve", "-redirect-uri", "https://attacker.example/callback"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "matching that origin") {
		t.Fatal("mismatched callback accepted")
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"workspace", "serve"}, &out, &errOut); code != 2 {
		t.Fatal("unsafe remote database accepted")
	}
	for _, secret := range []string{"PRIVATEUSER", "PRIVATEPASSWORD", "PRIVATE_CLIENT_SECRET", "PRIVATE_INVALID_KEY"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Fatal("startup printed credentials")
		}
	}
}

func TestPostgresWorkspaceServeStartsWithEnvironment(t *testing.T) {
	dsn := os.Getenv("GOMETER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL fixture required")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Hostname() != "127.0.0.1" || !strings.HasPrefix(parsed.Path, "/gometer_test_") {
		t.Fatal("refusing a non-fixture database")
	}
	ctx := context.Background()
	db, err := workspace.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var discovery atomic.Int64
	var oidc *httptest.Server
	oidc = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discovery.Add(1)
		if r.URL.Path != "/realms/team/.well-known/openid-configuration" {
			t.Errorf("unexpected provider request: %s", r.URL.Path)
		}
		issuer := oidc.URL + "/realms/team"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/auth", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/certs", "id_token_signing_alg_values_supported": []string{"RS256"}})
	}))
	defer oidc.Close()
	wid, _ := workspace.NewID()
	if _, err = db.Bootstrap(ctx, wid, "Startup fixture", workspace.Identity{Issuer: oidc.URL + "/realms/team", Subject: "initial-admin", DisplayName: "Fixture Admin"}); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "oidc-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oidc.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GOMETER_DATABASE_URL": dsn, "GOMETER_WORKSPACE_ID": wid,
		"GOMETER_TEAM_LISTEN": "127.0.0.1:0", "GOMETER_TEAM_PUBLIC_ORIGIN": "https://team.example",
		"GOMETER_LOGIN_KEY":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)),
		"GOMETER_OIDC_ISSUER": oidc.URL + "/realms/team", "GOMETER_OIDC_CLIENT_ID": "gometer",
		"GOMETER_OIDC_CLIENT_SECRET": "FIXTURE_CLIENT_SECRET", "GOMETER_OIDC_REDIRECT_URI": "https://team.example/auth/callback", "GOMETER_OIDC_CA": ca,
	} {
		t.Setenv(key, value)
	}
	lines, stop := serveCLI(t, "workspace", "serve")
	address := startupLine(t, lines, "Team listener: ")
	request, _ := http.NewRequest("GET", "http://"+address+"/", nil)
	request.Host = "team.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Referrer-Policy") != "same-origin" {
		t.Fatal("configured team origin did not serve sign-in")
	}
	request, _ = http.NewRequest("GET", "http://"+address+"/", nil)
	request.Header.Set("X-Forwarded-Host", "team.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("forwarded header selected a trusted origin")
	}
	stop()
	if discovery.Load() != 1 {
		t.Fatal("startup performed more than discovery")
	}
}
