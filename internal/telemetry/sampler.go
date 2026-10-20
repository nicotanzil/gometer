package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Token, CAFile string
	Client        *http.Client
}
type Snapshot struct {
	Sequence       int       `json:"sequence"`
	ElapsedSeconds float64   `json:"elapsed_seconds"`
	ObservedAt     time.Time `json:"observed_at"`
	DurationMS     float64   `json:"duration_ms"`
	Error          string    `json:"error,omitempty"`
	Values         []Value   `json:"values"`
}
type Series struct {
	Metric              Metric   `json:"metric"`
	Samples             int      `json:"samples"`
	Missing             int      `json:"missing"`
	Resets              int      `json:"resets"`
	FirstElapsedSeconds float64  `json:"first_elapsed_seconds"`
	LastElapsedSeconds  float64  `json:"last_elapsed_seconds"`
	First               *float64 `json:"first,omitempty"`
	Last                *float64 `json:"last,omitempty"`
	Min                 *float64 `json:"min,omitempty"`
	Max                 *float64 `json:"max,omitempty"`
	Mean                *float64 `json:"mean,omitempty"`
	ObservedIncrease    *float64 `json:"observed_increase,omitempty"`
	CoveredSeconds      float64  `json:"covered_seconds"`
	MeanRate            *float64 `json:"mean_rate,omitempty"`
}
type Summary struct {
	URL               string     `json:"url"`
	IntervalSeconds   float64    `json:"interval_seconds"`
	TimeoutSeconds    float64    `json:"timeout_seconds"`
	Attempts          int        `json:"attempts"`
	FailedScrapes     int        `json:"failed_scrapes"`
	CanceledScrapes   int        `json:"canceled_scrapes"`
	IncompleteScrapes int        `json:"incomplete_scrapes"`
	Complete          bool       `json:"complete"`
	Truncated         bool       `json:"truncated"`
	Series            []Series   `json:"series"`
	Timeline          []Snapshot `json:"timeline"`
}
type previous struct {
	value float64
	at    time.Time
	valid bool
}
type Sampler struct {
	mu       sync.Mutex
	plan     Plan
	client   *http.Client
	token    string
	start    time.Time
	cancel   context.CancelFunc
	done     chan struct{}
	begun    bool
	summary  Summary
	previous []previous
}

