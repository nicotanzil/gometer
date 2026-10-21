// Package distributed coordinates bounded, authenticated HTTP worker jobs.
package distributed

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"gometer/internal/config"
	"gometer/internal/diagnostics"
	"gometer/internal/engine"
	"gometer/internal/metrics"
)

const Protocol = 1
const controlLimit = 32 << 20
const leaseDuration = 10 * time.Second
const reservationDuration = 30 * time.Second

type Info struct {
	Protocol int       `json:"protocol"`
	Name     string    `json:"name"`
	Now      time.Time `json:"now"`
	Busy     bool      `json:"busy"`
}
type Prepare struct {
	Config   json.RawMessage `json:"config"`
	Index    int             `json:"index"`
	Count    int             `json:"count"`
	Capacity int             `json:"capacity"`
}
type Ready struct {
	ID string `json:"id"`
}
type Commit struct {
	StartAt time.Time `json:"start_at"`
}
type WorkerProgress struct {
	ElapsedSeconds float64                `json:"elapsed_seconds"`
	LoadSeconds    float64                `json:"load_seconds"`
	PhaseIndex     int                    `json:"phase_index"`
	Requests       metrics.Snapshot       `json:"requests"`
	Journeys       engine.JourneyMetrics  `json:"journeys"`
	Generator      *diagnostics.Resources `json:"generator,omitempty"`
	Live           *engine.LiveData       `json:"live,omitempty"`
}
type Status struct {
	ID       string                `json:"id"`
	State    string                `json:"state"`
	Error    string                `json:"error,omitempty"`
	Progress *WorkerProgress       `json:"progress,omitempty"`
	Result   *engine.Result        `json:"result,omitempty"`
	Data     *engine.AggregateData `json:"data,omitempty"`
}
type job struct {
	Status
	config      config.Config
	assignment  engine.Assignment
	lastContact time.Time
	cancel      context.CancelFunc
	done        chan struct{}
}
type Worker struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	name, token        string
	job                *job
	closed             bool
	watchDone          chan struct{}
	lease, reservation time.Duration
}

func ValidToken(token string) error {
	if len(token) < 32 || len(token) > 512 {
		return fmt.Errorf("GOMETER_WORKER_TOKEN must contain 32–512 printable ASCII characters")
	}
	for _, b := range []byte(token) {
		if b < 33 || b > 126 {
			return fmt.Errorf("GOMETER_WORKER_TOKEN must contain 32–512 printable ASCII characters")
		}
	}
	return nil
}

