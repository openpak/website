package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openpak/website/internal/turnstile"
)

// GET /auth/config tells the app whether the forms want a Turnstile answer.
func TestAuthConfig(t *testing.T) {
	s, _, _ := newTestAPI(t)
	var out struct {
		Turnstile map[string]any `json:"turnstile"`
	}
	rec := do(t, s, http.MethodGet, "/api/v1/auth/config", "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Turnstile["enabled"] != false || out.Turnstile["site_key"] != nil {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	s.Turnstile = turnstile.New("site-key-1", "secret")
	rec = do(t, s, http.MethodGet, "/api/v1/auth/config", "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Turnstile["enabled"] != true || out.Turnstile["site_key"] != "site-key-1" || out.Turnstile["widget_path"] != "/turnstile-widget" {
		t.Fatalf("on: %s", rec.Body.String())
	}
}

// With Turnstile configured, signup and the reset mail need a passed challenge,
// verified with the caller's address; Cloudflare not answering refuses.
func TestTurnstileGuardsRegisterAndForgot(t *testing.T) {
	var gotIP string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotIP = r.PostFormValue("remoteip")
		if r.PostFormValue("response") == "ok" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer cf.Close()
	s, _, _ := newTestAPI(t)
	s.Turnstile = turnstile.New("site-key-1", "secret")
	s.Turnstile.URL = cf.URL

	code := func(rec *httptest.ResponseRecorder) string {
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out["code"]
	}
	reg := map[string]string{"email": "bot@x.test", "password": "password-N-1", "handle": "botbot1"}
	for _, tok := range []string{"", "bad"} {
		reg["turnstile_token"] = tok
		rec := do(t, s, http.MethodPost, "/api/v1/register", "", reg)
		if rec.Code != http.StatusBadRequest || code(rec) != "turnstile_failed" {
			t.Fatalf("register token %q: %d %s", tok, rec.Code, rec.Body.String())
		}
		rec = do(t, s, http.MethodPost, "/api/v1/password/forgot", "", map[string]string{"email": "a@x.test", "turnstile_token": tok})
		if rec.Code != http.StatusBadRequest || code(rec) != "turnstile_failed" {
			t.Fatalf("forgot token %q: %d %s", tok, rec.Code, rec.Body.String())
		}
	}

	reg = map[string]string{"email": "human@x.test", "password": "password-N-1", "handle": "human1", "turnstile_token": "ok"}
	if rec := do(t, s, http.MethodPost, "/api/v1/register", "", reg); rec.Code != http.StatusAccepted {
		t.Fatalf("passed register: %d %s", rec.Code, rec.Body.String())
	}
	if gotIP == "" {
		t.Error("siteverify was not given the client address")
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/password/forgot", "", map[string]string{"email": "a@x.test", "turnstile_token": "ok"}); rec.Code != http.StatusAccepted {
		t.Fatalf("passed forgot: %d %s", rec.Code, rec.Body.String())
	}

	cf.Close()
	reg["email"] = "late@x.test"
	rec := do(t, s, http.MethodPost, "/api/v1/register", "", reg)
	if rec.Code != http.StatusServiceUnavailable || code(rec) != "turnstile_unavailable" {
		t.Fatalf("unreachable siteverify: %d %s", rec.Code, rec.Body.String())
	}
}
