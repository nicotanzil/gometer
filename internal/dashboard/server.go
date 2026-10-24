// Package dashboard hosts an embedded local app over the existing engine.
package dashboard

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gometer/examples"
	"gometer/internal/config"
	"gometer/internal/design"
	"gometer/internal/diagnostics"
	"gometer/internal/distributed"
	"gometer/internal/engine"
	"gometer/internal/metrics"
	"gometer/internal/report"
	"gometer/internal/telemetry"
)

//go:embed assets/*
var assets embed.FS

type Options struct {
	AllowHTTPWorkers bool
}

type Server struct {
	workerChecking   map[string]bool
	allowHTTPWorkers bool
	mu               sync.Mutex
	store            *store
	current          *Run
	cancel           context.CancelFunc
	done             chan struct{}
	ctx              context.Context
	closing          bool
	token            string
	host             string
	origin           string
	secureCookie     bool
	exports          map[string]configExport
	workerToken      string
	workerCA         string
	telemetryToken   string
	telemetryCA      string
}

type configExport struct {
	data    []byte
	expires time.Time
}

// Listen permits loopback by default. A remote listener requires an explicit
// HTTPS public origin so Host and Origin checks remain bound to one ingress URL.
func Listen(address, publicOrigin string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("listen address must include an explicit IP and port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("dashboard listen address must use an explicit IP")
	}
	if ip.IsLoopback() {
		if publicOrigin != "" {
			return nil, fmt.Errorf("-public-origin is only valid with a non-loopback listen address")
		}
	} else {
		if publicOrigin == "" {
			return nil, fmt.Errorf("non-loopback dashboard listeners require -public-origin https://host")
		}
		if _, _, err := parsePublicOrigin(publicOrigin); err != nil {
			return nil, err
		}
	}
	return net.Listen("tcp", address)
}

func parsePublicOrigin(raw string) (host, origin string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", "", fmt.Errorf("-public-origin must be an HTTPS origin such as https://gometer.example.com, without a path, credentials, query, or fragment")
	}
	name := strings.ToLower(u.Hostname())
	if name == "" || strings.Contains(name, "%") {
		return "", "", fmt.Errorf("-public-origin must contain a valid DNS name or IP address")
	}
	authority := name
	if strings.Contains(name, ":") {
		authority = "[" + name + "]"
	}
	if rawPort := u.Port(); rawPort != "" {
		port, parseErr := strconv.Atoi(rawPort)
		if parseErr != nil || port < 1 || port > 65535 {
			return "", "", fmt.Errorf("-public-origin port must be between 1 and 65535")
		}
		if port != 443 {
			authority = net.JoinHostPort(name, strconv.Itoa(port))
		}
	}
	return authority, "https://" + authority, nil
}

func New(ctx context.Context, dir, host, origin string, opts Options) (*Server, error) {
	saved, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	token, err := newID()
	if err != nil {
		return nil, err
	}
	return &Server{workerChecking: map[string]bool{}, allowHTTPWorkers: opts.AllowHTTPWorkers, store: saved, ctx: ctx, token: token, host: host, origin: origin, secureCookie: strings.HasPrefix(origin, "https://"), exports: map[string]configExport{}, workerToken: os.Getenv("GOMETER_WORKER_TOKEN"), workerCA: os.Getenv("GOMETER_WORKER_CA"), telemetryToken: os.Getenv("GOMETER_TELEMETRY_TOKEN"), telemetryCA: os.Getenv("GOMETER_TELEMETRY_CA")}, nil
}