func NewWorker(ctx context.Context, name, token string) (*Worker, error) {
	if err := ValidToken(token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" || len(name) > 256 {
		return nil, fmt.Errorf("worker name must contain 1–256 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("worker name must not contain control characters")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &Worker{ctx: ctx, cancel: cancel, name: name, token: token, watchDone: make(chan struct{}), lease: leaseDuration, reservation: reservationDuration}
	go w.watch()
	return w, nil
}
func busy(j *job) bool {
	return j != nil && (j.State == "prepared" || j.State == "scheduled" || j.State == "running" || j.State == "stopping")
}
func (w *Worker) watch() {
	defer close(w.watchDone)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case now := <-ticker.C:
			w.mu.Lock()
			if j := w.job; j != nil {
				if j.State == "prepared" && now.Sub(j.lastContact) > w.reservation {
					j.State = "expired"
					j.Error = "worker reservation expired"
				}
				if (j.State == "scheduled" || j.State == "running") && now.Sub(j.lastContact) > w.lease {
					j.Error = "coordinator lease expired"
					j.State = "stopping"
					j.cancel()
				}
			}
			w.mu.Unlock()
		}
	}
}
func (w *Worker) Close() {
	w.mu.Lock()
	w.closed = true
	var done chan struct{}
	if w.job != nil && w.job.cancel != nil {
		w.job.cancel()
		done = w.job.done
	}
	w.cancel()
	w.mu.Unlock()
	<-w.watchDone
	if done != nil {
		<-done
	}
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return fmt.Errorf("control requests must use application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, config.MaxConfigBytes+(64<<10))
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("invalid control request")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("control request must contain one JSON object")
	}
	return nil
}
func (w *Worker) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	out.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(out, 403, "browser-origin worker control is disabled")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+w.token)) != 1 {
		fail(out, 401, "worker authentication failed")
		return
	}
	if r.URL.RawQuery != "" {
		fail(out, 400, "control URLs do not accept query parameters")
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		fail(out, 503, "worker is shutting down")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/info" {
		writeJSON(out, 200, Info{Protocol, w.name, time.Now().UTC(), busy(w.job)})
		return
	}
	if r.Method == "POST" && r.URL.Path == "/v1/jobs" {
		if busy(w.job) {
			fail(out, 409, "worker already has a reserved or active job")
			return
		}
		var input Prepare
		if err := decode(out, r, &input); err != nil {
			fail(out, 400, err.Error())
			return
		}
		c, err := config.Load(bytes.NewReader(input.Config))
		if err != nil {
			fail(out, 400, "invalid worker configuration")
			return
		}
		p, err := c.Validate()
		if err != nil || len(c.Workers) > 0 || c.Telemetry != nil {
			fail(out, 400, "invalid worker configuration")
			return
		}
		a := engine.Assignment{Index: input.Index, Count: input.Count, Capacity: input.Capacity, StartAt: time.Now()}
		if err := a.Validate(p); err != nil {
			fail(out, 400, err.Error())
			return
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			fail(out, 500, "reserve worker job failed")
			return
		}
		id := hex.EncodeToString(random[:])
		w.job = &job{Status: Status{ID: id, State: "prepared"}, config: c, assignment: a, lastContact: time.Now()}
		writeJSON(out, 201, Ready{id})
		return
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segments) < 3 || len(segments) > 4 || segments[0] != "v1" || segments[1] != "jobs" || w.job == nil || segments[2] != w.job.ID {
		fail(out, 404, "worker job not found")
		return
	}
	j := w.job
	if len(segments) == 3 && r.Method == "GET" {
		j.lastContact = time.Now()
		writeJSON(out, 200, j.Status)
		return
	}
	if len(segments) != 4 || r.Method != "POST" {
		fail(out, 404, "worker route not found")
		return
	}
	switch segments[3] {
	case "start":
		var input Commit
		if err := decode(out, r, &input); err != nil {
			fail(out, 400, err.Error())
			return
		}
		if j.State != "prepared" {
			if !j.assignment.StartAt.Equal(input.StartAt) {
				fail(out, 409, "worker job already committed or expired")
				return
			}
			writeJSON(out, 202, Ready{j.ID})
			return
		}
		until := time.Until(input.StartAt)
		if until < 0 || until > 20*time.Second {
			fail(out, 400, "worker start must be within the next 20 seconds")
			return
		}
		j.assignment.StartAt = input.StartAt
		j.lastContact = time.Now()
		j.State = "scheduled"
		ctx, cancel := context.WithCancel(w.ctx)
		j.cancel = cancel
		j.done = make(chan struct{})
		go w.execute(ctx, j)
		writeJSON(out, 202, Ready{j.ID})
	case "cancel":
		if j.State == "prepared" {
			j.State = "canceled"
		} else if j.cancel != nil && busy(j) {
			j.State = "stopping"
			j.cancel()
		}
		writeJSON(out, 202, Ready{j.ID})
	default:
		fail(out, 404, "worker route not found")
	}
}
func (w *Worker) execute(ctx context.Context, j *job) {
	defer close(j.done)
	defer j.cancel()
	result, err := engine.Run(ctx, j.config, engine.Options{Shard: &j.assignment, Capture: true, OnProgress: func(p engine.Progress) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if j.State != "stopping" {
			j.State = "running"
		}
		j.Progress = &WorkerProgress{p.WallElapsed.Seconds(), p.Elapsed.Seconds(), p.PhaseIndex, p.Snapshot, p.Journeys, p.Generator, p.Merge}
	}})
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		j.State = "canceled"
		if j.Error == "" {
			j.Error = "worker stopped before admission"
		}
		return
	}
	j.Result = &result
	j.Data = result.Merge
	result.Merge = nil
	j.State = "finished"
	if result.Interrupted {
		j.State = "interrupted"
	}
}

const PlaintextControlWarning = "Worker control uses plain HTTP: the worker token and test configuration travel without application TLS."

// Non-loopback HTTP listeners require operator opt-in. All listeners require a token.
func ServeWorker(ctx context.Context, address, name, token, cert, key string, allowHTTP bool, output io.Writer) error {
	if err := ValidToken(token); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("worker listen address must include an explicit IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("worker listen address must use an explicit IP")
	}
	if (cert == "") != (key == "") {
		return fmt.Errorf("worker TLS settings require -tls-cert and -tls-key together")
	}
	if !ip.IsLoopback() && cert == "" && !allowHTTP {
		return fmt.Errorf("non-loopback workers require -tls-cert and -tls-key, or explicit -allow-http (GOMETER_WORKER_ALLOW_HTTP=true)")
	}
	if cert != "" {
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			return fmt.Errorf("load worker TLS certificate: %w", err)
		}
	}
	worker, err := NewWorker(ctx, name, token)
	if err != nil {
		return err
	}
	defer worker.Close()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: worker, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	scheme := "http"
	if cert != "" {
		scheme = "https"
	}
	if scheme == "http" && !ip.IsLoopback() {
		fmt.Fprintln(output, "WARNING:", PlaintextControlWarning)
	}
	fmt.Fprintf(output, "GoMeter worker %s: %s://%s\n", name, scheme, listener.Addr())
	done := make(chan error, 1)
	go func() {
		if cert != "" {
			done <- server.ServeTLS(listener, cert, key)
		} else {
			done <- server.Serve(listener)
		}
	}()
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		worker.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
		server.Close()
		<-done
	}
	return nil
}
