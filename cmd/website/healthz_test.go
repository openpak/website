package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeHealth struct{ err error }

func (f fakeHealth) Healthy(context.Context) error { return f.err }

func TestHealthzBehind(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz/{service}", healthzBehind(map[string]healthChecker{
		"saves": fakeHealth{},
		"chat":  fakeHealth{errors.New("down")},
	}))
	for path, want := range map[string]int{
		"/healthz/saves": http.StatusOK,
		"/healthz/chat":  http.StatusServiceUnavailable,
		"/healthz/nope":  http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}
