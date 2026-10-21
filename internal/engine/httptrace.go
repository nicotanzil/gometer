package engine

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"gometer/internal/metrics"
)

const userAgent = "GoMeter/0.8.0"

// A trace retains only pending timestamps. Events go directly to bounded
// aggregate histograms; there are no per-response timing slices to retain.
type requestTrace struct {
	mu                                 sync.Mutex
	start, conn, dns, tls, write, body time.Time
	connects                           map[string]time.Time
	firstByte, closed                  bool
	collectors                         []*metrics.Collector
}

func traceRequest(req *http.Request, start time.Time, collectors ...*metrics.Collector) (*http.Request, *requestTrace) {
	t := &requestTrace{start: start, collectors: collectors, connects: map[string]time.Time{}}
	hooks := &httptrace.ClientTrace{
		GetConn: func(string) {
			t.locked(func(now time.Time) { t.conn = now; t.event(metrics.HTTPEvent{Kind: metrics.HTTPGetConn}) })
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.locked(func(now time.Time) {
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPGotConn, Duration: now.Sub(t.conn), Timed: !t.conn.IsZero(), Reused: info.Reused, Idle: info.WasIdle})
				t.conn = time.Time{}
				t.write = now
			})
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			t.locked(func(now time.Time) {
				if !t.dns.IsZero() {
					t.unfinished()
				}
				t.dns = now
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPDNSStart})
			})
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			t.locked(func(now time.Time) {
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPDNSDone, Duration: now.Sub(t.dns), Timed: !t.dns.IsZero(), Failed: info.Err != nil})
				t.dns = time.Time{}
			})
		},
		ConnectStart: func(network, addr string) {
			t.locked(func(now time.Time) {
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPConnectStart})
				key := network + "\x00" + addr
				if len(t.connects) < 16 {
					if _, ok := t.connects[key]; ok {
						t.unfinished()
					}
					t.connects[key] = now
				} else {
					t.unfinished()
				}
			})
		},
		ConnectDone: func(network, addr string, err error) {
			t.locked(func(now time.Time) {
				key := network + "\x00" + addr
				at, ok := t.connects[key]
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPConnectDone, Duration: now.Sub(at), Timed: ok, Failed: err != nil})
				delete(t.connects, key)
			})
		},
		TLSHandshakeStart: func() {
			t.locked(func(now time.Time) {
				if !t.tls.IsZero() {
					t.unfinished()
				}
				t.tls = now
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPTLSStart})
			})
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			t.locked(func(now time.Time) {
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPTLSDone, Duration: now.Sub(t.tls), Timed: !t.tls.IsZero(), Failed: err != nil})
				t.tls = time.Time{}
			})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.locked(func(now time.Time) {
				t.event(metrics.HTTPEvent{Kind: metrics.HTTPWroteRequest, Duration: now.Sub(t.write), Timed: !t.write.IsZero(), Failed: info.Err != nil})
				t.write = time.Time{}
			})
		},
		GotFirstResponseByte: func() {
			t.locked(func(now time.Time) {
				if !t.firstByte {
					t.event(metrics.HTTPEvent{Kind: metrics.HTTPFirstByte, Duration: now.Sub(t.start), Timed: true})
					t.firstByte = true
				}
			})
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), hooks)), t
}

func (t *requestTrace) locked(fn func(time.Time)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		fn(time.Now())
	}
}
func (t *requestTrace) event(event metrics.HTTPEvent) {
	for _, c := range t.collectors {
		c.ObserveHTTP(event)
	}
}
func (t *requestTrace) unfinished() { t.event(metrics.HTTPEvent{Kind: metrics.HTTPUnfinished}) }

func (t *requestTrace) headers(protocol string) {
	t.locked(func(now time.Time) {
		t.body = now
		t.event(metrics.HTTPEvent{Kind: metrics.HTTPHeaders, Protocol: protocol})
	})
}

// Freeze at the network end so canceled requests cannot mutate a finished
// report through late dial/TLS callbacks. Only completed pairs have durations.
func (t *requestTrace) finish(end time.Time, bodyFailed bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if !t.body.IsZero() {
		t.event(metrics.HTTPEvent{Kind: metrics.HTTPBodyRead, Duration: end.Sub(t.body), Timed: true, Failed: bodyFailed})
	}
	for _, at := range []time.Time{t.conn, t.dns, t.tls, t.write} {
		if !at.IsZero() {
			t.unfinished()
		}
	}
	for range t.connects {
		t.unfinished()
	}
	t.closed = true
}