func Serve(ctx context.Context, address, dir, publicOrigin string, opts Options, output io.Writer) error {
	ln, err := Listen(address, publicOrigin)
	if err != nil {
		return err
	}
	defer ln.Close()
	host := ln.Addr().String()
	origin := "http://" + host
	if publicOrigin != "" {
		host, origin, err = parsePublicOrigin(publicOrigin)
		if err != nil {
			return err
		}
	}
	s, err := New(ctx, dir, host, origin, opts)
	if err != nil {
		return err
	}
	if publicOrigin != "" {
		fmt.Fprintln(output, "Warning: GoMeter is single-user; protect this listener with a VPN or ingress authentication and an allowlist.")
	}
	if opts.AllowHTTPWorkers {
		fmt.Fprintln(output, "WARNING: Remote HTTP worker control is permitted.", distributed.PlaintextControlWarning)
	}
	httpServer := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	fmt.Fprintf(output, "GoMeter dashboard: %s\nDashboard listener: %s\nSaved scenarios and runs: %s\n", origin, ln.Addr(), dir)
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			httpServer.Shutdown(shutdown)
		case <-stopped:
		}
	}()
	err = httpServer.Serve(ln)
	close(stopped)
	s.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close waits for cancellation and final persistence before returning.
func (s *Server) Close() {
	s.mu.Lock()
	s.closing = true
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if r.Host != s.host {
		fail(w, http.StatusForbidden, "Use the dashboard URL printed by GoMeter.")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		fail(w, http.StatusForbidden, "Cross-site requests are not allowed.")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		fail(w, http.StatusForbidden, "Origin does not match the dashboard.")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/download/") {
		cookie, err := r.Cookie("gometer-download")
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.token)) != 1 {
			fail(w, 403, "Reload the dashboard before downloading.")
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
		if r.Method == "GET" && len(parts) == 2 && parts[0] == "config" && validID(parts[1]) {
			s.mu.Lock()
			defer s.mu.Unlock()
			export, ok := s.exports[parts[1]]
			if !ok || time.Now().After(export.expires) {
				fail(w, 404, "Configuration export expired. Export again from the editor.")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", `attachment; filename="gometer-scenario.json"`)
			w.Write(export.data)
			return
		}
		if r.Method != "GET" || len(parts) != 2 || !validID(parts[0]) || (parts[1] != "report.json" && parts[1] != "report.html" && parts[1] != "export") {
			fail(w, 404, "Download not found.")
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/api/runs/" + strings.Join(parts, "/")
		s.api(w, clone)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gometer-Token")), []byte(s.token)) != 1 {
			fail(w, http.StatusForbidden, "Reload the dashboard to reconnect.")
			return
		}
		s.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, 405, "Method not allowed.")
		return
	}
	switch r.URL.Path {
	case "/":
		// This cookie authorizes only read-only native downloads. API mutations
		// still require the startup token in a custom header.
		http.SetCookie(w, &http.Cookie{Name: "gometer-download", Value: s.token, Path: "/download/", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteStrictMode})
		page, _ := assets.ReadFile("assets/index.html")
		t, err := template.New("dashboard").Parse(string(page))
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodHead {
			t.Execute(w, struct {
				Token            string
				AllowHTTPWorkers bool
				PlaintextWarning string
			}{s.token, s.allowHTTPWorkers, distributed.PlaintextControlWarning})
		}
	case "/fonts.css", "/BricolageGrotesque-Latin.woff2", "/BricolageGrotesque-LatinExt.woff2", "/HankenGrotesk-Latin.woff2", "/HankenGrotesk-LatinExt.woff2",
		"/gometer-mark.svg", "/favicon.svg", "/favicon.ico", "/apple-touch-icon.png":
		body, err := design.Files.ReadFile("assets" + r.URL.Path)
		if err != nil {
			fail(w, 404, "Asset not found.")
			return
		}
		kind := "font/woff2"
		switch {
		case strings.HasSuffix(r.URL.Path, ".css"):
			kind = "text/css; charset=utf-8"
		case strings.HasSuffix(r.URL.Path, ".svg"):
			kind = "image/svg+xml"
		case strings.HasSuffix(r.URL.Path, ".ico"):
			kind = "image/x-icon"
		case strings.HasSuffix(r.URL.Path, ".png"):
			kind = "image/png"
		}
		w.Header().Set("Content-Type", kind)
		if r.Method != http.MethodHead {
			w.Write(body)
		}
	case "/tests.js", "/tests.css", "/results.js", "/results.css", "/workers.js", "/workers.css", "/app.js", "/style.css", "/theme.js", "/theme.css", "/navigation.js", "/navigation.css", "/journey-model.js", "/journey-editor.js", "/journey.css", "/jmx.js", "/jmx.css", "/csv.js", "/csv.css", "/shortcuts.js", "/shortcuts.css", "/coverage.json",
		"/IBMPlexSans-Regular.woff2", "/IBMPlexSans-Medium.woff2", "/IBMPlexSans-SemiBold.woff2", "/IBMPlexSans-Bold.woff2", "/IBMPlexMono-Regular.woff2":
		body, err := assets.ReadFile("assets" + r.URL.Path)
		if err != nil {
			fail(w, 404, "Asset not found.")
			return
		}
		kind := "text/javascript; charset=utf-8"
		if strings.HasSuffix(r.URL.Path, ".css") {
			kind = "text/css; charset=utf-8"
		}
		if r.URL.Path == "/coverage.json" {
			kind = "application/json; charset=utf-8"
		}
		if strings.HasSuffix(r.URL.Path, ".woff2") {
			kind = "font/woff2"
		}
		w.Header().Set("Content-Type", kind)
		if r.Method != http.MethodHead {
			w.Write(body)
		}
	default:
		fail(w, 404, "Not found.")
	}
}

