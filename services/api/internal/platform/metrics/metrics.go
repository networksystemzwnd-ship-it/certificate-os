// Package metrics is a tiny, dependency-free metrics registry that renders the Prometheus text format (M6b).
//
// Rules that keep it safe: label values are chosen by the code (method, status class, error code), never taken from
// requests or personal data; there are no per-path or per-user labels (cardinality and privacy); the scrape endpoint
// is disabled unless a token is configured and requires it as a bearer token. All methods are safe on a nil *Registry
// so call sites never need a nil check.
package metrics

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	mu       sync.Mutex
	help     map[string]string
	counters map[string]map[string]float64 // name -> rendered labels -> value
	hists    map[string]map[string]*hist
	gauges   []gaugeFunc
}

type hist struct {
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

// Sample is one gauge value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

type gaugeFunc struct {
	name string
	fn   func(ctx context.Context) []Sample
}

func New() *Registry {
	return &Registry{help: map[string]string{}, counters: map[string]map[string]float64{}, hists: map[string]map[string]*hist{}}
}

// DefaultBuckets are request-duration buckets in seconds.
var DefaultBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

func renderLabels(pairs []string) string {
	if len(pairs) < 2 {
		return ""
	}
	type kv struct{ k, v string }
	var kvs []kv
	for i := 0; i+1 < len(pairs); i += 2 {
		kvs = append(kvs, kv{pairs[i], pairs[i+1]})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].k < kvs[j].k })
	var b strings.Builder
	b.WriteByte('{')
	for i, e := range kvs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, e.k, escape(e.v))
	}
	b.WriteByte('}')
	return b.String()
}

func escape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// Describe sets the help text of a metric.
func (r *Registry) Describe(name, help string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.help[name] = help
	r.mu.Unlock()
}

// Inc adds 1 to a counter. labels are key, value pairs.
func (r *Registry) Inc(name string, labels ...string) { r.Add(name, 1, labels...) }

func (r *Registry) Add(name string, v float64, labels ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.counters[name]
	if m == nil {
		m = map[string]float64{}
		r.counters[name] = m
	}
	m[renderLabels(labels)] += v
}

// Observe records a value in a histogram with DefaultBuckets.
func (r *Registry) Observe(name string, v float64, labels ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.hists[name]
	if m == nil {
		m = map[string]*hist{}
		r.hists[name] = m
	}
	key := renderLabels(labels)
	h := m[key]
	if h == nil {
		h = &hist{buckets: DefaultBuckets, counts: make([]uint64, len(DefaultBuckets))}
		m[key] = h
	}
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.count++
}

// GaugeFunc registers a gauge computed at scrape time (for example queue depth from the database).
func (r *Registry) GaugeFunc(name, help string, fn func(ctx context.Context) []Sample) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.help[name] = help
	r.gauges = append(r.gauges, gaugeFunc{name, fn})
}

// Render writes the Prometheus text exposition format.
func (r *Registry) Render(ctx context.Context) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	gauges := append([]gaugeFunc(nil), r.gauges...)
	var b strings.Builder
	head := func(name, typ string) {
		if h := r.help[name]; h != "" {
			fmt.Fprintf(&b, "# HELP %s %s\n", name, strings.NewReplacer("\n", " ").Replace(h))
		}
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, typ)
	}
	for _, name := range sortedKeys(r.counters) {
		head(name, "counter")
		for _, l := range sortedKeys(r.counters[name]) {
			fmt.Fprintf(&b, "%s%s %g\n", name, l, r.counters[name][l])
		}
	}
	for _, name := range sortedKeys(r.hists) {
		head(name, "histogram")
		for _, l := range sortedKeys(r.hists[name]) {
			h := r.hists[name][l]
			base := strings.TrimSuffix(strings.TrimPrefix(l, "{"), "}")
			join := func(extra string) string {
				if base == "" {
					return "{" + extra + "}"
				}
				return "{" + base + "," + extra + "}"
			}
			for i, bk := range h.buckets {
				fmt.Fprintf(&b, "%s_bucket%s %d\n", name, join(fmt.Sprintf(`le="%g"`, bk)), h.counts[i])
			}
			fmt.Fprintf(&b, "%s_bucket%s %d\n", name, join(`le="+Inf"`), h.count)
			fmt.Fprintf(&b, "%s_sum%s %g\n%s_count%s %d\n", name, l, h.sum, name, l, h.count)
		}
	}
	r.mu.Unlock()
	// Gauges run without the lock: they may query the database.
	for _, g := range gauges {
		samples := g.fn(ctx)
		r.mu.Lock()
		head(g.name, "gauge")
		r.mu.Unlock()
		sort.Slice(samples, func(i, j int) bool { return renderLabelMap(samples[i].Labels) < renderLabelMap(samples[j].Labels) })
		for _, s := range samples {
			fmt.Fprintf(&b, "%s%s %g\n", g.name, renderLabelMap(s.Labels), s.Value)
		}
	}
	return b.String()
}

func renderLabelMap(m map[string]string) string {
	var p []string
	for k, v := range m {
		p = append(p, k, v)
	}
	return renderLabels(p)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Handler serves the scrape endpoint. With an empty token it answers 404 (disabled); otherwise it requires
// "Authorization: Bearer <token>" (constant-time compare, 401 without it). Never cached.
func (r *Registry) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if token == "" {
			http.NotFound(w, req)
			return
		}
		got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if got == req.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(r.Render(ctx)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// HTTP counts requests by method and status class and observes their duration. No path, no user, no institute.
func (r *Registry) HTTP(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	r.Describe("cos_http_requests_total", "HTTP requests by method and status class.")
	r.Describe("cos_http_request_duration_seconds", "HTTP request duration in seconds.")
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, req)
		method := req.Method
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			method = "OTHER"
		}
		class := fmt.Sprintf("%dxx", rec.code/100)
		r.Inc("cos_http_requests_total", "method", method, "class", class)
		r.Observe("cos_http_request_duration_seconds", time.Since(start).Seconds(), "method", method)
	})
}
