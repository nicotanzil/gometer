package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gometer/internal/config"
	"gometer/internal/dashboard"
	"gometer/internal/distributed"
	"gometer/internal/engine"
	"gometer/internal/report"
	"gometer/internal/telemetry"
)

var Version = "0.9.0-dev"

const help = `GoMeter — HTTP load and stress testing

Usage:
  gometer run -url http://localhost:8787/work -vus 10 -duration 10s
  gometer run -url http://localhost:8787/work -rate 100 -duration 10s
  gometer run -config examples/stress.json
  gometer run -config examples/journey.json
  gometer validate -config examples/stress.json
  gometer ui -listen 127.0.0.1:8790
  gometer ui -listen 0.0.0.0:8790 -public-origin https://gometer.example.com
  gometer worker -listen 127.0.0.1:8801 -name worker-1
  gometer run -config examples/smoke.json -workers http://127.0.0.1:8801,http://127.0.0.1:8802
  gometer workspace migrate
  gometer workspace check
  gometer workspace oidc-check
  gometer workspace serve
  gometer version

Startup flags override GOMETER_* environment settings.
Run and validate accept GOMETER_CONFIG; -config overrides it.
Run flags override configuration fields. Run 'gometer run -help' for flags.
Results are saved as JSON and standalone HTML under results/ by default.
Exit codes: 0 passed, 1 checks failed, 2 input/output error, 130 interrupted.
`

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, help)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, help)
		return 0
	case "version":
		fmt.Fprintln(stdout, "GoMeter", Version)
		return 0
	case "validate":
		return validate(args[1:], stdout, stderr)
	case "run":
		return run(ctx, args[1:], stdout, stderr)
	case "ui":
		return ui(ctx, args[1:], stdout, stderr)
	case "workspace":
		return workspaceCommand(ctx, args[1:], stdout, stderr)
	case "worker":
		return worker(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], help)
		return 2
	}
}