// New performs credential/TLS setup without contacting a target. Secret values
// are private runtime options and never enter the configuration or result.
func New(c Config, opts Options) (*Sampler, error) {
	p, err := c.Compile()
	if err != nil {
		return nil, err
	}
	if p.BearerAuth {
		if len(opts.Token) < 1 || len(opts.Token) > 4096 {
			return nil, fmt.Errorf("GOMETER_TELEMETRY_TOKEN is required for bearer telemetry (1–4096 ASCII characters without whitespace)")
		}
		for _, b := range []byte(opts.Token) {
			if b < 33 || b > 126 {
				return nil, fmt.Errorf("GOMETER_TELEMETRY_TOKEN must contain ASCII characters without whitespace")
			}
		}
	}
	var client http.Client
	if opts.Client != nil {
		client = *opts.Client
	} else {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if opts.CAFile != "" {
			data, e := os.ReadFile(opts.CAFile)
			if e != nil {
				return nil, fmt.Errorf("read telemetry CA file: %w", e)
			}
			if !roots.AppendCertsFromPEM(data) {
				return nil, fmt.Errorf("telemetry CA file has no certificates")
			}
		}
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableCompression: true}
	}
	client.Timeout = p.Limit
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &Sampler{plan: p, client: &client, token: opts.Token, previous: make([]previous, len(p.Metrics)), summary: Summary{URL: p.URL, IntervalSeconds: p.Every.Seconds(), TimeoutSeconds: p.Limit.Seconds()}}
	for _, m := range p.Metrics {
		s.summary.Series = append(s.summary.Series, Series{Metric: m})
	}
	return s, nil
}
func wait(ctx context.Context, at time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	d := time.Until(at)
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
func (s *Sampler) Start(ctx context.Context, start time.Time) {
	s.start = start
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		if !wait(ctx, start) {
			return
		}
		s.mu.Lock()
		s.begun = true
		s.mu.Unlock()
		next := start
		for {
			s.scrape(ctx)
			next = next.Add(s.plan.Every)
			if now := time.Now(); !next.After(now) {
				next = next.Add((now.Sub(next)/s.plan.Every + 1) * s.plan.Every)
			}
			if !wait(ctx, next) {
				return
			}
		}
	}()
}
func (s *Sampler) Close() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	s.client.CloseIdleConnections()
}
func clonePoint(p Snapshot) Snapshot {
	p.Values = append([]Value(nil), p.Values...)
	for i := range p.Values {
		if v := p.Values[i].Value; v != nil {
			p.Values[i].Value = number(*v)
		}
		if v := p.Values[i].Rate; v != nil {
			p.Values[i].Rate = number(*v)
		}
	}
	return p
}
func (s *Sampler) Live() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.summary.Timeline) == 0 {
		return nil
	}
	p := clonePoint(s.summary.Timeline[len(s.summary.Timeline)-1])
	return &p
}
func (s *Sampler) Finish() Summary {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	s.mu.Lock()
	begun := s.begun
	s.mu.Unlock()
	if begun {
		ctx, cancel := context.WithTimeout(context.Background(), s.plan.Limit)
		s.scrape(ctx)
		cancel()
	}
	s.client.CloseIdleConnections()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.summary
	r.Complete = r.Attempts > 0 && r.FailedScrapes == 0 && r.IncompleteScrapes == 0
	r.Series = append([]Series(nil), r.Series...)
	r.Timeline = append([]Snapshot(nil), r.Timeline...)
	for i := range r.Timeline {
		r.Timeline[i] = clonePoint(r.Timeline[i])
	}
	return r
}
func (s *Sampler) scrape(ctx context.Context) {
	before := time.Now()
	values, err := s.fetch(ctx)
	if err != nil && ctx.Err() == context.Canceled {
		s.mu.Lock()
		s.summary.CanceledScrapes++
		s.mu.Unlock()
		return
	}
	at := time.Now()
	problem := ""
	if err != nil {
		problem = err.Error()
	}
	s.record(at, at.Sub(before), values, problem)
}
func (s *Sampler) fetch(ctx context.Context) ([]Value, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", s.plan.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid telemetry request")
	}
	r.Header.Set("Accept", "text/plain;version=0.0.4")
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("User-Agent", "GoMeter/0.6.0 telemetry")
	if s.plan.BearerAuth {
		r.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("telemetry request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("telemetry returned HTTP %d", response.StatusCode)
	}
	media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "text/plain" || (params["version"] != "" && params["version"] != "0.0.4") || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
		return nil, fmt.Errorf("telemetry endpoint must return uncompressed Prometheus text 0.0.4")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxBody+1))
	if err != nil || len(data) > MaxBody {
		return nil, fmt.Errorf("telemetry body is incomplete or exceeds 1 MiB")
	}
	return parse(data, s.plan.Metrics)
}
func (s *Sampler) record(at time.Time, duration time.Duration, values []Value, problem string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary.Attempts++
	p := Snapshot{Sequence: s.summary.Attempts, ElapsedSeconds: max(0, at.Sub(s.start).Seconds()), ObservedAt: at.UTC(), DurationMS: float64(duration) / 1e6, Error: problem, Values: make([]Value, len(s.plan.Metrics))}
	if problem != "" {
		s.summary.FailedScrapes++
	}
	missing := false
	for i := range s.summary.Series {
		v := Value{Issue: "scrape failed"}
		if problem == "" {
			v = values[i]
		}
		stats := &s.summary.Series[i]
		prev := s.previous[i]
		if v.Value == nil {
			missing = true
			stats.Missing++
			s.previous[i].valid = false
		} else {
			value := *v.Value
			stats.Samples++
			if stats.Samples == 1 {
				stats.FirstElapsedSeconds = p.ElapsedSeconds
				stats.First = number(value)
				stats.Min = number(value)
				stats.Max = number(value)
				stats.Mean = number(value)
			} else {
				stats.Min = number(min(*stats.Min, value))
				stats.Max = number(max(*stats.Max, value))
				stats.Mean = number(*stats.Mean + (value-*stats.Mean)/float64(stats.Samples))
			}
			stats.Last = number(value)
			stats.LastElapsedSeconds = p.ElapsedSeconds
			if stats.Metric.Kind == "counter" && prev.valid {
				if value < prev.value {
					stats.Resets++
					v.Reset = true
				} else if seconds := at.Sub(prev.at).Seconds(); seconds > 0 {
					delta := value - prev.value
					v.Rate = number(delta / seconds)
					increase := delta
					if stats.ObservedIncrease != nil {
						increase += *stats.ObservedIncrease
					}
					stats.ObservedIncrease = number(increase)
					stats.CoveredSeconds += seconds
					stats.MeanRate = number(increase / stats.CoveredSeconds)
				}
			}
			s.previous[i] = previous{value, at, true}
		}
		p.Values[i] = v
	}
	if missing && problem == "" {
		s.summary.IncompleteScrapes++
	}
	if len(s.summary.Timeline) == MaxPoints {
		copy(s.summary.Timeline, s.summary.Timeline[1:])
		s.summary.Timeline = s.summary.Timeline[:MaxPoints-1]
		s.summary.Truncated = true
	}
	s.summary.Timeline = append(s.summary.Timeline, p)
}