func jsonResponse(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, code int, message string) {
	jsonResponse(w, code, map[string]string{"error": message})
}

func readConfig(w http.ResponseWriter, r *http.Request) (config.Config, config.Plan, error) {
	if contentType := strings.Split(r.Header.Get("Content-Type"), ";")[0]; contentType != "application/json" {
		return config.Config{}, config.Plan{}, fmt.Errorf("send a JSON configuration with Content-Type application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, config.MaxConfigBytes)
	c, err := config.Load(r.Body)
	if err != nil {
		return c, config.Plan{}, err
	}
	p, err := c.Validate()
	return c, p, err
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if s.definitionsAPI(w, r) {
		return
	}
	if s.workersAPI(w, r) {
		return
	}
	if s.csvAPI(w, r) {
		return
	}
	if s.jmxAPI(w, r) {
		return
	}
	if r.Method == "POST" && path == "/api/config-export" {
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			fail(w, 400, "Send a JSON configuration.")
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, config.MaxConfigBytes))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		c, err := config.Load(bytes.NewReader(data))
		if err == nil {
			_, err = c.Validate()
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, export := range s.exports {
			if time.Now().After(export.expires) {
				delete(s.exports, id)
			}
		}
		if len(s.exports) >= 16 {
			fail(w, 429, "Too many recent exports. Try again in five minutes.")
			return
		}
		id, err := newID()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.exports[id] = configExport{data: append(data, '\n'), expires: time.Now().Add(5 * time.Minute)}
		jsonResponse(w, 200, map[string]string{"path": "/download/config/" + id})
		return
	}
	if r.Method == "POST" && path == "/api/runs/import" {
		s.importRun(w, r)
		return
	}
	if r.Method == "GET" && path == "/api/state" {
		s.mu.Lock()
		defer s.mu.Unlock()
		var current *RunSummary
		if s.current != nil {
			summary := s.current.summary()
			current = &summary
		}
		jsonResponse(w, 200, map[string]any{"current": current, "runs": s.store.runList(), "scenarios": s.store.scenarioList(), "migrations": s.store.migrationList(), "max_samples": maxSamples, "allow_http_workers": s.allowHTTPWorkers, "workers": s.store.workerList()})
		return
	}
	if r.Method == "GET" && path == "/api/examples" {
		list := []SavedScenario{}
		for _, name := range []string{"smoke", "stress", "post", "journey", "flow", "mix", "distributed", "telemetry", "users-ramp", "csv"} {
			data, _ := examples.Files.ReadFile(name + ".json")
			c, err := config.Load(bytes.NewReader(data))
			if err != nil {
				fail(w, 500, err.Error())
				return
			}
			list = append(list, SavedScenario{ID: name, Name: c.Name, Config: c})
		}
		jsonResponse(w, 200, list)
		return
	}
	if r.Method == "POST" && (path == "/api/validate" || path == "/api/runs" || path == "/api/scenarios") {
		c, p, err := readConfig(w, r)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		switch path {
		case "/api/validate":
			if err := config.ValidateWorkerTransport(c.Workers, s.allowHTTPWorkers); err != nil {
				fail(w, 400, err.Error())
				return
			}
			unit := "requests"
			if p.IsJourney() {
				unit = "journeys"
			}
			jsonResponse(w, 200, map[string]any{"valid": true, "workload": unit, "load_seconds": p.LoadDuration.Seconds(), "phase_count": len(p.Phases), "scenario_count": len(p.ScenarioPlans), "config": c})
		case "/api/scenarios":
			s.saveScenario(w, "", c, "")
		case "/api/runs":
			s.start(w, c, p)
		}
		return
	}
	if strings.HasPrefix(path, "/api/scenarios/") {
		id := strings.TrimPrefix(path, "/api/scenarios/")
		if strings.HasSuffix(id, "/copy") {
			sourceID := strings.TrimSuffix(id, "/copy")
			if !validID(sourceID) {
				fail(w, 404, "Test not found.")
				return
			}
			s.copyDefinition(w, r, sourceID)
			return
		}
		if !validID(id) {
			fail(w, 404, "Scenario not found.")
			return
		}
		if r.Method == "GET" {
			s.mu.Lock()
			defer s.mu.Unlock()
			saved, ok := s.store.scenarios[id]
			if !ok {
				fail(w, 404, "Scenario not found.")
				return
			}
			saved.Revision = definitionRevision(saved)
			w.Header().Set("ETag", definitionETag(saved))
			jsonResponse(w, 200, saved)
			return
		}
		if r.Method == "PUT" {
			c, _, err := readConfig(w, r)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			s.saveScenario(w, id, c, r.Header.Get("If-Match"))
			return
		}
		if r.Method == "DELETE" {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := s.store.scenarios[id]; !ok {
				fail(w, 404, "Scenario not found.")
				return
			}
			if err := os.Remove(s.store.dir + "/scenarios/" + id + ".json"); err != nil {
				fail(w, 500, err.Error())
				return
			}
			delete(s.store.scenarios, id)
			jsonResponse(w, 200, map[string]bool{"deleted": true})
			return
		}
	}
	if strings.HasPrefix(path, "/api/runs/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/runs/"), "/")
		id := parts[0]
		if !validID(id) || len(parts) > 2 {
			fail(w, 404, "Run not found.")
			return
		}
		if len(parts) == 2 && parts[1] == "stop" && r.Method == "POST" {
			s.stop(w, id)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		var run Run
		var err error
		if s.current != nil && s.current.ID == id {
			run = *s.current
		} else {
			run, err = s.store.getRun(id)
		}
		if err != nil {
			fail(w, 404, "Run not found.")
			return
		}
		if len(parts) == 1 && r.Method == "DELETE" {
			if s.current != nil && s.current.ID == id && s.cancel != nil {
				fail(w, 409, "Stop this run before deleting it.")
				return
			}
			if err := os.Remove(s.store.dir + "/runs/" + id + ".json"); err != nil {
				fail(w, 500, err.Error())
				return
			}
			delete(s.store.runs, id)
			if s.current != nil && s.current.ID == id {
				s.current = nil
			}
			jsonResponse(w, 200, map[string]bool{"deleted": true})
			return
		}
		if r.Method != "GET" {
			fail(w, 405, "Method not allowed.")
			return
		}
		if len(parts) == 1 {
			jsonResponse(w, 200, run)
			return
		}
		// Downloads never put authentication tokens in URLs.
		switch parts[1] {
		case "export":
			w.Header().Set("Content-Disposition", `attachment; filename="gometer-run-`+id+`.json"`)
			jsonResponse(w, 200, run)
		case "config":
			jsonResponse(w, 200, run.Config)
		case "report.json", "report.html":
			if run.Result == nil {
				fail(w, 409, "Final results are not available yet.")
				return
			}
			w.Header().Set("Content-Disposition", `attachment; filename="gometer-`+id+`.`+strings.TrimPrefix(parts[1], "report.")+`"`)
			if parts[1] == "report.json" {
				w.Header().Set("Content-Type", "application/json")
				report.JSON(w, *run.Result)
			} else {
				w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; base-uri 'none'; frame-ancestors 'none'")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				report.HTML(w, *run.Result)
			}
		default:
			fail(w, 404, "Not found.")
		}
		return
	}
	fail(w, 404, "API route not found.")
}

