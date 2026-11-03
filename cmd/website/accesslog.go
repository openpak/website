package main

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// logRequests writes one line per request: method, path, status, duration.
// Never the query, headers, cookies or body. Opaque path segments (reset and
// verification tokens, long ids) are masked so a link token cannot reach the
// log. Static files, health probes and metrics are left out.
func logRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipAccessLog(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Info("http", "method", r.Method, "path", safePath(r.URL.Path), "status", sw.status,
				"ms", time.Since(start).Milliseconds())
		})
	}
}

func skipAccessLog(p string) bool {
	return strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/healthz") || p == "/metrics" ||
		strings.HasPrefix(p, "/internal/logs/")
}

var opaqueSegment = regexp.MustCompile(`^[A-Za-z0-9_-]{24,}$`)

func safePath(p string) string {
	if len(p) > 200 {
		p = p[:200]
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if opaqueSegment.MatchString(s) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

// statusWriter records the status; Unwrap keeps http.ResponseController
// (flush, hijack) working through it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int)        { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
