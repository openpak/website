package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/turnstile"
)

func regForm(email, birthday string) url.Values {
	return url.Values{"email": {email}, "password": {"password-A-1"}, "handle": {"alice1"}, "birthday": {birthday}}
}

func TestRegisterMinimumAge(t *testing.T) {
	s, fc, _ := newTestServer(t)
	s.MinAge = 16
	young := time.Now().AddDate(-15, 0, 0).Format("2006-01-02")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/register", regForm("kid@x.test", young)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at least 16") {
		t.Fatalf("under-age signup: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := fc.accounts["kid@x.test"]; ok {
		t.Fatal("an under-age signup must not reach the core")
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/register", regForm("adult@x.test", "1990-05-01")))
	if rec.Code != http.StatusOK {
		t.Fatalf("adult signup: %d", rec.Code)
	}
	if _, ok := fc.accounts["adult@x.test"]; !ok {
		t.Fatal("an adult signup should reach the core")
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/register", nil))
	if !strings.Contains(rec.Body.String(), "at least 16") {
		t.Error("the form should state the minimum age")
	}
}

func TestRegisterMailThrottle(t *testing.T) {
	s, fc, _ := newTestServer(t)
	s.MailLimit = accounts.NewMailLimit()
	for i := 0; i < accounts.MailPerHour+1; i++ {
		delete(fc.accounts, "a@x.test")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, post("/register", regForm("A@x.test", "1990-05-01")))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "verification email") {
			t.Fatalf("attempt %d: the answer must look the same: %d", i+1, rec.Code)
		}
		_, reached := fc.accounts["a@x.test"]
		if want := i < accounts.MailPerHour; reached != want {
			t.Fatalf("attempt %d: reached core = %v, want %v", i+1, reached, want)
		}
	}
}

func TestTurnstileGuardsForms(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, _ = w.Write([]byte(`{"success":` + map[bool]string{true: "true", false: "false"}[r.PostFormValue("response") == "ok"] + `}`))
	}))
	defer cf.Close()
	s, fc, _ := newTestServer(t)
	s.Turnstile = turnstile.New("site-key-1", "secret")
	s.Turnstile.URL = cf.URL

	for _, path := range []string{"/register", "/forgot-password"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()
		if !strings.Contains(body, `data-sitekey="site-key-1"`) || !strings.Contains(body, "challenges.cloudflare.com/turnstile") {
			t.Errorf("%s: widget missing", path)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src https://challenges.cloudflare.com") {
			t.Errorf("%s: CSP does not allow the widget: %s", path, csp)
		}
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/register", regForm("bot@x.test", "1990-05-01")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no answer: %d", rec.Code)
	}
	if _, ok := fc.accounts["bot@x.test"]; ok {
		t.Fatal("a signup without a passed challenge must not reach the core")
	}
	f := regForm("human@x.test", "1990-05-01")
	f.Set(turnstile.FormField, "ok")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/register", f))
	if _, ok := fc.accounts["human@x.test"]; rec.Code != http.StatusOK || !ok {
		t.Fatalf("passed challenge: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/forgot-password", url.Values{"email": {"a@x.test"}}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forgot without answer: %d", rec.Code)
	}
	// Fail closed when Cloudflare does not answer.
	cf.Close()
	f.Set("email", "late@x.test")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/register", f))
	if _, ok := fc.accounts["late@x.test"]; rec.Code != http.StatusBadRequest || ok {
		t.Fatalf("unreachable siteverify must refuse: %d", rec.Code)
	}
}

func TestNoTurnstileByDefault(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/register", nil))
	if strings.Contains(rec.Body.String(), "cf-turnstile") || strings.Contains(rec.Header().Get("Content-Security-Policy"), "cloudflare") {
		t.Error("an unconfigured site must not draw the widget or widen the CSP")
	}
}
