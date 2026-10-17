package pages

import (
	"context"
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
		case "Same Tag":
			_, _ = w.Write([]byte(`{"xuid":"0009abc","gamertag":"Same Tag","previous":"Same Tag","changed":false}`))
		default:
			_, _ = w.Write([]byte(`{"xuid":"0009abc","gamertag":"` + body["gamertag"].(string) + `","previous":"Old","changed":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return xbaccount.New(srv.URL, nil)
}

// fake360 is a second gamertag backend, standing in for the future Xbox 360 one.
type fake360 struct {
	err   error
	calls []string // request ids
	tags  []string
}

func (f *fake360) SetGamertag(_ context.Context, _, tag, rid, _ string) (xbaccount.Result, error) {
	f.calls, f.tags = append(f.calls, rid), append(f.tags, tag)
	if f.err != nil {
		return xbaccount.Result{}, f.err
	}
	return xbaccount.Result{Gamertag: tag, Changed: true}, nil
}

const rid1 = "web-00000000000000000000000000000001"

func postTag(s *Server, a *httptest.ResponseRecorder, tag, rid string) *httptest.ResponseRecorder {
	return authedRequest(s, a, http.MethodPost, "/account/networks/xbox/gamertag",
		&url.Values{"gamertag": {tag}, "request_id": {rid}, "core_account_id": {"someone-else"}})
}

func TestXboxGamertag(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fx := &fakeXB{}
	s.XB = fx.serve(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.xboxTags["acct-a@x.test"] = "OldTag"
	a := login(t, s, "a@x.test", "password-A-1")

	// The Networks area is separate from the common profile section and lists the one field.
	body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	for _, want := range []string{`id="networks"`, `id="net-xbox"`, `value="OldTag"`, `name="request_id" value="web-`, `maxlength="15"`, "Applies to: Original Xbox"} {
		if !strings.Contains(body, want) {
			t.Errorf("account page lacks %s", want)
		}
	}
	if strings.Index(body, `id="profile"`) > strings.Index(body, `id="networks"`) || strings.Contains(body, "Xbox 360</") && strings.Contains(body, "Applies to: Original Xbox, Xbox 360") {
		t.Error("profile must come first and the 360 must be hidden while it has no backend")
	}

	for tag, msg := range map[string]string{
		"New Tag": "net-saved", "Taken One": "net-failed%3Axbox%3Ataken", "Xbox": "reserved", "bad!!": "invalid",
		"Bound": "consolebound", "Down": "unavailable", "Garbage": "unavailable", "   ": "invalid",
	} {
		rec := postTag(s, a, tag, rid1)
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), msg) {
			t.Errorf("%q: %d %s, want %s", tag, rec.Code, rec.Header().Get("Location"), msg)
		}
	}
	if len(fx.calls) != 7 {
		t.Fatalf("calls = %d, want 7 (whitespace-only never leaves the site)", len(fx.calls))
	}
	for i, c := range fx.calls {
		if fx.paths[i] != "/internal/v1/accounts/acct-a@x.test/gamertag" {
			t.Errorf("path %q: the account must come from the session, not the form", fx.paths[i])
		}
		if c["request_id"] != rid1 || c["actor"] != "website:acct-a@x.test" {
			t.Errorf("request id / actor: %v", c)
		}
		if _, sent := c["force_console_identity"]; sent {
			t.Errorf("force_console_identity must never be sent: %v", c)
		}
	}
	fx.calls = nil
	postTag(s, a, "New Tag", strings.Repeat("x", 500))
	if id, _ := fx.calls[0]["request_id"].(string); !requestIDRe.MatchString(id) {
		t.Errorf("forwarded request id %q", id)
	}
	// A name xb-account reports unchanged is "already your gamertag", not "changed".
	if rec := postTag(s, a, "Same Tag", rid1); !strings.Contains(rec.Header().Get("Location"), "net-same") {
		t.Errorf("unchanged: %s", rec.Header().Get("Location"))
	}
	// Unknown network or field: nothing is served.
	if rec := authedRequest(s, a, http.MethodPost, "/account/networks/psn/gamertag", &url.Values{"gamertag": {"X"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown network: %d", rec.Code)
	}
}

// One edit applies to both Xbox networks; a failure on the second is reported as partial, the
// retry reuses the request id, and nothing claims full success.
func TestXboxGamertagBothNetworks(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fx, f360 := &fakeXB{}, &fake360{}
	s.XB, s.X360 = fx.serve(t), f360
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.xboxTags["acct-a@x.test"] = "OldTag"
	a := login(t, s, "a@x.test", "password-A-1")

	if body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(); !strings.Contains(body, "Applies to: Original Xbox, Xbox 360") {
		t.Error("both networks should be listed once the 360 backend is configured")
	}
	rec := postTag(s, a, "New Tag", rid1)
	if !strings.Contains(rec.Header().Get("Location"), "net-saved") || len(fx.calls) != 1 || len(f360.calls) != 1 || f360.calls[0] != rid1 {
		t.Fatalf("both: %s xb=%d 360=%v", rec.Header().Get("Location"), len(fx.calls), f360.calls)
	}

	// 360 fails after the original Xbox applied: partial, request id carried for the retry.
	f360.err = xbaccount.ErrUnavailable
	rec = postTag(s, a, "New Tag", rid1)
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "net-saved") || !strings.Contains(loc, "net-failed") || !strings.Contains(loc, "rid="+rid1) {
		t.Fatalf("partial: %s", loc)
	}
	page := authedRequest(s, a, http.MethodGet, loc, nil).Body.String()
	for _, want := range []string{"Changed on Original Xbox", "Not changed on Xbox 360", `name="request_id" value="` + rid1 + `"`} {
		if !strings.Contains(page, want) {
			t.Errorf("partial page lacks %q", want)
		}
	}
	// Failure on the first backend: the 360 is not attempted.
	before := len(f360.calls)
	postTag(s, a, "Taken One", rid1)
	if len(f360.calls) != before {
		t.Error("the 360 was called after the original Xbox refused")
	}
	// Retry with the same id once the 360 recovers.
	f360.err = nil
	rec = postTag(s, a, "New Tag", rid1)
	if !strings.Contains(rec.Header().Get("Location"), "net-saved") || f360.calls[len(f360.calls)-1] != rid1 {
		t.Errorf("retry: %s", rec.Header().Get("Location"))
	}
}

