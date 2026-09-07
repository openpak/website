// Package metrics serves the minimal metrics/pprof listener on the
// website's +2 port (port plan: WEBSITE_METRICS_ADDR, default :20012).
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
}

// New starts the clock.
func New() *Metrics {
	return &Metrics{requests: map[string]uint64{}, start: time.Now()}
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
	b = append(b, "# HELP openpak_website_uptime_seconds Process uptime in seconds.\n"...)
	b = append(b, "# TYPE openpak_website_uptime_seconds gauge\n"...)
	b = append(b, fmt.Sprintf("openpak_website_uptime_seconds %d\n", int64(time.Since(m.start).Seconds()))...)
	keys := make([]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b = append(b, "# HELP openpak_website_http_requests_total HTTP requests processed.\n"...)
	b = append(b, "# TYPE openpak_website_http_requests_total counter\n"...)
	for _, k := range keys {
		var code int
		var method string
		if _, err := fmt.Sscanf(k, "%d|%s", &code, &method); err == nil {
			b = append(b, fmt.Sprintf("openpak_website_http_requests_total{code=\"%d\",method=\"%s\"} %d\n",
				code, method, m.requests[k])...)
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
