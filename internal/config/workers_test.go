package config

import "testing"

func TestWorkerConfigurationAndSecureOrigins(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:8801", "http://[::1]:8801/", "https://load.example:9443"} {
		if err := WorkerURL(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://localhost:8801", "http://192.0.2.1:8801", "https://user:secret@worker.example", "https://worker.example/path", "https://worker.example?q=secret", "https://worker.example/#fragment", "https://worker.example:99999"} {
		if err := WorkerURL(raw); err == nil {
			t.Fatalf("unsafe control URL accepted: %s", raw)
		}
	}
	c := Defaults()
	c.URL = "http://127.0.0.1/work"
	c.Workers = []string{"http://127.0.0.1:8801"}
	if _, err := c.Validate(); err == nil {
		t.Fatal("distributed users mode accepted")
	}
	c.Mode = "rate"
	c.Rate = 10
	if _, err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Workers = append(c.Workers, c.Workers[0]+"/")
	if _, err := c.Validate(); err == nil {
		t.Fatal("duplicate worker origin accepted")
	}
	c.Workers = []string{"http://127.0.0.1:8801", "http://127.0.0.1:8802"}
	c.MaxInFlight = 1
	if _, err := c.Validate(); err == nil {
		t.Fatal("zero-capacity assignment accepted")
	}
}

func TestScenarioStrideMatchesGlobalOrdinals(t *testing.T) {
	three, one := 3, 1
	c := Defaults()
	c.URL = "http://127.0.0.1"
	c.Scenarios = []Scenario{{Name: "A", Weight: &three, Steps: []Step{{URL: "/a"}}}, {Name: "B", Weight: &one, Steps: []Step{{URL: "/b"}}}}
	p, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	for stride := int64(1); stride <= 32; stride++ {
		for start := int64(0); start < 8; start++ {
			for _, n := range []int64{0, 1, 2, 5, 37, 1001} {
				want := []int64{0, 0}
				for i := int64(0); i < n; i++ {
					want[p.ScenarioAt(start+i*stride)]++
				}
				got := p.ScenarioStride(start, n, stride)
				if got[0] != want[0] || got[1] != want[1] {
					t.Fatalf("start %d count %d stride %d: %v != %v", start, n, stride, got, want)
				}
			}
		}
	}
	got := p.ScenarioStride(3, 1e12, 32)
	if got[0] != 0 || got[1] != 1e12 {
		t.Fatalf("large skipped range lost assigned scenario: %v", got)
	}
}

func TestWorkerOriginsHaveStableCanonicalIdentity(t *testing.T) {
	for _, group := range [][]string{
		{"https://worker.example", "https://WORKER.example:443/", "https://worker.example.:0443"},
		{"http://127.0.0.1", "http://127.0.0.1:80/", "http://[::ffff:127.0.0.1]:080/"},
		{"https://[::1]:9443", "https://[0:0:0:0:0:0:0:1]:09443/"},
	} {
		for _, origin := range group {
			key, err := NormalizeWorkerOrigin(origin)
			if err != nil || key != group[0] {
				t.Fatalf("canonical %q: %q %v", origin, key, err)
			}
		}
	}
}
