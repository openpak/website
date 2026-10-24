package pages

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/xbl"
)

// fakeXBL is xbl's private approval API: codes are pending, expired or used.
type fakeXBL struct {
	mu       sync.Mutex
	codes    map[string]string // code -> "", "expired", "used"
	approved map[string]string // code -> account id
}

func newFakeXBL(t *testing.T) (*fakeXBL, *xbl.Client) {
	f := &fakeXBL{codes: map[string]string{"GJ45S8Z2": "", "OLDC0DE2": "expired"}, approved: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Internal-Key") != "k3y" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		notFound := func(reason string) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "code_not_found_or_expired", "reason": reason})
		}
		if r.Method == http.MethodPost && r.URL.Path == "/internal/v1/link/approve" {
			var req struct{ UserCode, AccountID string }
			json.NewDecoder(r.Body).Decode(&struct {
				UserCode  *string `json:"user_code"`
				AccountID *string `json:"account_id"`
			}{&req.UserCode, &req.AccountID})
			st, ok := f.codes[req.UserCode]
			switch {
			case !ok:
				notFound("unknown")
			case st != "":
				notFound(st)
			default:
				f.codes[req.UserCode] = "used"
				f.approved[req.UserCode] = req.AccountID
				json.NewEncoder(w).Encode(map[string]any{"approved": true, "gamertag": "Gamer A"})
			}
			return
		}
		code := strings.TrimPrefix(r.URL.Path, "/internal/v1/link/")
		st, ok := f.codes[code]
		switch {
		case !ok:
			notFound("unknown")
		case st != "":
			notFound(st)
		default:
			json.NewEncoder(w).Encode(map[string]any{"user_code": code, "client_id": "000000004426B87D",
				"expires_at": time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)})
		}
	}))
	t.Cleanup(srv.Close)
	return f, xbl.New(srv.URL, "k3y")
}

// loginNext signs in through the form with a return address.
func loginNext(s *Server, email, password, next string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/login", url.Values{"email": {email}, "password": {password}, "next": {next}}))
	return rec
}

func TestLinkSignedOutReturnsAfterSignIn(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	rec := get(t, s, "/link?otc=gj45-s8z2")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `href="/login?next=%2Flink%3Fcode%3DGJ45S8Z2"`) || !strings.Contains(body, "GJ45S8Z2") {
		t.Fatalf("signed-out /link: %d %s", rec.Code, body)
	}
	if !strings.Contains(body, "Link a console or game") || !strings.Contains(body, `href="/press"`) {
		t.Error("/link must be drawn in the site layout")
	}
	// The login form carries the return address, and only a /link one.
	if b := get(t, s, "/login?next=%2Flink%3Fcode%3DGJ45S8Z2").Body.String(); !strings.Contains(b, `name="next" value="/link?code=GJ45S8Z2"`) {
		t.Fatalf("login form lost next: %s", b)
	}
	if b := get(t, s, "/login?next=https%3A%2F%2Fevil.test%2Flink").Body.String(); strings.Contains(b, `name="next"`) {
		t.Fatal("an off-site next must be dropped")
	}
	if loc := loginNext(s, emailA, "password-A-1", "/link?code=gj45s8z2").Header().Get("Location"); loc != "/link?code=GJ45S8Z2" {
		t.Fatalf("after sign-in: %q", loc)
	}
	for _, bad := range []string{"//evil.test/link", "https://evil.test/link", "/account/delete", "/link?code=<x>"} {
		want := "/account"
		if strings.HasPrefix(bad, "/link") {
			want = "/link"
		}
		if loc := loginNext(s, emailA, "password-A-1", bad).Header().Get("Location"); loc != want {
			t.Errorf("next %q: went to %q, want %q", bad, loc, want)
		}
	}
}

