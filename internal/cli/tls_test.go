package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gometer/internal/engine"
)

func TestInsecureFlagOverridesConfigAndLabelsReports(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(scenario, []byte(fmt.Sprintf(`{"url":%q,"insecure":true,"mode":"rate","rate":1,"duration":"40ms"}`, target.URL)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"-insecure", "-insecure=false"} {
		var out, logs bytes.Buffer
		jsonPath, htmlPath := filepath.Join(dir, "result.json"), filepath.Join(dir, "result.html")
		code := Run(context.Background(), []string{"run", "-config", scenario, flag, "-quiet", "-out", jsonPath, "-html", htmlPath}, &out, &logs)
		enabled := flag == "-insecure"
		if (enabled && code != 0) || (!enabled && code != 1) {
			t.Fatalf("%s exit %d: %s %s", flag, code, &out, &logs)
		}
		data, err := os.ReadFile(jsonPath)
		if err != nil {
			t.Fatal(err)
		}
		var result engine.Result
		if err := json.Unmarshal(data, &result); err != nil || result.Insecure != enabled {
			t.Fatalf("report TLS setting: %v", err)
		}
		html, err := os.ReadFile(htmlPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{out.String(), logs.String(), string(html)} {
			if strings.Contains(text, "TLS verification disabled") != enabled {
				t.Fatalf("TLS labeling incorrect: %s", text)
			}
		}
	}
}
