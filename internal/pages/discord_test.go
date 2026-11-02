package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/discordlink"
)

func fakeDiscord(t *testing.T, id string) *discordlink.Client {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("code") != "good" {
			http.Error(w, "{}", 400)
			return
		}
		w.Write([]byte(`{"access_token":"tok"}`))
	})
	mux.HandleFunc("GET /users/@me", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"` + id + `","username":"somebody"}`))
	})
	mux.HandleFunc("POST /oauth2/token/revoke", func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := discordlink.New("cid", "sec")
	c.APIBase = srv.URL
	return c
}

// callback replays what Discord's redirect does in the browser: the state from the start
// redirect, plus the state cookie the start set.
func callback(s *Server, login, start *httptest.ResponseRecorder, code string) *httptest.ResponseRecorder {
	loc, _ := url.Parse(start.Header().Get("Location"))
	state := loc.Query().Get("state")
	req := httptest.NewRequest(http.MethodGet, "/account/link/discord/callback?code="+code+"&state="+url.QueryEscape(state), nil)
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}
	for _, c := range start.Result().Cookies() {
		if c.Name == discordCookie {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDiscordLinkFlow(t *testing.T) {
	s, fc, db := newTestServer(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.accounts["b@x.test"], fc.verified["b@x.test"] = "password-B-1", true
	a := login(t, s, "a@x.test", "password-A-1")

	// Not configured: no button, start is refused.
	rec := authedRequest(s, a, http.MethodGet, "/account", nil)
	if strings.Contains(rec.Body.String(), "/account/link/discord") {
		t.Fatal("button shown without Discord configured")
	}
	if rec := authedRequest(s, a, http.MethodPost, "/account/link/discord", &url.Values{}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured start: %d", rec.Code)
	}

	s.Discord = fakeDiscord(t, "111111111111111111")
	rec = authedRequest(s, a, http.MethodGet, "/account", nil)
	if !strings.Contains(rec.Body.String(), `action="/account/link/discord"`) {
		t.Fatal("link button missing")
	}
	// CSRF: a bare POST is refused.
	req := httptest.NewRequest(http.MethodPost, "/account/link/discord", nil)
	for _, c := range a.Result().Cookies() {
		req.AddCookie(c)
	}
	bad := httptest.NewRecorder()
	s.Handler().ServeHTTP(bad, req)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("no csrf: %d", bad.Code)
	}

	start := authedRequest(s, a, http.MethodPost, "/account/link/discord", &url.Values{})
	loc, _ := url.Parse(start.Header().Get("Location"))
	if start.Code != http.StatusSeeOther || loc.Host != "discord.com" || loc.Query().Get("scope") != "identify" ||
		loc.Query().Get("redirect_uri") != "http://example.com/account/link/discord/callback" {
		t.Fatalf("start: %d %s", start.Code, loc)
	}

	// A state replayed without the cookie, or by another account, links nothing.
	noCookie := httptest.NewRequest(http.MethodGet, "/account/link/discord/callback?code=good&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	for _, c := range a.Result().Cookies() {
		noCookie.AddCookie(c)
	}
	nr := httptest.NewRecorder()
	s.Handler().ServeHTTP(nr, noCookie)
	if !strings.Contains(nr.Header().Get("Location"), "dm=failed") {
		t.Fatalf("no cookie: %s", nr.Header().Get("Location"))
	}
	b := login(t, s, "b@x.test", "password-B-1")
	if rec := callback(s, b, start, "good"); !strings.Contains(rec.Header().Get("Location"), "dm=failed") {
		t.Fatalf("other account: %s", rec.Header().Get("Location"))
	}
	if _, err := db.DiscordLinkByDiscordID(t.Context(), "111111111111111111"); err == nil {
		t.Fatal("forged callback linked")
	}

	// Cancelled at Discord.
	cancel := httptest.NewRequest(http.MethodGet, "/account/link/discord/callback?error=access_denied&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	for _, c := range append(a.Result().Cookies(), start.Result().Cookies()...) {
		cancel.AddCookie(c)
	}
	cr := httptest.NewRecorder()
	s.Handler().ServeHTTP(cr, cancel)
	if !strings.Contains(cr.Header().Get("Location"), "dm=cancelled") {
		t.Fatalf("cancel: %s", cr.Header().Get("Location"))
	}

	// The real thing.
	if rec := callback(s, a, start, "bad"); !strings.Contains(rec.Header().Get("Location"), "dm=failed") {
		t.Fatalf("bad code: %s", rec.Header().Get("Location"))
	}
	rec = callback(s, a, start, "good")
	if !strings.Contains(rec.Header().Get("Location"), "dm=linked") {
		t.Fatalf("link: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	l, err := db.DiscordLinkByAccount(t.Context(), "acct-a@x.test")
	if err != nil || l.DiscordID != "111111111111111111" || l.Username != "somebody" {
		t.Fatalf("stored: %+v %v", l, err)
	}
	rec = authedRequest(s, a, http.MethodGet, "/account?dm=linked", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "somebody") || !strings.Contains(body, "Discord linked.") || !strings.Contains(body, "/account/link/discord/revoke") {
		t.Fatalf("page after link: %s", body)
	}

	// One Discord account, one OpenPak account.
	st2 := authedRequest(s, b, http.MethodPost, "/account/link/discord", &url.Values{})
	if rec := callback(s, b, st2, "good"); !strings.Contains(rec.Header().Get("Location"), "dm=taken") {
		t.Fatalf("second account: %s", rec.Header().Get("Location"))
	}

	// Unlink, audited.
	rec = authedRequest(s, a, http.MethodPost, "/account/link/discord/revoke", &url.Values{})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "dm=unlinked") {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := db.DiscordLinkByAccount(t.Context(), "acct-a@x.test"); err == nil {
		t.Fatal("still linked")
	}
	log, _ := db.ListAudit(t.Context(), 20)
	var seen []string
	for _, e := range log {
		seen = append(seen, e.Action)
	}
	if got := strings.Join(seen, ","); !strings.Contains(got, "discord.link") || !strings.Contains(got, "discord.unlink") || !strings.Contains(got, "discord.link.refused") {
		t.Fatalf("audit: %s", got)
	}
}
