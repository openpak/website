package pages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/xbaccount"
)

// fakeXB stands in for xb-account: it records every call and answers per gamertag.
type fakeXB struct {
	calls []map[string]any
	paths []string
}

func (f *fakeXB) serve(t *testing.T) *xbaccount.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.calls, f.paths = append(f.calls, body), append(f.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fail := func(code int, e string) { w.WriteHeader(code); _, _ = w.Write([]byte(`{"error":"` + e + `"}`)) }
		switch body["gamertag"] {
		case "Taken One":
			fail(409, "gamertag_taken")
		case "Xbox":
			fail(422, "reserved_gamertag")
		case "bad!!":
			fail(400, "invalid_gamertag")
		case "Bound":
			fail(409, "console_bound")
		case "Down":
			fail(503, "unavailable")
		case "Garbage":
			_, _ = w.Write([]byte("<html>"))
		default:
			_, _ = w.Write([]byte(`{"xuid":"0009abc","gamertag":"` + body["gamertag"].(string) + `","previous":"Old","changed":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return xbaccount.New(srv.URL, nil)
}

func TestXboxGamertag(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fx := &fakeXB{}
	s.XB = fx.serve(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.xboxTags["acct-a@x.test"] = "OldTag"
	a := login(t, s, "a@x.test", "password-A-1")

	// The page shows the current gamertag in a form with a request id and a CSRF token.
	rec := authedRequest(s, a, http.MethodGet, "/account", nil)
	body := rec.Body.String()
	for _, want := range []string{`id="xbox"`, `value="OldTag"`, `name="request_id" value="web-`, `maxlength="15"`} {
		if !strings.Contains(body, want) {
			t.Errorf("account page lacks %s", want)
		}
	}

	for tag, msg := range map[string]string{
		"New Tag": "xbox-saved", "Taken One": "xbox-taken", "Xbox": "xbox-reserved", "bad!!": "xbox-invalid",
		"Bound": "xbox-console-bound", "Down": "xbox-unavailable", "Garbage": "xbox-unavailable", "   ": "xbox-invalid",
	} {
		rec = authedRequest(s, a, http.MethodPost, "/account/xbox/gamertag",
			&url.Values{"gamertag": {tag}, "request_id": {"web-00000000000000000000000000000001"}, "core_account_id": {"someone-else"}})
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "m="+msg) {
			t.Errorf("%q: %d %s, want %s", tag, rec.Code, rec.Header().Get("Location"), msg)
		}
	}
	// Whitespace-only never leaves the site; everything else is one call each.
	if len(fx.calls) != 7 {
		t.Fatalf("calls = %d, want 7", len(fx.calls))
	}
	for i, c := range fx.calls {
		if fx.paths[i] != "/internal/v1/accounts/acct-a@x.test/gamertag" {
			t.Errorf("path %q: the account must come from the session, not the form", fx.paths[i])
		}
		if c["request_id"] != "web-00000000000000000000000000000001" || c["actor"] != "website:acct-a@x.test" {
			t.Errorf("request id / actor: %v", c)
		}
		if _, sent := c["force_console_identity"]; sent {
			t.Errorf("force_console_identity must never be sent: %v", c)
		}
	}

	// A request id that is not ours is replaced, never forwarded.
	fx.calls = nil
	authedRequest(s, a, http.MethodPost, "/account/xbox/gamertag", &url.Values{"gamertag": {"New Tag"}, "request_id": {strings.Repeat("x", 500)}})
	if id, _ := fx.calls[0]["request_id"].(string); !requestIDRe.MatchString(id) {
		t.Errorf("forwarded request id %q", id)
	}
}

func TestXboxGamertagGuards(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.xboxTags["acct-a@x.test"] = "OldTag"
	a := login(t, s, "a@x.test", "password-A-1")

	// Not configured: no section, and the route refuses.
	if rec := authedRequest(s, a, http.MethodGet, "/account", nil); strings.Contains(rec.Body.String(), `id="xbox"`) {
		t.Error("section shown without xb-account configured")
	}
	if rec := authedRequest(s, a, http.MethodPost, "/account/xbox/gamertag", &url.Values{"gamertag": {"X"}}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured post: %d", rec.Code)
	}

	fx := &fakeXB{}
	s.XB = fx.serve(t)
	// Signed out: not served, nothing sent.
	req := httptest.NewRequest(http.MethodPost, "/account/xbox/gamertag", strings.NewReader("gamertag=X"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || len(fx.calls) != 0 {
		t.Errorf("signed-out post: %d, calls %d", rec.Code, len(fx.calls))
	}
	// Wrong CSRF token: refused, nothing sent.
	req = httptest.NewRequest(http.MethodPost, "/account/xbox/gamertag", strings.NewReader("gamertag=X&csrf=bad"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range a.Result().Cookies() {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(fx.calls) != 0 {
		t.Errorf("bad csrf: %d, calls %d", rec.Code, len(fx.calls))
	}

	// No xbox link yet: no form, an explanation instead.
	delete(fc.xboxTags, "acct-a@x.test")
	if rec := authedRequest(s, a, http.MethodGet, "/account", nil); strings.Contains(rec.Body.String(), `name="gamertag"`) {
		t.Error("form shown for an account without an Xbox identity")
	}
}
