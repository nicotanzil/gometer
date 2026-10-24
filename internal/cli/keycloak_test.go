package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkspaceKeycloakDiscoveryCommand(t *testing.T) {
	var calls atomic.Int64
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/realms/team/.well-known/openid-configuration" {
			t.Errorf("unexpected OIDC request: %s %s", r.Method, r.URL.Path)
		}
		issuer := server.URL + "/realms/team"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/auth", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/certs", "id_token_signing_alg_values_supported": []string{"RS256"}})
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "issuer-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMETER_DATABASE_URL", "")
	t.Setenv("GOMETER_OIDC_ISSUER", "https://unused.invalid/realms/unused")
	t.Setenv("GOMETER_OIDC_CLIENT_ID", "gometer")
	t.Setenv("GOMETER_OIDC_REDIRECT_URI", "https://gometer.example/auth/callback")
	t.Setenv("GOMETER_OIDC_CLIENT_SECRET", "FIXTURE_PRIVATE_CLIENT_SECRET")
	t.Setenv("GOMETER_OIDC_CA", ca)
	var out, errOut bytes.Buffer
	args := []string{"workspace", "oidc-check", "-issuer", server.URL + "/realms/team"}
	if code := Run(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("discovery: %d %s", code, errOut.String())
	}
	if calls.Load() != 1 || !strings.Contains(out.String(), "discovery and TLS checks passed") {
		t.Fatal("discovery check did not remain read-only")
	}
	if strings.Contains(out.String()+errOut.String(), "FIXTURE_PRIVATE_CLIENT_SECRET") {
		t.Fatal("client credential printed")
	}
	t.Setenv("GOMETER_OIDC_CLIENT_SECRET", "")
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), args, &out, &errOut); code != 2 || calls.Load() != 1 {
		t.Fatal("incomplete OIDC settings sent a request")
	}
}
