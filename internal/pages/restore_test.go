package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Signing in to a closed account offers its restore, with the erasure date,
// and only to the right password; the restore signs in; once the grace period
// is over the answer is that it is gone.
func TestLoginOffersRestore(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	fc.closed = map[string]bool{"a@x.test": true}
	mux := s.Handler()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {"a@x.test"}, "password": {"wrong-password"}}))
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "/login/restore") {
		t.Fatalf("wrong password on a closed account: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {"a@x.test"}, "password": {"password-A-1"}}))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `action="/login/restore"`) || !strings.Contains(body, "22 November 2026") ||
		strings.Contains(body, "password-A-1") {
		t.Fatalf("restore offer: %d %s", rec.Code, body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login/restore", url.Values{"email": {"a@x.test"}, "password": {"password-A-1"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" || fc.closed["a@x.test"] {
		t.Fatalf("restore: %d %s closed=%v", rec.Code, rec.Header().Get("Location"), fc.closed)
	}

	fc.erasing = map[string]bool{"b@x.test": true}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login/restore", url.Values{"email": {"b@x.test"}, "password": {"password-B-1"}}))
	if rec.Code != http.StatusGone {
		t.Fatalf("restore after the grace: %d", rec.Code)
	}
}