// Imports are records of someone else's measurements. Validate structure and
// accounting, but do not imply that GoMeter independently verified the traffic.
func (s *Server) importRun(w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 400, "Send a run bundle as JSON.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var run Run
	if err := dec.Decode(&run); err != nil {
		fail(w, 400, "Read run bundle: "+err.Error())
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		fail(w, 400, "Run bundle must contain one JSON object.")
		return
	}
	p, err := run.Config.Validate()
	if err != nil {
		fail(w, 400, "Run configuration: "+err.Error())
		return
	}
	if err := validateWorkerSnapshots(run.WorkerSnapshots, run.Config.Workers); err != nil {
		fail(w, 400, err.Error())
		return
	}
	result := run.Result
	workload := "requests"
	if p.IsJourney() {
		workload = "journeys"
	}
	if result == nil || (result.SchemaVersion < 2 || result.SchemaVersion > 11) || result.Workload != workload || result.URL != run.Config.URL || result.Mode != run.Config.Mode || result.Insecure != run.Config.Insecure || result.StartedAt.IsZero() || result.PlannedLoadSeconds != p.LoadDuration.Seconds() || result.LoadSeconds < 0 || (p.LoadDuration > 0 && result.LoadSeconds > result.PlannedLoadSeconds) || result.TotalSeconds < result.LoadSeconds {
		fail(w, 400, "Import a completed schema 2–11 run bundle exported with Run + timeline. Configuration and results must match.")
		return
	}
	if err := result.ValidateIterations(p); err != nil {
		fail(w, 400, "Run bundle stopping conditions: "+err.Error())
		return
	}
	if len(run.Config.Workers) > 0 && ((result.SchemaVersion != 4 && result.SchemaVersion != 5 && result.SchemaVersion != 6) || result.Distribution == nil || len(result.Distribution.Workers) != len(run.Config.Workers)) {
		fail(w, 400, "Distributed run bundle is missing worker results.")
		return
	}
	if err := telemetry.ValidateSummary(run.Config.Telemetry, result.Telemetry, result.StartedAt, result.TotalSeconds); err != nil || (result.Telemetry != nil && result.SchemaVersion < 5) {
		fail(w, 400, "Run bundle has invalid target telemetry.")
		return
	}
	if err := result.ValidateFlow(p); err != nil {
		fail(w, 400, "Run bundle flow measurements: "+err.Error())
		return
	}
	validCounts := func(s metrics.Counts) bool {
		return s.Started >= 0 && s.Completed >= 0 && s.Succeeded >= 0 && s.Failed >= 0 && s.DroppedCapacity >= 0 && s.DroppedScheduler >= 0 && s.InFlight == 0 && s.Scheduled == s.Started+s.DroppedCapacity+s.DroppedScheduler && s.Started == s.Completed && s.Completed == s.Succeeded+s.Failed
	}
	j := result.Journeys
	if !validCounts(result.Counts) || (workload == "journeys" && !validCounts(metrics.Counts{Scheduled: j.Scheduled, Started: j.Started, Completed: j.Completed, Succeeded: j.Succeeded, Failed: j.Failed, DroppedCapacity: j.DroppedCapacity, DroppedScheduler: j.DroppedScheduler, InFlight: j.InFlight})) || len(run.Samples) > maxSamples || len(result.Phases) > 100 || len(result.Scenarios) > 20 {
		fail(w, 400, "Run bundle has inconsistent counts or exceeds timeline limits.")
		return
	}
	var previous float64 = -1
	var previousUsers engine.UserCounts
	var previousStopped int64
	var previousTestStop bool
	var previousDatasets []engine.DatasetResult
	for _, sample := range run.Samples {
		if sample.ElapsedSeconds < 0 || sample.ElapsedSeconds < previous || sample.ElapsedSeconds > result.TotalSeconds || sample.LoadSeconds < 0 || sample.LoadSeconds > result.LoadSeconds || len(sample.Workers) > config.MaxWorkers {
			fail(w, 400, "Run bundle timeline is invalid.")
			return
		}
		if sample.Telemetry != nil {
			if p.TargetTelemetry == nil || telemetry.ValidateSnapshot(*sample.Telemetry, p.TargetTelemetry.Metrics, result.StartedAt, result.TotalSeconds) != nil {
				fail(w, 400, "Run bundle target telemetry timeline is invalid.")
				return
			}
		}
		if p.HasUserSchedule() != (sample.UserSchedule != nil) {
			fail(w, 400, "Run bundle is missing user schedule observations.")
			return
		}
		if sample.UserSchedule != nil {
			if sample.UserSchedule.Validate(p, sample.ElapsedSeconds, sample.LoadSeconds, sample.ElapsedSeconds == result.TotalSeconds) != nil || sample.UserSchedule.Users.Started > result.UserSchedule.Users.Started || sample.UserSchedule.Users.Finished > result.UserSchedule.Users.Finished || sample.UserSchedule.Users.Started < previousUsers.Started || sample.UserSchedule.Users.Finished < previousUsers.Finished {
				fail(w, 400, "Run bundle user schedule timeline is invalid.")
				return
			}
			previousUsers = sample.UserSchedule.Users
		}
		if p.HasFailurePolicy() != (sample.FailurePolicy != nil) {
			fail(w, 400, "Run bundle is missing failure-policy observations.")
			return
		}
		if f := sample.FailurePolicy; f != nil {
			failed := sample.Requests.Failed
			if p.IsJourney() {
				failed = sample.Journeys.Failed
			}
			if f.Validate(p.Config, failed) != nil || f.UsersStopped < previousStopped || f.UsersStopped > result.FailurePolicy.UsersStopped || (previousTestStop && !f.TestStopped) || (f.TestStopped && !result.FailurePolicy.TestStopped) || (sample.ElapsedSeconds == result.TotalSeconds && f.TestStopped != result.FailurePolicy.TestStopped) {
				fail(w, 400, "Run bundle failure-policy timeline is invalid.")
				return
			}
			previousStopped = f.UsersStopped
			previousTestStop = f.TestStopped
		}

		if err := engine.ValidateDatasets(p, sample.Datasets, sample.Journeys.Started); err != nil {
			fail(w, 400, "Run bundle CSV timeline: "+err.Error())
			return
		}
		for i, d := range sample.Datasets {
			final := result.Datasets[i]
			if d.RowsAssigned > final.RowsAssigned || d.UsersExhausted > final.UsersExhausted || (len(previousDatasets) > i && (d.RowsAssigned < previousDatasets[i].RowsAssigned || d.UsersExhausted < previousDatasets[i].UsersExhausted)) || (sample.ElapsedSeconds == result.TotalSeconds && d != final) {
				fail(w, 400, "Run bundle CSV timeline is inconsistent.")
				return
			}
		}
		previousDatasets = sample.Datasets
		previous = sample.ElapsedSeconds
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.store.runs) >= maxRuns {
		fail(w, 409, "Run history is full (1000).")
		return
	}
	id, err := newID()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	run.ID = id
	run.Name = result.Name
	run.CreatedAt = result.StartedAt
	run.Workload = workload
	run.PlannedSeconds = result.PlannedLoadSeconds
	run.Status = "failed"
	if result.Passed {
		run.Status = "passed"
	}
	if result.Interrupted {
		run.Status = "interrupted"
	}
	run.Error = ""
	run.SaveError = ""
	run.Imported = true
	if err := s.store.write("runs", id, run); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.store.runs[id] = run.summary()
	jsonResponse(w, 201, run.summary())
}

