package session

import (
	"net/http/httptest"
	"testing"
	"time"
)

func newManager() *Manager {
	return New("opak_session", "secret-0123456789abcdef-secret-0123456789!", false, time.Hour)
}

func TestCookieRoundTrip(t *testing.T) {
	m := newManager()
	rec := httptest.NewRecorder()
	m.SetCookie(rec, "core-sess-1")

	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "opak_session" {
			req.AddCookie(c)
		}
	}
	if got := m.CoreSessionID(req); got != "core-sess-1" {
		t.Fatalf("CoreSessionID = %q", got)
	}
}

func TestCSRFIsSessionBound(t *testing.T) {
	m := newManager()
	tok := m.CSRFToken("sess-a")
	if !m.CheckCSRF("sess-a", tok) {
		t.Fatal("valid CSRF rejected")
	}
	if m.CheckCSRF("sess-b", tok) {
		t.Fatal("CSRF must not transfer between sessions")
	}
	if m.CheckCSRF("sess-a", "") || m.CheckCSRF("", tok) {
		t.Fatal("empty session or token must fail")
	}
}

func TestBearerHashingIsStableAndDistinct(t *testing.T) {
	m := newManager()
	raw, err := NewBearer()
	if err != nil || len(raw) < 40 {
		t.Fatalf("bearer: %v %q", err, raw)
	}
	if m.HashToken(raw) != m.HashToken(raw) {
		t.Fatal("hash must be deterministic")
	}
	if m.HashToken(raw) == m.HashToken(raw+"x") {
		t.Fatal("different tokens must hash differently")
	}
	if m.HashToken(raw) == raw {
		t.Fatal("hash must never be the raw token")
	}
}
