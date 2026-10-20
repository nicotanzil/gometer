package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func pointer[T any](v T) *T { return &v }
func journeyConfig(target string, steps ...config.Step) config.Config {
	c := testConfig(target)
	c.Rate = 1
	c.Duration = "20ms"
	c.Scenarios = []config.Scenario{{Name: "Journey", Steps: steps}}
	return c
}

func assertJourneyAccounting(t *testing.T, r Result) {
	t.Helper()
	j := r.Journeys
	if j.Scheduled != j.Started+j.DroppedCapacity+j.DroppedScheduler || j.Started != j.Completed || j.Completed != j.Succeeded+j.Failed || j.InFlight != 0 || j.Latency.Samples != j.Completed {
		t.Fatalf("invalid journey accounting: %+v", j)
	}
	var completed, scheduled, requests int64
	for _, s := range r.Scenarios {
		completed += s.Journeys.Completed
		scheduled += s.Journeys.Scheduled
		for _, step := range s.Steps {
			requests += step.Completed
			assertAccounting(t, step.Snapshot)
		}
	}
	if completed != j.Completed || scheduled != j.Scheduled || requests != r.Completed {
		t.Fatalf("scenario totals differ: %d/%d journeys %d/%d arrivals %d/%d requests", completed, j.Completed, scheduled, j.Scheduled, requests, r.Completed)
	}
}