func (s *Server) saveScenario(w http.ResponseWriter, id string, c config.Config, expected string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		old, ok := s.store.scenarios[id]
		if !ok {
			fail(w, 404, "Scenario not found.")
			return
		}
		if !checkDefinitionRevision(w, old, expected) {
			return
		}
	} else {
		if len(s.store.scenarios) >= maxScenarios {
			fail(w, 409, "Scenario library is full (200). Delete an unused scenario first.")
			return
		}
		var err error
		id, err = newID()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	now := time.Now().UTC()
	saved := SavedScenario{ID: id, Name: c.Name, UpdatedAt: now, CreatedAt: &now, Revision: 1, Config: c}
	if old, ok := s.store.scenarios[id]; ok {
		saved.Migration = old.Migration
		saved.CreatedAt = old.CreatedAt
		saved.Source = old.Source
		saved.Revision = definitionRevision(old) + 1
	}
	if err := s.store.write("scenarios", id, saved); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.store.scenarios[id] = saved
	w.Header().Set("ETag", definitionETag(saved))
	jsonResponse(w, 200, saved)
}

func (s *Server) start(w http.ResponseWriter, c config.Config, p config.Plan) {
	if err := config.ValidateWorkerTransport(c.Workers, s.allowHTTPWorkers); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Telemetry != nil {
		collector, err := telemetry.New(*c.Telemetry, telemetry.Options{Token: s.telemetryToken, CAFile: s.telemetryCA})
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		collector.Close()
	}
	if len(c.Workers) > 0 {
		if err := distributed.ValidToken(s.workerToken); err != nil {
			fail(w, 400, "Set GOMETER_WORKER_TOKEN before starting the dashboard to use workers.")
			return
		}
	}
	if s.closing {
		fail(w, 503, "Dashboard is shutting down.")
		return
	}
	if s.cancel != nil {
		fail(w, 409, "A run is already active. Stop it or wait for completion.")
		return
	}
	if s.current != nil && s.current.SaveError != "" {
		fail(w, 409, "The previous run could not be saved. Export it before restarting the dashboard.")
		return
	}
	if len(s.store.runs) >= maxRuns {
		fail(w, 409, "Run history is full (1000). Export and delete an old run first.")
		return
	}
	id, err := newID()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	workload := "requests"
	if p.IsJourney() {
		workload = "journeys"
	}
	run := &Run{WorkerSnapshots: s.snapshotWorkers(c.Workers), ID: id, Name: c.Name, CreatedAt: time.Now().UTC(), Status: "running", Config: c, Workload: workload, PlannedSeconds: p.LoadDuration.Seconds(), Samples: []Sample{{Phase: p.Phases[0].Name, TargetRate: p.Phases[0].Rate}}}
	run.Samples[0].Datasets = engine.DatasetSettings(p)
	if p.HasUserSchedule() {
		run.Samples[0].UserSchedule = &engine.UserSchedule{RampUpSeconds: p.UserRampUp.Seconds(), PlannedStartupSeconds: p.UserStartupDelay.Seconds(), Waiting: p.UserStartupDelay > 0, Users: engine.UserCounts{Planned: int64(p.VUs)}}
	}
	if p.HasFailurePolicy() {
		run.Samples[0].FailurePolicy = &engine.FailurePolicy{Action: p.ErrorAction()}
	}
	// Reserve the file before sending traffic, catching an unwritable data dir.
	if err := s.store.write("runs", id, run); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.store.runs[id] = run.summary()
	s.current = run
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.execute(ctx, c, p, id, s.done)
	jsonResponse(w, 202, run.summary())
}

