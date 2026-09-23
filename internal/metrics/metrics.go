// Package metrics serves the minimal metrics/pprof listener on a service's
// +2 port (port plan: WEBSITE_METRICS_ADDR, default :20012; the registry's
// REGISTRY_METRICS_ADDR, default :20142).
// Hand-rolled Prometheus text output: process uptime and HTTP request
// counters. pprof is mounted alongside for live debugging.
package metrics

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"sort"
	"sync"
	"time"
)

// Metrics accumulates the small counter set the status dashboards use.
type Metrics struct {
	mu       sync.Mutex
	requests map[string]uint64 // key: "code|method"
	start    time.Time
	prefix   string // "openpak_<service>_"
}

// New starts the clock for the website's series.
func New() *Metrics { return NewFor("website") }

// NewFor starts the clock for another service in this repository; its
// series are named openpak_<service>_*, so two processes never collide.
func NewFor(service string) *Metrics {
	return &Metrics{requests: map[string]uint64{}, start: time.Now(), prefix: "openpak_" + service + "_"}
}

// IncRequest records one finished HTTP request.
func (m *Metrics) IncRequest(code int, method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[fmt.Sprintf("%d|%s", code, method)]++
}

// statusWriter captures the response code for the middleware.
type statusWriter struct {
	http.ResponseWriter
	code int
}

// Unwrap lets http.ResponseController and WebSocket upgrades reach the
// connection underneath (the voice-chat socket hijacks it).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware wraps a handler, counting requests by outcome.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		m.IncRequest(sw.code, r.Method)
	})
}

// render produces the Prometheus text exposition format.
func (m *Metrics) render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b []byte
	b = append(b, "# HELP "+m.prefix+"uptime_seconds Process uptime in seconds.\n"...)
	b = append(b, "# TYPE "+m.prefix+"uptime_seconds gauge\n"...)
	b = append(b, fmt.Sprintf("%suptime_seconds %d\n", m.prefix, int64(time.Since(m.start).Seconds()))...)
	keys := make([]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b = append(b, "# HELP "+m.prefix+"http_requests_total HTTP requests processed.\n"...)
	b = append(b, "# TYPE "+m.prefix+"http_requests_total counter\n"...)
	for _, k := range keys {
		var code int
		var method string
		if _, err := fmt.Sscanf(k, "%d|%s", &code, &method); err == nil {
			b = append(b, fmt.Sprintf("%shttp_requests_total{code=\"%d\",method=\"%s\"} %d\n",
				m.prefix, code, method, m.requests[k])...)
		}
	}
	return string(b)
}

// Handler returns the metrics/pprof mux.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.render()))
	})
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
