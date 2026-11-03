package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogHasNoQueryOrSecrets(t *testing.T) {
	var buf bytes.Buffer
	h := logRequests(slog.New(slog.NewTextHandler(&buf, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, path := range []string{"/login?token=SECRETQ&password=hunter2", "/verify/abcdefghijklmnopqrstuvwxyz0123456789", "/static/x.css", "/healthz", "/internal/logs/ingest"} {
		r := httptest.NewRequest("POST", path, strings.NewReader("password=BODYSECRET"))
		r.Header.Set("Authorization", "Bearer HEADERSECRET")
		r.AddCookie(&http.Cookie{Name: "s", Value: "COOKIESECRET"})
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	out := buf.String()
	for _, bad := range []string{"SECRETQ", "hunter2", "BODYSECRET", "HEADERSECRET", "COOKIESECRET", "abcdefghijklmnop", "static", "healthz", "ingest"} {
		if strings.Contains(out, bad) {
			t.Errorf("log holds %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "path=/login") || !strings.Contains(out, "status=418") || !strings.Contains(out, "path=/verify/{id}") || !strings.Contains(out, "ms=") {
		t.Errorf("missing fields:\n%s", out)
	}
}
