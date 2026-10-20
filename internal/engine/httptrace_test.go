package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gometer/internal/config"
	"gometer/internal/metrics"
)

func tracedGet(t *testing.T, client *http.Client, url string, c *metrics.Collector) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req, trace := traceRequest(req, time.Now(), c)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	trace.headers(response.Proto)
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	trace.finish(time.Now(), err != nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTraceSeparatesReuseAndDelayedBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(35 * time.Millisecond)
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	client := s.Client()
	c := metrics.New()
	tracedGet(t, client, s.URL, c)
	tracedGet(t, client, s.URL, c)
	h := c.Snapshot(time.Second).HTTP
	if h == nil || h.ConnectionsAcquired != 2 || h.ReusedConnections != 1 || h.Connect.Samples != 1 || h.DNS.Samples != 0 || h.TLS.Samples != 0 || h.FirstByte.Samples != 2 || h.BodyRead.Samples != 2 || h.Protocols["HTTP/1.1"] != 2 {
		t.Fatalf("wrong populations: %+v", h)
	}
	if h.BodyRead.MinMS < 25 {
		t.Fatalf("body delay was lost: %+v", h.BodyRead)
	}
	// Snapshot maps must not alias the collector.
	h.Protocols["HTTP/1.1"] = 0
	if c.Snapshot(time.Second).HTTP.Protocols["HTTP/1.1"] != 2 {
		t.Fatal("snapshot changed live protocol counts")
	}
}

func TestHTTPTraceTLSAndHTTP2(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	client := s.Client()
	client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	c := metrics.New()
	tracedGet(t, client, s.URL, c)
	tracedGet(t, client, s.URL, c)
	h := c.Snapshot(time.Second).HTTP
	if h.TLS.Samples != 1 || h.TLSFailures != 0 || h.Protocols["HTTP/2.0"] != 2 || h.ReusedConnections != 1 {
		t.Fatalf("TLS or HTTP/2 not observed: %+v", h)
	}
}

func TestTraceFailureAndLateCallbacksDoNotInventTiming(t *testing.T) {
	c := metrics.New()
	req, _ := http.NewRequest("GET", "http://127.0.0.1", nil)
	req, trace := traceRequest(req, time.Now(), c)
	hooks := httptrace.ContextClientTrace(req.Context())
	hooks.GetConn("127.0.0.1:80")
	hooks.ConnectStart("tcp", "127.0.0.1:80")
	trace.finish(time.Now(), true)
	before := c.Snapshot(time.Second).HTTP
	if before.ConnectionWait.Samples != 0 || before.Connect.Samples != 0 || before.FirstByte.Samples != 0 || before.BodyRead.Samples != 0 || before.UnfinishedEvents != 2 {
		t.Fatalf("missing events became timing samples: %+v", before)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hooks.ConnectDone("tcp", "127.0.0.1:80", context.Canceled)
			hooks.GotConn(httptrace.GotConnInfo{})
			hooks.GotFirstResponseByte()
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(before, c.Snapshot(time.Second).HTTP) {
		t.Fatal("late trace callbacks mutated frozen results")
	}
}

func TestDiagnosticsCanBeDisabledAndCanceledBeforeHeaders(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c := config.Defaults()
	c.URL = s.URL
	c.Mode = "rate"
	c.Rate = 1
	c.Duration = "10ms"
	c.Timeout = "30ms"
	result, err := Run(context.Background(), c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 3 || result.Generator == nil || result.Generator.Samples < 2 || result.HTTP == nil || result.HTTP.FirstByte.Samples != 0 || result.HTTP.BodyRead.Samples != 0 || result.Failed != 1 {
		t.Fatalf("failed request measurements: %+v", result)
	}
	if summary := result.Summary(); !strings.Contains(summary, "first header byte p95 unavailable · body read p95 unavailable") {
		t.Fatalf("missing timings reported as measurements: %s", summary)
	}
	disabled := false
	c.Diagnostics = &disabled
	result, err = Run(context.Background(), c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Generator != nil || result.HTTP != nil || result.Phases[0].HTTP != nil {
		t.Fatal("disabled diagnostics were still recorded")
	}
}
