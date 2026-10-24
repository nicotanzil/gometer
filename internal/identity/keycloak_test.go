package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type fixtureCode struct {
	claims       map[string]any
	challenge    string
	badSignature bool
}
type keycloakFixture struct {
	server   *httptest.Server
	config   Config
	key      *rsa.PrivateKey
	mu       sync.Mutex
	codes    map[string]fixtureCode
	calls    atomic.Int64
	metadata map[string]any
}

func fixture(t *testing.T) *keycloakFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &keycloakFixture{key: key, codes: map[string]fixtureCode{}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	f.server = server
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "keycloak-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	f.config = Config{Issuer: server.URL + "/realms/gometer", ClientID: "gometer", ClientSecret: "FIXTURE_CLIENT_SECRET", RedirectURI: "https://gometer.example/auth/callback", CAFile: ca}
	f.metadata = map[string]any{"issuer": f.config.Issuer, "authorization_endpoint": f.config.Issuer + "/protocol/openid-connect/auth", "token_endpoint": f.config.Issuer + "/protocol/openid-connect/token", "jwks_uri": f.config.Issuer + "/protocol/openid-connect/certs", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}}
	return f
}
func (f *keycloakFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		json.NewEncoder(w).Encode(f.metadata)
	case strings.HasSuffix(r.URL.Path, "/certs"):
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
	case strings.HasSuffix(r.URL.Path, "/token"):
		f.calls.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != f.config.ClientID || password != f.config.ClientSecret || r.Method != "POST" {
			http.Error(w, "fixture authentication failure", 401)
			return
		}
		if r.ParseForm() != nil || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != f.config.RedirectURI {
			http.Error(w, "bad flow", 400)
			return
		}
		f.mu.Lock()
		code, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(hash[:]) != code.challenge {
			http.Error(w, "invalid code or PKCE", 400)
			return
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture", "typ": "JWT"})
		claims, _ := json.Marshal(code.claims)
		payload := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		digest := sha256.Sum256([]byte(payload))
		signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
		if err != nil {
			http.Error(w, "signing error", 500)
			return
		}
		if code.badSignature {
			signature[0] ^= 1
		}
		token := payload + "." + base64.RawURLEncoding.EncodeToString(signature)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "FIXTURE_ACCESS_TOKEN", "id_token": token, "refresh_token": "FIXTURE_REFRESH_TOKEN", "token_type": "Bearer", "expires_in": 120})
	default:
		http.NotFound(w, r)
	}
}
func (f *keycloakFixture) code(t *testing.T, login *Login, change func(map[string]any), badSignature bool) string {
	t.Helper()
	u, err := url.Parse(login.AuthorizationURL())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") != login.State() || q.Get("client_id") != f.config.ClientID || q.Get("redirect_uri") != f.config.RedirectURI {
		t.Fatal("authorization request does not protect state, nonce, PKCE or redirect")
	}
	claims := map[string]any{"iss": f.config.Issuer, "sub": "federated-subject-123", "aud": f.config.ClientID, "azp": f.config.ClientID, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": q.Get("nonce"), "name": "LDAP display name", "preferred_username": "ldap-user"}
	if change != nil {
		change(claims)
	}
	code := fmt.Sprint("fixture-", len(f.codes), "-", time.Now().UnixNano())
	f.mu.Lock()
	f.codes[code] = fixtureCode{claims: claims, challenge: q.Get("code_challenge"), badSignature: badSignature}
	f.mu.Unlock()
	return code
}
func TestKeycloakCodeFlowVerifiesIdentityAndRejectsReplay(t *testing.T) {
	f := fixture(t)
	provider, err := New(context.Background(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	login, err := provider.Begin()
	if err != nil {
		t.Fatal(err)
	}
	code := f.code(t, login, nil, false)
	principal, err := provider.Finish(context.Background(), login, login.State(), code)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Issuer != f.config.Issuer || principal.Subject != "federated-subject-123" || principal.DisplayName != "LDAP display name" || principal.ExpiresAt.Before(time.Now()) {
		t.Fatalf("identity: %+v", principal)
	}
	encoded, _ := json.Marshal(principal)
	for _, secret := range []string{"FIXTURE_ACCESS_TOKEN", "FIXTURE_REFRESH_TOKEN", "FIXTURE_CLIENT_SECRET"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("credentials retained in public identity")
		}
	}
	if _, err := provider.Finish(context.Background(), login, login.State(), code); !errors.Is(err, ErrLogin) {
		t.Fatal("flow replay accepted")
	}
	if f.calls.Load() != 1 {
		t.Fatal("replayed flow reached token endpoint")
	}
}
func TestKeycloakCodeFlowRejectsUntrustedClaims(t *testing.T) {
	f := fixture(t)
	provider, err := New(context.Background(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	for _, tc := range []struct {
		name         string
		change       func(map[string]any)
		badSignature bool
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://untrusted.example/realms/gometer" }, false},
		{"wrong audience", func(c map[string]any) { c["aud"] = "another-app" }, false},
		{"wrong authorized party", func(c map[string]any) { c["azp"] = "another-app" }, false},
		{"multiple audiences without authorized party", func(c map[string]any) { c["aud"] = []string{"gometer", "another-app"}; delete(c, "azp") }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, false},
		{"wrong nonce", func(c map[string]any) { c["nonce"] = "wrong" }, false},
		{"empty subject", func(c map[string]any) { c["sub"] = "" }, false},
		{"invalid signature", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			login, _ := provider.Begin()
			code := f.code(t, login, tc.change, tc.badSignature)
			_, err := provider.Finish(context.Background(), login, login.State(), code)
			if !errors.Is(err, ErrLogin) {
				t.Fatalf("untrusted claims accepted: %v", err)
			}
			if strings.Contains(err.Error(), "FIXTURE_") {
				t.Fatal("error reflected provider response")
			}
		})
	}
}
func TestKeycloakStateExpiryPKCEAndSingleUseConcurrency(t *testing.T) {
	f := fixture(t)
	provider, err := New(context.Background(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	login, _ := provider.Begin()
	code := f.code(t, login, nil, false)
	if _, err := provider.Finish(context.Background(), login, "wrong-state", code); !errors.Is(err, ErrLogin) || f.calls.Load() != 0 {
		t.Fatal("wrong state reached token endpoint")
	}
	login.expiresAt = time.Now().Add(-time.Second)
	if _, err := provider.Finish(context.Background(), login, login.State(), code); !errors.Is(err, ErrExpired) {
		t.Fatal("expired flow accepted")
	}
	login, _ = provider.Begin()
	code = f.code(t, login, nil, false)
	login.verifier = "wrong-verifier"
	if _, err := provider.Finish(context.Background(), login, login.State(), code); !errors.Is(err, ErrLogin) {
		t.Fatal("wrong PKCE accepted")
	}
	login, _ = provider.Begin()
	code = f.code(t, login, nil, false)
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	before := f.calls.Load()
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := provider.Finish(context.Background(), login, login.State(), code)
			outcomes <- err
		}()
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrLogin) {
			t.Fatal(err)
		}
	}
	if successes != 1 || f.calls.Load() != before+1 {
		t.Fatal("concurrent callback was exchanged more than once")
	}
}
func TestKeycloakDiscoveryRequiresTLSAndExactIssuer(t *testing.T) {
	f := fixture(t)
	untrusted := f.config
	untrusted.CAFile = ""
	if _, err := New(context.Background(), untrusted); !errors.Is(err, ErrConfiguration) {
		t.Fatal("untrusted provider TLS accepted")
	}
	bad := f.config
	bad.Issuer = strings.Replace(bad.Issuer, "https:", "http:", 1)
	if _, err := New(context.Background(), bad); !errors.Is(err, ErrConfiguration) {
		t.Fatal("HTTP issuer accepted")
	}
	f.metadata["issuer"] = "https://another.example/realms/gometer"
	if _, err := New(context.Background(), f.config); !errors.Is(err, ErrConfiguration) {
		t.Fatal("mismatched discovery issuer accepted")
	}
	f.metadata["issuer"] = f.config.Issuer
	f.metadata["token_endpoint"] = "http://127.0.0.1/token"
	if _, err := New(context.Background(), f.config); !errors.Is(err, ErrConfiguration) {
		t.Fatal("insecure token endpoint accepted")
	}
}

func TestKeycloakTokenRedirectDoesNotForwardCredentials(t *testing.T) {
	f := fixture(t)
	var captured atomic.Int64
	// Even a redirect to a trusted origin must not be followed.
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			http.Redirect(w, r, f.server.URL+"/captured", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/captured" {
			captured.Add(1)
			http.Error(w, "unexpected forwarded credential", 500)
			return
		}
		f.serve(w, r)
	})
	provider, err := New(context.Background(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	login, _ := provider.Begin()
	code := f.code(t, login, nil, false)
	if _, err := provider.Finish(context.Background(), login, login.State(), code); !errors.Is(err, ErrLogin) {
		t.Fatal("redirected token exchange accepted")
	}
	if captured.Load() != 0 {
		t.Fatal("token exchange followed a redirect")
	}
}
