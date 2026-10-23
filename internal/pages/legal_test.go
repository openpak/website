package pages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The alpha strip and the legal links are on every page of the site shell.
func TestAlphaStripAndLegalFooter(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, path := range []string{"/", "/titles", "/features", "/downloads", "/status", "/login", "/register", "/forgot-password", "/help", "/terms", "/privacy", "/legal"} {
		rec := get(t, s, path)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		for _, want := range []string{`class="alpha-strip"`, "OpenPak is in public alpha", `href="/terms"`, `href="/privacy"`, `href="/legal"`, "mailto:contact@openpak.org", "SUPPORT.md"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
	}
}

func TestLegalPages(t *testing.T) {
	s, _, _ := newTestServer(t)
	for path, want := range map[string][]string{
		"/terms":   {"Terms of use", "the OpenPak project", "at least 16", "AGPL", "pirated"},
		"/privacy": {"Privacy policy", "the OpenPak project", "opak_session", "Cloudflare", "Delete account", "contact@openpak.org"},
		"/legal":   {"Nintendo", "Microsoft", "Sony", "takedown", "contact@openpak.org"},
	} {
		body := get(t, s, path).Body.String()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
		if strings.Contains(body, "OWNER REVIEW") {
			t.Errorf("%s: the review note must stay in the template, not reach visitors", path)
		}
	}
	// A translated shell says the text itself is English.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	r.Header.Set("Accept-Language", "de")
	s.Handler().ServeHTTP(rec, r)
	if !strings.Contains(rec.Body.String(), "nur auf Englisch") {
		t.Error("German visitors should be told the policy is English only")
	}
}

func TestRegisterNotices(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/register").Body.String()
	if !strings.Contains(body, `notice warn`) || !strings.Contains(body, "you agree to the Terms of use") {
		t.Error("the register form should carry the alpha notice and the terms agreement")
	}
	if strings.Contains(get(t, s, "/").Body.String(), "opened or closed by the operator") {
		t.Error("the landing page still has operator-speak")
	}
}

func TestSecurityTxt(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := get(t, s, "/.well-known/security.txt")
	body := rec.Body.String()
	for _, want := range []string{"Contact: mailto:contact@openpak.org", "Expires: 2027-", "Policy: https://github.com/openpak/.github/blob/main/SECURITY.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("security.txt: missing %q", want)
		}
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Error("security.txt must be text/plain")
	}
}

func TestHelpContactSignedOut(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/help").Body.String()
	if !strings.Contains(body, `<a href="mailto:contact@openpak.org">contact@openpak.org</a>`) {
		t.Error("signed-out help should show the contact address")
	}
}
