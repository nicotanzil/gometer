package distributed

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckWorkerIsAuthenticatedReadOnlyObservation(t *testing.T) {
	token := strings.Repeat("t", 32)
	for _, tc := range []struct {
		name, state string
		status      int
		body        string
	}{
		{"idle", "reachable", 200, `{"protocol":1,"name":"east","now":"2026-10-23T00:00:00Z","busy":false}`},
		{"busy", "busy", 200, `{"protocol":1,"name":"east","now":"2026-10-23T00:00:00Z","busy":true}`},
		{"unauthorized", "authentication_failed", 401, token},
		{"forbidden", "authentication_failed", 403, token},
		{"protocol", "incompatible", 200, `{"protocol":2,"name":"east","now":"2026-10-23T00:00:00Z","busy":false}`},
		{"malformed", "incompatible", 200, `{"protocol":1}`},
		{"missing-busy", "incompatible", 200, `{"protocol":1,"name":"east","now":"2026-10-23T00:00:00Z"}`},
		{"extra", "incompatible", 200, `{"protocol":1,"name":"east","now":"2026-10-23T00:00:00Z","busy":false,"token":"secret"}`},
		{"trailing", "incompatible", 200, `{} {}`},
		{"oversized", "incompatible", 200, strings.Repeat("x", (64<<10)+1)},
		{"other-service", "incompatible", 404, token},
		{"redirect", "incompatible", 302, token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/info" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Origin") != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			before := time.Now()
			got := CheckWorker(context.Background(), server.URL, Options{Token: token})
			if got.State != tc.state || got.CheckedAt.Before(before) || got.CheckedAt.After(time.Now()) || calls.Load() != 1 {
				t.Fatalf("check: %+v, calls: %d", got, calls.Load())
			}
			data, _ := json.Marshal(got)
			if strings.Contains(string(data), token) {
				t.Fatal("token exposed")
			}
		})
	}
}

func TestCheckWorkerUsesVerifiedTLSAndStartupPolicy(t *testing.T) {
	token := strings.Repeat("t", 32)
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(Info{Protocol: Protocol, Name: "TLS fixture", Now: time.Now()})
	}))
	defer server.Close()
	if got := CheckWorker(context.Background(), server.URL, Options{Token: token}); got.State != "transport_failed" {
		t.Fatalf("untrusted TLS accepted: %+v", got)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	ca := filepath.Join(t.TempDir(), "worker-ca.pem")
	if err := os.WriteFile(ca, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if got := CheckWorker(context.Background(), server.URL, Options{Token: token, CAFile: ca}); got.State != "reachable" {
		t.Fatalf("private CA: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("TLS calls: %d", calls.Load())
	}
	if got := CheckWorker(context.Background(), server.URL, Options{}); got.State != "authentication_failed" {
		t.Fatalf("missing token: %+v", got)
	}
	if got := CheckWorker(context.Background(), "http://localhost:1", Options{Token: token}); got.State != "transport_failed" {
		t.Fatalf("remote HTTP policy: %+v", got)
	}
	if got := CheckWorker(context.Background(), "http://localhost:1", Options{Token: token, AllowHTTPWorkers: true}); got.State != "unreachable" {
		t.Fatalf("HTTP opt-in: %+v", got)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + ln.Addr().String()
	ln.Close()
	if got := CheckWorker(context.Background(), origin, Options{Token: token}); got.State != "unreachable" {
		t.Fatalf("closed listener: %+v", got)
	}
}