func TestXboxGamertagGuards(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.xboxTags["acct-a@x.test"] = "OldTag"
	a := login(t, s, "a@x.test", "password-A-1")

	if rec := authedRequest(s, a, http.MethodGet, "/account", nil); strings.Contains(rec.Body.String(), `id="networks"`) {
		t.Error("Networks area shown with no backend configured")
	}
	if rec := postTag(s, a, "X", rid1); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured post: %d", rec.Code)
	}

	fx := &fakeXB{}
	s.XB = fx.serve(t)
	req := httptest.NewRequest(http.MethodPost, "/account/networks/xbox/gamertag", strings.NewReader("gamertag=X"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || len(fx.calls) != 0 {
		t.Errorf("signed-out post: %d, calls %d", rec.Code, len(fx.calls))
	}
	req = httptest.NewRequest(http.MethodPost, "/account/networks/xbox/gamertag", strings.NewReader("gamertag=X&csrf=bad"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range a.Result().Cookies() {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(fx.calls) != 0 {
		t.Errorf("bad csrf: %d, calls %d", rec.Code, len(fx.calls))
	}
	delete(fc.xboxTags, "acct-a@x.test")
	if rec := authedRequest(s, a, http.MethodGet, "/account", nil); strings.Contains(rec.Body.String(), `name="gamertag"`) {
		t.Error("form shown for an account without an Xbox identity")
	}
}
