package pages

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/sso"
)

const ssoCB = "https://splat.openpak.org/auth/callback"

func ssoServer(t *testing.T) (*Server, *fakeCore) {
	t.Helper()
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	s.SSO = sso.New([]sso.Client{{ID: "opensplat", Secret: "sekret", Origins: []string{"https://splat.openpak.org"}}})
	return s, fc
}

func authorizeURL(extra url.Values) string {
	q := url.Values{"client": {"opensplat"}, "redirect_uri": {ssoCB}, "state": {"state-12345678"}}
	for k, v := range extra {
		q[k] = v
	}
	return "/account/sso/authorize?" + q.Encode()
}

func exchange(s *Server, form url.Values, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/sso/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("opensplat", secret)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// A signed-out visitor goes through the login and comes back; signed in, the code is issued.
func TestSSOFlow(t *testing.T) {
	s, _ := ssoServer(t)
	target := authorizeURL(nil)
	w := getPath(s, target)
	loc := w.Header().Get("Location")
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("signed out: %d %q", w.Code, loc)
	}
	// The login form keeps the way back (linkNext lets the authorize path through).
	next, _ := url.Parse(loc)
	if got := linkNext(next.Query().Get("next")); !strings.HasPrefix(got, "/account/sso/authorize?") {
		t.Fatalf("login drops the way back: %q", got)
	}

	rec := login(t, s, "a@x.test", "password-A-1")
	w = authedRequest(s, rec, http.MethodGet, target, nil)
	back, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil || back.Host != "splat.openpak.org" || back.Query().Get("state") != "state-12345678" || back.Query().Get("code") == "" {
		t.Fatalf("signed in: %d %q", w.Code, w.Header().Get("Location"))
	}
	code := back.Query().Get("code")

	form := url.Values{"code": {code}, "redirect_uri": {ssoCB}}
	if w = exchange(s, form, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	w = exchange(s, form, "sekret")
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out["account_id"] != "acct-a@x.test" {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	if _, has := out["wiiu_pid"]; !has {
		t.Errorf("wiiu_pid is not in the answer: %s", w.Body.String())
	}
	if w = exchange(s, form, "sekret"); w.Code != http.StatusBadRequest {
		t.Fatalf("code reuse: %d", w.Code)
	}
}

func TestSSORedirectMismatch(t *testing.T) {
	s, _ := ssoServer(t)
	rec := login(t, s, "a@x.test", "password-A-1")
	for _, uri := range []string{"https://evil.test/cb", "http://splat.openpak.org/cb", "https://splat.openpak.org@evil.test/", "https://evil.openpak.org/cb"} {
		w := authedRequest(s, rec, http.MethodGet, authorizeURL(url.Values{"redirect_uri": {uri}}), nil)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d %q", uri, w.Code, w.Header().Get("Location"))
		}
	}
	// No state, or an unknown client, is refused too.
	for _, q := range []url.Values{{"state": {""}}, {"client": {"nope"}}, {"code_challenge": {"short"}}} {
		if w := authedRequest(s, rec, http.MethodGet, authorizeURL(q), nil); w.Code != http.StatusBadRequest {
			t.Errorf("%v: %d", q, w.Code)
		}
	}
	// A code for one redirect_uri cannot be exchanged for another.
	w := authedRequest(s, rec, http.MethodGet, authorizeURL(nil), nil)
	back, _ := url.Parse(w.Header().Get("Location"))
	bad := exchange(s, url.Values{"code": {back.Query().Get("code")}, "redirect_uri": {"https://splat.openpak.org/elsewhere"}}, "sekret")
	if bad.Code != http.StatusBadRequest {
		t.Errorf("redirect mismatch at exchange: %d", bad.Code)
	}
}

func TestSSOPKCE(t *testing.T) {
	s, _ := ssoServer(t)
	rec := login(t, s, "a@x.test", "password-A-1")
	verifier := "verifier-verifier-verifier-verifier-verifier-1234"
	sum := sha256.Sum256([]byte(verifier))
	ch := base64.RawURLEncoding.EncodeToString(sum[:])
	w := authedRequest(s, rec, http.MethodGet, authorizeURL(url.Values{"code_challenge": {ch}, "code_challenge_method": {"S256"}}), nil)
	back, _ := url.Parse(w.Header().Get("Location"))
	code := back.Query().Get("code")
	if w = exchange(s, url.Values{"code": {code}, "redirect_uri": {ssoCB}, "code_verifier": {"nope"}}, "sekret"); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier: %d", w.Code)
	}
}

func TestSSOLogout(t *testing.T) {
	s, _ := ssoServer(t)
	rec := login(t, s, "a@x.test", "password-A-1")
	w := authedRequest(s, rec, http.MethodGet, "/account/sso/logout?redirect_uri="+url.QueryEscape("https://splat.openpak.org/"), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="csrf"`) {
		t.Fatalf("confirm page: %d", w.Code)
	}
	if w = authedRequest(s, rec, http.MethodGet, "/account/sso/logout?redirect_uri="+url.QueryEscape("https://evil.test/"), nil); w.Code != http.StatusBadRequest {
		t.Errorf("open redirect on logout: %d", w.Code)
	}
	w = authedRequest(s, rec, http.MethodPost, "/account/sso/logout", &url.Values{"redirect_uri": {"https://splat.openpak.org/"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://splat.openpak.org/" {
		t.Fatalf("logout: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w = authedRequest(s, rec, http.MethodGet, "/account", nil); w.Code != http.StatusSeeOther {
		t.Errorf("still signed in after the SSO logout: %d", w.Code)
	}
}

func TestSSOOff(t *testing.T) {
	s, _, _ := newTestServer(t)
	if w := getPath(s, authorizeURL(nil)); w.Code != http.StatusNotFound {
		t.Errorf("sso off: %d", w.Code)
	}
}
