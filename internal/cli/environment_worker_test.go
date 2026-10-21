package cli

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentWorkerStartupAndFlagPrecedence(t *testing.T) {
	const token = "environment-worker-test-token-01234567890123456789"
	for _, tc := range []struct {
		name, listen, workerName, wantName string
		args                               []string
	}{
		{"environment", "127.0.0.1:0", "Environment worker", "Environment worker", nil},
		{"explicit flags", "invalid-listener", "Environment worker", "Flag worker", []string{"-listen", "127.0.0.1:0", "-name", "Flag worker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMETER_WORKER_LISTEN", tc.listen)
			t.Setenv("GOMETER_WORKER_NAME", tc.workerName)
			t.Setenv("GOMETER_WORKER_TOKEN", token)
			lines, _ := serveCLI(t, append([]string{"worker"}, tc.args...)...)
			line := startupLine(t, lines, "GoMeter worker ")
			fields := strings.Fields(line)
			address := fields[len(fields)-1]
			parsed, err := url.Parse(address)
			if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "8801" {
				t.Fatalf("worker did not use the isolated listener: %s", address)
			}
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Get(address + "/v1/info")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("worker control accepted an unauthenticated request")
			}
			request, _ := http.NewRequest("GET", address+"/v1/info", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var info struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || info.Name != tc.wantName {
				t.Fatal("authenticated worker did not return the selected display name")
			}
		})
	}
}

func TestEnvironmentWorkerTLSValidation(t *testing.T) {
	for _, tc := range []struct{ name, listen, cert, key, want string }{
		{"remote requires TLS", "0.0.0.0:0", "", "", "non-loopback workers require"},
		{"certificate needs key", "127.0.0.1:0", "missing.crt", "", "require -tls-cert and -tls-key"},
		{"invalid certificate files", "127.0.0.1:0", "missing.crt", "missing.key", "load worker TLS certificate"},
		{"empty listener is explicit", "", "", "", "listen address must include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("GOMETER_WORKER_LISTEN", tc.listen)
			t.Setenv("GOMETER_WORKER_TLS_CERT", tc.cert)
			t.Setenv("GOMETER_WORKER_TLS_KEY", tc.key)
			t.Setenv("GOMETER_WORKER_TOKEN", "environment-test-worker-token-01234567890123456789")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var out bytes.Buffer
			code := Run(ctx, []string{"worker"}, &out, &out)
			if code != 2 || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("worker setup: exit=%d output=%s", code, &out)
			}
		})
	}
}

func TestEnvironmentWorkerServesTLSAndCanClearTLSWithFlags(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	defer fixture.Close()
	certificate := fixture.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "worker.crt"), filepath.Join(dir, "worker.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	const token = "environment-test-tls-token-01234567890123456789"
	t.Setenv("GOMETER_WORKER_LISTEN", "127.0.0.1:0")
	t.Setenv("GOMETER_WORKER_NAME", "TLS worker")
	t.Setenv("GOMETER_WORKER_TLS_CERT", certPath)
	t.Setenv("GOMETER_WORKER_TLS_KEY", keyPath)
	t.Setenv("GOMETER_WORKER_TOKEN", token)
	lines, stop := serveCLI(t, "worker")
	address := startupLine(t, lines, "GoMeter worker TLS worker: ")
	if !strings.HasPrefix(address, "https://") {
		t.Fatal("worker did not enable TLS from environment")
	}
	client := fixture.Client()
	client.Timeout = 3 * time.Second
	request, _ := http.NewRequest("GET", address+"/v1/info", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("TLS worker response: %d", response.StatusCode)
	}
	stop()
	lines, _ = serveCLI(t, "worker", "-tls-cert", "", "-tls-key", "")
	if address := startupLine(t, lines, "GoMeter worker TLS worker: "); !strings.HasPrefix(address, "http://127.0.0.1:") {
		t.Fatal("explicit empty flags did not clear environment TLS paths")
	}
}
