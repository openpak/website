package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The core's answers for a closed account: 409 with the date from /v1/login,
// a session from /v1/login/restore, 410 once the grace period is over.
func TestLoginDeletionPendingAndRestore(t *testing.T) {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /v1/login", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusConflict, map[string]any{"error": "account closed", "deletion_pending": true, "erase_after": "2026-11-22T12:00:00Z"})
	})
	mux.HandleFunc("POST /v1/login/restore", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Email string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Email == "late@x.test" {
			write(w, http.StatusGone, map[string]string{"error": "the grace period is over"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "openpak_session", Value: "sess-r"})
		write(w, http.StatusOK, map[string]string{"message": "signed in", "account_id": "acct-1"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := NewHTTP(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = c.Login(ctx, "a@x.test", "pw")
	var pending *DeletionPending
	if !errors.As(err, &pending) || !pending.EraseAfter.Equal(time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("login = %v", err)
	}
	if sess, err := c.RestoreLogin(ctx, "a@x.test", "pw"); err != nil || sess != "sess-r" {
		t.Fatalf("restore = %q %v", sess, err)
	}
	if _, err := c.RestoreLogin(ctx, "late@x.test", "pw"); !errors.Is(err, ErrErasureStarted) {
		t.Fatalf("late restore = %v", err)
	}
}