func TestLinkTwoFactorKeepsNext(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	fc.twoFactor[emailA] = true
	rec := loginNext(s, emailA, "password-A-1", "/link?code=GJ45S8Z2")
	if !strings.Contains(rec.Body.String(), `name="next" value="/link?code=GJ45S8Z2"`) {
		t.Fatalf("2FA step lost next: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/login/2fa", url.Values{"challenge": {"ch-" + emailA}, "code": {fakeTOTP}, "next": {"/link?code=GJ45S8Z2"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/link?code=GJ45S8Z2" {
		t.Fatalf("after 2FA: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLinkApproveJourney(t *testing.T) {
	s, fc, _ := newTestServer(t)
	f, cli := newFakeXBL(t)
	s.XBL = cli
	emailA, _ := seedTwoAccounts(fc)
	lr := login(t, s, emailA, "password-A-1")

	rec := authedRequest(s, lr, http.MethodGet, "/link?code=GJ45S8Z2", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `value="GJ45S8Z2"`) || !strings.Contains(body, `name="csrf"`) || !strings.Contains(body, `<label for="code">`) {
		t.Fatalf("code form: %d %s", rec.Code, body)
	}
	// No CSRF token, no lookup.
	norec := httptest.NewRecorder()
	req := post("/link", url.Values{"code": {"GJ45S8Z2"}})
	req.AddCookie(cookieFrom(t, lr))
	s.Handler().ServeHTTP(norec, req)
	if norec.Code != http.StatusForbidden {
		t.Fatalf("POST /link without csrf: %d", norec.Code)
	}

	rec = authedRequest(s, lr, http.MethodPost, "/link", &url.Values{"code": {"gj45-s8z2"}})
	body = html.UnescapeString(rec.Body.String())
	if rec.Code != http.StatusOK || !strings.Contains(body, "Link Minecraft Dungeons to "+emailA+"?") || !strings.Contains(body, `action="/link/approve"`) || !strings.Contains(body, "Cancel") {
		t.Fatalf("confirm: %d %s", rec.Code, body)
	}
	if len(f.approved) != 0 {
		t.Fatal("looking a code up must not approve it")
	}

	rec = authedRequest(s, lr, http.MethodPost, "/link/approve", &url.Values{"code": {"GJ45S8Z2"}, "client_id": {"000000004426B87D"}})
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Minecraft Dungeons is signing in as Gamer A") || !strings.Contains(body, `role="status"`) {
		t.Fatalf("approve: %d %s", rec.Code, body)
	}
	if f.approved["GJ45S8Z2"] != "acct-"+emailA {
		t.Fatalf("approved for %q", f.approved["GJ45S8Z2"])
	}

	for _, c := range []struct {
		path, code string
		status     int
		want       string
	}{
		{"/link/approve", "GJ45S8Z2", http.StatusConflict, "already been used"},
		{"/link", "GJ45S8Z2", http.StatusConflict, "already been used"},
		{"/link", "OLDC0DE2", http.StatusGone, "has expired"},
		{"/link", "ZZZZZZZZ", http.StatusNotFound, "not recognised"},
		{"/link", "  ", http.StatusBadRequest, "Enter the code"},
		{"/link", "<script>", http.StatusBadRequest, "Enter the code"},
	} {
		rec := authedRequest(s, lr, http.MethodPost, c.path, &url.Values{"code": {c.code}})
		if b := rec.Body.String(); rec.Code != c.status || !strings.Contains(b, c.want) || !strings.Contains(b, `role="alert"`) || strings.Contains(b, "<script>") {
			t.Errorf("%s %q: %d, want %d with %q", c.path, c.code, rec.Code, c.status, c.want)
		}
	}
	if de := authedRequestLang(s, lr, "/link", "de"); !strings.Contains(de, "Konsole oder Spiel verknüpfen") {
		t.Error("/link [de]: German page expected")
	}
}

func TestLinkUnavailableAndRateLimited(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	lr := login(t, s, emailA, "password-A-1")
	if rec := authedRequest(s, lr, http.MethodPost, "/link", &url.Values{"code": {"GJ45S8Z2"}}); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "unavailable") {
		t.Fatalf("no xbl configured: %d", rec.Code)
	}
	_, s.XBL = newFakeXBL(t)
	s.LinkLimit = ratelimit.New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if rec := authedRequest(s, lr, http.MethodPost, "/link", &url.Values{"code": {"ZZZZZZZZ"}}); rec.Code != http.StatusNotFound {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec := authedRequest(s, lr, http.MethodPost, "/link", &url.Values{"code": {"GJ45S8Z2"}}); rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "Too many codes") {
		t.Fatalf("over the limit: %d", rec.Code)
	}
}

func authedRequestLang(s *Server, lr *httptest.ResponseRecorder, target, lang string) string {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Accept-Language", lang)
	for _, c := range lr.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}