func TestAuthenticatedJourneyHasIsolatedCookiesDataAndCorrelations(t *testing.T) {
	var bad, received atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		id := r.URL.Query().Get("id")
		token := "private-token-" + id
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			if _, err := r.Cookie("session"); err == nil {
				bad.Add(1)
			}
			var body struct {
				Username string `json:"username"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Username == "" || r.URL.Query().Get("iteration") != "1" {
				bad.Add(1)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/"})
			w.Header().Set("X-Token", token)
			json.NewEncoder(w).Encode(map[string]string{"token": token, "username": body.Username})
		case "/profile":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != token || r.Header.Get("Authorization") != "Bearer "+token || r.URL.Query().Get("username") == "" {
				bad.Add(1)
				http.Error(w, "wrong session", 401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "username": r.URL.Query().Get("username")})
		default:
			io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer s.Close()
	c := journeyConfig(s.URL,
		config.Step{Name: "Login", URL: "/login?id={{user_id}}&iteration={{iteration}}", Method: "POST", Body: `{"username":{{username|json}}}`, Extract: map[string]config.Extraction{"token": {JSONPointer: pointer("/token")}, "header_token": {Header: "X-Token"}}, Assertions: []config.Assertion{{JSONPointer: pointer("/username"), Equals: pointer("{{username}}")}, {Header: "X-Token", Equals: pointer("{{token}}")}}},
		config.Step{Name: "Profile", URL: "/profile?id={{user_id}}&username={{username|url}}", Headers: map[string]string{"Authorization": "Bearer {{header_token}}"}, Assertions: []config.Assertion{{JSONPointer: pointer("/ok"), Equals: pointer("true")}, {JSONPointer: pointer("/username"), Equals: pointer("{{username}}")}}},
		config.Step{Name: "Finish", URL: "/finish", Assertions: []config.Assertion{{BodyContains: pointer(`"ok":true`)}}},
	)
	c.Rate = 10
	c.Duration = "300ms"
	c.Scenarios[0].Data = []map[string]string{{"username": "alice \"quoted\" & more"}, {"username": "bob\nnewline"}}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if bad.Load() != 0 || received.Load() != 9 || r.Completed != 9 || r.Journeys.Completed != 3 || !r.Passed || r.Journeys.StartedPerSecond != 10 || r.StartedRPS != 30 {
		t.Fatalf("journey behavior incorrect: bad=%d received=%d result=%+v", bad.Load(), received.Load(), r)
	}
	serialized, _ := json.Marshal(r)
	if strings.Contains(string(serialized), "private-token-") || strings.Contains(string(serialized), "quoted") {
		t.Fatal("correlation data leaked into report")
	}
}

func TestUsersRetainTheirOwnCookiesAcrossIterations(t *testing.T) {
	var bad atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		iteration, _ := strconv.Atoi(r.URL.Query().Get("iteration"))
		cookie, err := r.Cookie("session")
		if iteration == 1 {
			if err == nil {
				bad.Add(1)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: id, Path: "/"})
		} else if err != nil || cookie.Value != id {
			bad.Add(1)
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
		io.WriteString(w, "ok")
	}))
	defer s.Close()
	c := journeyConfig(s.URL, config.Step{URL: "/visit?id={{user_id}}&iteration={{iteration}}"})
	c.Mode = "users"
	c.Rate = 0
	c.VUs = 2
	c.Duration = "100ms"
	c.ThinkTime = "2ms"
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if bad.Load() != 0 || r.Journeys.Completed < 4 || !r.Passed {
		t.Fatalf("user cookie persistence failed: bad=%d result=%+v", bad.Load(), r)
	}
}

func TestJourneyFailureSkipsDependentSteps(t *testing.T) {
	for _, category := range []string{"extraction", "assertion"} {
		t.Run(category, func(t *testing.T) {
			var dependent atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/start" {
					dependent.Add(1)
				}
				io.WriteString(w, `{"ok":true}`)
			}))
			defer s.Close()
			first := config.Step{URL: "/start"}
			if category == "extraction" {
				first.Extract = map[string]config.Extraction{"token": {JSONPointer: pointer("/missing")}}
			} else {
				first.Assertions = []config.Assertion{{JSONPointer: pointer("/ok"), Equals: pointer("false")}}
			}
			r := runTest(t, journeyConfig(s.URL, first, config.Step{URL: "/second"}, config.Step{URL: "/third"}))
			assertJourneyAccounting(t, r)
			if dependent.Load() != 0 || r.Errors[category] != 1 || r.Journeys.Failed != 1 || r.Scenarios[0].Steps[1].Skipped != 1 || r.Scenarios[0].Steps[2].Skipped != 1 || r.Passed {
				t.Fatalf("failure did not stop journey: %+v", r)
			}
		})
	}
}

func TestExpandedHeadersAndURLsCannotEscapeValidation(t *testing.T) {
	for _, attack := range []string{"header", "origin"} {
		t.Run(attack, func(t *testing.T) {
			var sent atomic.Int64
			value := "secret\r\nInjected: value"
			if attack == "origin" {
				value = "//example.invalid/steal"
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent.Add(1)
				json.NewEncoder(w).Encode(map[string]string{"value": value})
			}))
			defer s.Close()
			second := config.Step{URL: "/second", Headers: map[string]string{"Authorization": "{{value}}"}}
			if attack == "origin" {
				second.URL = "{{value}}"
				second.Headers = nil
			}
			c := journeyConfig(s.URL, config.Step{URL: "/start", Extract: map[string]config.Extraction{"value": {JSONPointer: pointer("/value")}}}, second, config.Step{URL: "/third"})
			r := runTest(t, c)
			assertJourneyAccounting(t, r)
			if sent.Load() != 1 || r.Errors["template"] != 1 || r.Completed != 2 || r.Scenarios[0].Steps[2].Skipped != 1 || r.Passed {
				t.Fatalf("expanded request was sent: %+v", r)
			}
		})
	}
}

func TestJourneyDeadlineIncludesPausesAndDrainContinuesSteps(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer s.Close()
	c := journeyConfig(s.URL, config.Step{URL: "/first", Pause: "40ms"}, config.Step{URL: "/second", Pause: "40ms"})
	c.Duration = "10ms"
	c.JourneyTimeout = "60ms"
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if r.Completed != 2 || r.Failed != 0 || r.Journeys.Errors["timeout"] != 1 || r.TotalSeconds < .055 || r.Passed {
		t.Fatalf("journey deadline or drain is wrong: %+v", r)
	}
}

func TestWeightedCapacityDropsBelongToJourneys(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(80 * time.Millisecond):
		}
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	c := journeyConfig(s.URL, config.Step{URL: "/first"}, config.Step{URL: "/second"})
	c.Rate = 20
	c.Duration = "300ms"
	c.MaxInFlight = 1
	c.Scenarios[0].Name = "A"
	c.Scenarios[0].Weight = pointer(2)
	c.Scenarios = append(c.Scenarios, config.Scenario{Name: "B", Weight: pointer(1), Steps: []config.Step{{URL: "/only"}}})
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if r.Journeys.Scheduled != 6 || r.Journeys.DroppedCapacity == 0 || r.DroppedCapacity != 0 || r.Scenarios[0].Journeys.Scheduled != 4 || r.Scenarios[1].Journeys.Scheduled != 2 || r.Journeys.PeakInFlight != 1 || r.Passed {
		t.Fatalf("weighted demand accounting wrong: %+v", r)
	}
	if r.arrivalDropPercent() != r.Journeys.DropPercent {
		t.Fatal("threshold used request counts for journey drops")
	}
}

func TestCaptureIsBounded(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, strings.Repeat("x", (1<<20)+1)) }))
	defer s.Close()
	c := journeyConfig(s.URL, config.Step{URL: "/", Assertions: []config.Assertion{{BodyContains: pointer("x")}}})
	r := runTest(t, c)
	if r.Errors["capture_too_large"] != 1 || r.BytesRead != (1<<20)+1 || r.Passed {
		t.Fatalf("capture limit ignored: %+v", r)
	}
	if r.HTTP == nil || r.HTTP.BodyRead.Samples != 1 || r.HTTP.BodyReadFailures != 0 {
		t.Fatalf("correlation capture limit changed completed network timing: %+v", r.HTTP)
	}
}

func TestJitterIsRepeatableOrderedAndClippedToPhase(t *testing.T) {
	r := runner{plan: config.Plan{Config: config.Config{ArrivalDistribution: "jitter", Seed: 42}}}
	start := time.Now()
	duration := 103 * time.Millisecond
	var previous time.Time
	for i := int64(0); i < slots(duration, 20); i++ {
		due := r.due(start, duration, i, 20, i)
		if due != r.due(start, duration, i, 20, i) || due.Before(arrival(start, i, 20)) || !due.Before(start.Add(duration)) || (!previous.IsZero() && !due.After(previous)) {
			t.Fatalf("invalid jitter time for slot %d: %v", i, due)
		}
		previous = due
	}
}

func TestBoundedCookieJarAllowsUpdatesAndDeletion(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	j := &boundedJar{jar: jar, keys: map[cookieKey]int{}}
	u, _ := url.Parse("http://example.test/")
	for i := 0; i < maxCookieKeys+10; i++ {
		j.SetCookies(u, []*http.Cookie{{Name: fmt.Sprintf("c%d", i), Value: "initial", Path: "/"}})
	}
	if len(j.Cookies(u)) != maxCookieKeys {
		t.Fatal("cookie history is unbounded")
	}
	j.SetCookies(u, []*http.Cookie{{Name: "c0", Value: "updated", Path: "/"}, {Name: "c1", MaxAge: -1, Path: "/"}})
	j.SetCookies(u, []*http.Cookie{{Name: "replacement", Value: "ok", Path: "/"}})
	if len(j.Cookies(u)) != maxCookieKeys {
		t.Fatal("deletion did not free a cookie key")
	}
	for _, c := range j.Cookies(u) {
		if c.Name == "c0" && c.Value != "updated" {
			t.Fatal("existing cookie could not be updated")
		}
	}
}

func TestOversizedCookieFailsTheStep(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: strings.Repeat("x", maxCookieBytes), Path: "/"})
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	r := runTest(t, journeyConfig(s.URL, config.Step{URL: "/"}))
	if r.Errors["cookie_limit"] != 1 || r.Journeys.Failed != 1 || r.Passed {
		t.Fatalf("cookie limit was silently ignored: %+v", r)
	}
}

func TestJourneyCancellationSkipsPendingSteps(t *testing.T) {
	started := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	c := journeyConfig(s.URL, config.Step{URL: "/blocking"}, config.Step{URL: "/later"})
	c.Duration = "5s"
	r, err := Run(ctx, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertJourneyAccounting(t, r)
	if !r.Interrupted || r.Journeys.Errors["canceled"] != 1 || r.Scenarios[0].Steps[1].Skipped != 1 || r.Passed {
		t.Fatalf("journey cancellation failed: %+v", r)
	}
}