func worker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := envString(fs, "listen", "GOMETER_WORKER_LISTEN", "127.0.0.1:8801", "worker IP and port; remote HTTP requires -allow-http")
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	name := envString(fs, "name", "GOMETER_WORKER_NAME", host, "worker display name")
	cert := envString(fs, "tls-cert", "GOMETER_WORKER_TLS_CERT", "", "PEM server certificate for remote control")
	key := envString(fs, "tls-key", "GOMETER_WORKER_TLS_KEY", "", "PEM private key for remote control")
	allowHTTP := fs.Bool("allow-http", false, "permit non-loopback HTTP worker control without application TLS (env GOMETER_WORKER_ALLOW_HTTP)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if err := applyEnvBool(fs, "allow-http", "GOMETER_WORKER_ALLOW_HTTP"); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: gometer worker [-listen IP:port] [-name worker] [-tls-cert cert.pem -tls-key key.pem] [-allow-http]")
		return 2
	}
	if err := distributed.ServeWorker(ctx, *listen, *name, os.Getenv("GOMETER_WORKER_TOKEN"), *cert, *key, *allowHTTP, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func ui(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := envString(fs, "listen", "GOMETER_UI_LISTEN", "127.0.0.1:8790", "dashboard listen IP and port; non-loopback requires -public-origin")
	publicOrigin := envString(fs, "public-origin", "GOMETER_UI_PUBLIC_ORIGIN", "", "exact HTTPS origin used by a trusted private ingress")
	allowHTTP := fs.Bool("allow-http-workers", false, "permit remote HTTP worker control without application TLS (env GOMETER_ALLOW_HTTP_WORKERS)")
	data := envString(fs, "data", "GOMETER_UI_DATA", ".gometer", "directory for saved scenarios and runs")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if err := applyEnvBool(fs, "allow-http-workers", "GOMETER_ALLOW_HTTP_WORKERS"); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if fs.NArg() != 0 || *data == "" {
		fmt.Fprintln(stderr, "usage: gometer ui [-listen 127.0.0.1:8790] [-public-origin https://host] [-data .gometer]")
		return 2
	}
	path, err := filepath.Abs(*data)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := dashboard.Serve(ctx, *listen, path, *publicOrigin, dashboard.Options{AllowHTTPWorkers: *allowHTTP}, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func load(path string) (config.Config, error) {
	if path == "" {
		return config.Defaults(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, err
	}
	defer f.Close()
	return config.Load(f)
}

func validate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := envString(fs, "config", "GOMETER_CONFIG", "", "JSON scenario to validate")
	csv := addCSVFlags(fs)
	allowHTTP := fs.Bool("allow-http-workers", false, "permit remote HTTP worker control without application TLS (env GOMETER_ALLOW_HTTP_WORKERS)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if err := applyEnvBool(fs, "allow-http-workers", "GOMETER_ALLOW_HTTP_WORKERS"); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *path == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: gometer validate -config scenario.json")
		return 2
	}
	c, err := load(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := csv.apply(fs, &c); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := config.ValidateWorkerTransport(c.Workers, *allowHTTP); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if config.HasRemoteHTTPWorkers(c.Workers) {
		fmt.Fprintln(stderr, "WARNING:", distributed.PlaintextControlWarning)
	}
	p, err := c.Validate()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "Valid: %s · %s mode · %s · %d phase(s)\n", c.Name, c.Mode, loadDescription(p), len(p.Phases))
	if p.IsJourney() {
		fmt.Fprintf(stdout, "%d scenario(s) · rate unit: journeys/second · %s arrivals\n", len(p.ScenarioPlans), p.ArrivalDistribution)
	}
	return 0
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	d := config.Defaults()
	path := envString(fs, "config", "GOMETER_CONFIG", "", "JSON scenario file")
	csv := addCSVFlags(fs)
	allowHTTP := fs.Bool("allow-http-workers", false, "permit remote HTTP worker control without application TLS (env GOMETER_ALLOW_HTTP_WORKERS)")
	url := fs.String("url", "", "target HTTP/HTTPS URL")
	name := fs.String("name", d.Name, "test name")
	method := fs.String("method", d.Method, "HTTP method")
	body := fs.String("body", "", "request body (use config for headers)")
	mode := fs.String("mode", d.Mode, "users or rate")
	duration := fs.String("duration", d.Duration, "load duration (ignored when stages are configured)")
	iterations := fs.Int("iterations", 0, "iterations per user; clears duration unless -duration is also supplied")
	rampUp := fs.String("ramp-up", "", "time to stagger user starts; users mode only, e.g. 10s")
	startupDelay := fs.String("startup-delay", "", "wait before the user load window; users mode only")
	onError := fs.String("on-error", "", "users mode: next_iteration, continue, stop_user, stop_test, or stop_test_now")
	vus := fs.Int("vus", d.VUs, "concurrent users in users mode")
	rate := fs.Int("rate", 0, "workload arrivals/second (journeys when scenarios are present); selects rate mode")
	maxInFlight := fs.Int("max-in-flight", d.MaxInFlight, "concurrent workload cap in rate mode")
	distribution := fs.String("arrival-distribution", d.ArrivalDistribution, "constant or jitter; rate mode only")
	seed := fs.Int64("seed", d.Seed, "seed for repeatable arrival jitter")
	journeyTimeout := fs.String("journey-timeout", d.JourneyTimeout, "timeout for the entire journey, including pauses")
	journeyP95 := fs.String("journey-p95", "", "maximum p95 journey latency")
	insecure := fs.Bool("insecure", false, "skip certificate and hostname verification for workload HTTPS requests")
	timeout := fs.String("timeout", d.Timeout, "timeout for each request, including response body")
	think := fs.String("think-time", "", "delay between requests for each user")
	maxBytes := fs.Int64("max-response-bytes", d.MaxResponseBytes, "maximum response body bytes")
	p95 := fs.String("p95", "", "maximum p95 latency, e.g. 200ms")
	maxError := fs.Float64("max-error-percent", 0, "maximum failed requests as a percentage of completed requests")
	maxDrop := fs.Float64("max-drop-percent", 0, "maximum missed workload arrivals as a percentage of scheduled arrivals")
	minRPS := fs.Float64("min-started-rps", 0, "minimum requests dispatched per second")
	base := filepath.Join("results", time.Now().Format("20060102-150405.000000000"))
	jsonPath := fs.String("out", base+".json", "JSON report path; empty disables JSON output")
	htmlPath := fs.String("html", base+".html", "HTML report path; empty disables HTML output")
	quiet := fs.Bool("quiet", false, "disable progress output")
	diagnostics := fs.Bool("diagnostics", true, "record generator resources and detailed HTTP timing")
	workers := fs.String("workers", "", "comma-separated worker origins; rate mode only; authentication uses GOMETER_WORKER_TOKEN")
	workerCA := fs.String("worker-ca", os.Getenv("GOMETER_WORKER_CA"), "optional PEM CA file for worker HTTPS certificates")
	targetTelemetry := fs.Bool("telemetry", true, "collect configured target-server metrics; false disables telemetry")
	telemetryCA := fs.String("telemetry-ca", os.Getenv("GOMETER_TELEMETRY_CA"), "optional PEM CA file for telemetry HTTPS certificates")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if err := applyEnvBool(fs, "allow-http-workers", "GOMETER_ALLOW_HTTP_WORKERS"); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments; use -url or -config")
		return 2
	}
	c, err := load(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !*targetTelemetry {
		c.Telemetry = nil
	}
	if set["url"] {
		c.URL = *url
	}
	if set["name"] {
		c.Name = *name
	}
	if set["method"] {
		c.Method = *method
	}
	if set["body"] {
		c.Body = *body
	}
	if set["mode"] {
		c.Mode = *mode
	}
	if set["duration"] {
		c.Duration = *duration
	}
	if set["iterations"] {
		c.Iterations = *iterations
		if *iterations > 0 && !set["duration"] {
			c.Duration = ""
		}
	}
	if set["vus"] {
		c.VUs = *vus
	}
	if set["ramp-up"] {
		c.RampUp = *rampUp
	}
	if set["startup-delay"] {
		c.StartupDelay = *startupDelay
	}
	if set["on-error"] {
		c.OnError = *onError
	}
	if set["rate"] {
		c.Rate = *rate
		c.Stages = nil
		if !set["mode"] {
			c.Mode = "rate"
		}
	}
	if set["max-in-flight"] {
		c.MaxInFlight = *maxInFlight
	}
	if set["arrival-distribution"] {
		c.ArrivalDistribution = *distribution
	}
	if set["seed"] {
		c.Seed = *seed
	}
	if set["journey-timeout"] {
		c.JourneyTimeout = *journeyTimeout
	}
	if set["journey-p95"] {
		c.JourneyThresholds.P95 = *journeyP95
	}
	if set["insecure"] {
		c.Insecure = *insecure
	}
	if set["timeout"] {
		c.Timeout = *timeout
	}
	if set["think-time"] {
		c.ThinkTime = *think
	}
	if set["max-response-bytes"] {
		c.MaxResponseBytes = *maxBytes
	}
	if set["p95"] {
		c.Thresholds.P95 = *p95
	}
	if set["max-error-percent"] {
		c.Thresholds.MaxErrorPercent = maxError
	}
	if set["max-drop-percent"] {
		c.Thresholds.MaxDropPercent = maxDrop
	}
	if set["min-started-rps"] {
		c.Thresholds.MinStartedRPS = minRPS
	}
	if set["diagnostics"] {
		c.Diagnostics = diagnostics
	}
	if set["workers"] {
		c.Workers = nil
		if strings.TrimSpace(*workers) != "" {
			for _, raw := range strings.Split(*workers, ",") {
				c.Workers = append(c.Workers, strings.TrimSpace(raw))
			}
		}
	}
	if err := csv.apply(fs, &c); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := config.ValidateWorkerTransport(c.Workers, *allowHTTP); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if config.HasRemoteHTTPWorkers(c.Workers) {
		fmt.Fprintln(stderr, "WARNING:", distributed.PlaintextControlWarning)
	}
	p, err := c.Validate()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := distinctPaths(*path, *jsonPath, *htmlPath, *csv.path); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, output := range []string{*jsonPath, *htmlPath} {
		if err := report.CheckPath(output); err != nil {
			fmt.Fprintf(stderr, "prepare report: %v\n", err)
			return 2
		}
	}
	limit := p.MaxInFlight
	if p.Mode == "users" {
		limit = p.VUs
	}
	unit := "requests"
	if p.IsJourney() {
		unit = "journeys"
	}
	fmt.Fprintf(stderr, "GoMeter %s · %s mode · %s · concurrency %d %s · %s arrivals\n%s\n", Version, c.Mode, loadDescription(p), limit, unit, p.ArrivalDistribution, c.URL)
	if c.Insecure {
		fmt.Fprintln(stderr, "TLS verification disabled for workload HTTPS requests. Worker and telemetry TLS verification remains enabled.")
	}
	opts := engine.Options{Telemetry: telemetry.Options{Token: os.Getenv("GOMETER_TELEMETRY_TOKEN"), CAFile: *telemetryCA}}
	if !*quiet {
		opts.OnProgress = func(progress engine.Progress) {
			if progress.Workload == "journeys" {
				fmt.Fprintf(stderr, "[%5.1fs] %d journeys · %.1f journeys/s · %.1f req/s · %.2f%% journey errors · %d journeys dropped\n", progress.Elapsed.Seconds(), progress.Journeys.Completed, progress.Journeys.StartedPerSecond, progress.StartedRPS, progress.Journeys.ErrorPercent, progress.Journeys.DroppedCapacity+progress.Journeys.DroppedScheduler)
			} else {
				fmt.Fprintf(stderr, "[%5.1fs] %d completed · %.1f started req/s · %.2f%% errors · %d dropped\n", progress.Elapsed.Seconds(), progress.Completed, progress.StartedRPS, progress.ErrorPercent, progress.DroppedCapacity+progress.DroppedScheduler)
			}
		}
	}
	var result engine.Result
	if len(c.Workers) > 0 {
		fmt.Fprintf(stderr, "Coordinating %d workers; the configured rate and concurrency are fleet totals.\n", len(c.Workers))
		result, err = distributed.Run(ctx, c, distributed.Options{AllowHTTPWorkers: *allowHTTP, Token: os.Getenv("GOMETER_WORKER_TOKEN"), CAFile: *workerCA, OnProgress: opts.OnProgress, Telemetry: opts.Telemetry})
	} else {
		result, err = engine.Run(ctx, c, opts)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 2
	}
	fmt.Fprint(stdout, result.Summary())
	for _, check := range result.Checks {
		if !check.Passed {
			fmt.Fprintf(stdout, "  Failed %s: %.2f %s (limit %.2f)\n", check.Name, check.Actual, check.Unit, check.Limit)
		}
	}
	failedOutput := false
	for _, artifact := range []struct {
		path   string
		render func(io.Writer) error
	}{
		{*jsonPath, func(w io.Writer) error { return report.JSON(w, result) }},
		{*htmlPath, func(w io.Writer) error { return report.HTML(w, result) }},
	} {
		if artifact.path == "" {
			continue
		}
		if err := report.Write(artifact.path, artifact.render); err != nil {
			fmt.Fprintf(stderr, "write report: %v\n", err)
			failedOutput = true
		} else {
			fmt.Fprintln(stdout, "Report:", artifact.path)
		}
	}
	if result.Interrupted {
		return 130
	}
	if failedOutput {
		return 2
	}
	if !result.Passed {
		return 1
	}
	return 0
}

func loadDescription(p config.Plan) string {
	schedule := ""
	if p.HasUserSchedule() {
		schedule = fmt.Sprintf(" · %s startup delay · %s user ramp", p.UserStartupDelay, p.UserRampUp)
	}
	if p.Iterations > 0 {
		text := fmt.Sprintf("%d iterations/user", p.Iterations)
		if p.LoadDuration > 0 {
			text += fmt.Sprintf(" or %s load, whichever comes first", p.LoadDuration)
		}
		return text + schedule
	}
	return p.LoadDuration.String() + " load" + schedule
}

func distinctPaths(paths ...string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if seen[abs] {
			return fmt.Errorf("configuration and report paths must be distinct")
		}
		seen[abs] = true
	}
	return nil
}