func appendSample(run *Run, sample Sample) {
	if len(run.Samples) >= maxSamples {
		copy(run.Samples, run.Samples[1:])
		run.Samples = run.Samples[:maxSamples-1]
	}
	run.Samples = append(run.Samples, sample)
}

func (s *Server) execute(ctx context.Context, c config.Config, p config.Plan, id string, done chan struct{}) {
	defer close(done)
	onProgress := func(progress engine.Progress) {
		s.mu.Lock()
		defer s.mu.Unlock()
		appendSample(s.current, Sample{ElapsedSeconds: progress.WallElapsed.Seconds(), LoadSeconds: progress.Elapsed.Seconds(), Phase: progress.PhaseName, TargetRate: progress.TargetRate, Draining: progress.Draining, UserSchedule: progress.UserSchedule, FailurePolicy: progress.FailurePolicy, Datasets: progress.Datasets, Requests: progress.Snapshot, Journeys: progress.Journeys, Generator: progress.Generator, Workers: engine.WorkerPoints(progress.Workers, progress.Workload), Telemetry: progress.Telemetry})
	}
	var result engine.Result
	var err error
	if len(c.Workers) > 0 {
		result, err = distributed.Run(ctx, c, distributed.Options{AllowHTTPWorkers: s.allowHTTPWorkers, Token: s.workerToken, CAFile: s.workerCA, OnProgress: onProgress, Telemetry: telemetry.Options{Token: s.telemetryToken, CAFile: s.telemetryCA}})
	} else {
		result, err = engine.Run(ctx, c, engine.Options{OnProgress: onProgress, Telemetry: telemetry.Options{Token: s.telemetryToken, CAFile: s.telemetryCA}})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.current
	if err != nil {
		run.Status = "error"
		run.Error = err.Error()
	} else {
		run.Result = &result
		run.Status = "failed"
		if result.Passed {
			run.Status = "passed"
		}
		if result.Interrupted {
			run.Status = "interrupted"
		}
		phase := p.Phases[0]
		elapsed := time.Duration(result.LoadSeconds * float64(time.Second))
		for _, candidate := range p.Phases {
			phase = candidate
			if elapsed < candidate.Duration {
				break
			}
			elapsed -= candidate.Duration
		}
		var resources *diagnostics.Resources
		if result.Generator != nil {
			resources = &result.Generator.Final
		}
		var points []engine.WorkerPoint
		if result.Distribution != nil {
			points = engine.WorkerPoints(result.Distribution.Workers, result.Workload)
		}
		var targetPoint *telemetry.Snapshot
		if result.Telemetry != nil && len(result.Telemetry.Timeline) > 0 {
			value := result.Telemetry.Timeline[len(result.Telemetry.Timeline)-1]
			targetPoint = &value
		}
		appendSample(run, Sample{ElapsedSeconds: result.TotalSeconds, LoadSeconds: result.LoadSeconds, Phase: phase.Name, TargetRate: phase.Rate, Draining: true, UserSchedule: result.UserSchedule, FailurePolicy: result.FailurePolicy, Datasets: result.Datasets, Requests: result.Snapshot, Journeys: result.Journeys, Generator: resources, Workers: points, Telemetry: targetPoint})
	}
	if err := s.store.write("runs", id, run); err != nil {
		run.SaveError = "Final results could not be saved: " + err.Error() + ". Export this run before restarting."
	}
	s.store.runs[id] = run.summary()
	s.cancel = nil
}

func (s *Server) stop(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || s.current.ID != id || s.cancel == nil {
		fail(w, 409, "This run is no longer active.")
		return
	}
	s.current.Status = "stopping"
	s.cancel()
	jsonResponse(w, 202, s.current.summary())
}
