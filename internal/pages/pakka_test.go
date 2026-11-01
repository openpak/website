package pages

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pakkachat"
)

type seenReq struct {
	method, path, account, key, grant string
}

// fakePakka records what the website forwards and answers like Pakka would.
func fakePakka(t *testing.T, seen *[]seenReq) *pakkachat.Bridge {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, seenReq{r.Method, r.URL.Path, r.Header.Get("X-Account-Id"),
			strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r.Header.Get("X-Pakka-Grant")})
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: delta\ndata: {\"text\":\"hi\"}\n\nevent: done\ndata: {}\n\n")
	}))
	t.Cleanup(srv.Close)
	return pakkachat.New(srv.URL, "svc-key", nil)
}

func setup(t *testing.T) (*Server, *httptest.ResponseRecorder, *httptest.ResponseRecorder, *[]seenReq) {
	t.Helper()
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	fc.birthdays = map[string]string{"acct-a@x.test": "1990-04-02", "acct-b@x.test": time.Now().AddDate(-12, 0, 0).Format("2006-01-02")}
	var seen []seenReq
	s.Pakka = fakePakka(t, &seen)
	s.Pakka.Tools = s.PakkaTools()
	return s, login(t, s, "a@x.test", "password-A-1"), login(t, s, "b@x.test", "password-B-1"), &seen
}

func cookieOf(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "opak_session" {
			return c.Value
		}
	}
	return ""
}

func apiReq(s *Server, rec *httptest.ResponseRecorder, method, target, csrf, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "opak_session", Value: cookieOf(rec)})
	if csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestAdultBirthday(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]bool{
		"2008-11-01": true, "2008-11-02": false, "1990-01-01": true, "2020-01-01": false,
		"": false, "garbage": false, "01/02/1990": false, "2030-01-01": false,
	} {
		if got := AdultBirthday(in, now); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

func TestPakkaPageAdultChildAndEntryPoints(t *testing.T) {
	s, adult, child, _ := setup(t)
	w := apiReq(s, adult, "GET", "/account/pakka", "", "", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `id="pakka"`) || !strings.Contains(body, "Pakka is an AI assistant and can make mistakes.") || !strings.Contains(body, "may be used by Meta to train and improve its models") {
		t.Fatalf("adult page: %d", w.Code)
	}
	for _, p := range []string{"/account", "/help"} {
		if b := apiReq(s, adult, "GET", p, "", "", nil).Body.String(); !strings.Contains(b, "/account/pakka") {
			t.Errorf("adult %s has no entry point", p)
		}
	}
	// A child account: the friendly page, no panel, no entry points, no API.
	w = apiReq(s, child, "GET", "/account/pakka", "", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), `id="pakka"`) || !strings.Contains(w.Body.String(), "Pakka is not available on this account") {
		t.Errorf("child page: %d", w.Code)
	}
	for _, p := range []string{"/account", "/help"} {
		if b := apiReq(s, child, "GET", p, "", "", nil).Body.String(); strings.Contains(b, "/account/pakka") || strings.Contains(b, "pakka.svg") {
			t.Errorf("child %s shows Pakka", p)
		}
	}
	// Signed out: sent to the sign-in page.
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/account/pakka", nil))
	if w.Code != http.StatusSeeOther {
		t.Errorf("signed out: %d", w.Code)
	}
}

func TestPakkaAPIForwardsOnlyTheSessionAccount(t *testing.T) {
	s, adult, _, seen := setup(t)
	csrf := s.Sessions.CSRFToken(cookieOf(adult))
	// No CSRF header: refused before anything leaves.
	if w := apiReq(s, adult, "POST", "/account/pakka/api/conversations", "", "", nil); w.Code != 403 {
		t.Errorf("no csrf: %d", w.Code)
	}
	if w := apiReq(s, adult, "POST", "/account/pakka/api/conversations", "wrong", "", nil); w.Code != 403 {
		t.Errorf("wrong csrf: %d", w.Code)
	}
	if len(*seen) != 0 {
		t.Fatalf("forwarded without csrf: %v", *seen)
	}
	// A client-supplied account id, in a header or the body, changes nothing.
	w := apiReq(s, adult, "POST", "/account/pakka/api/conversations/0123456789abcdef01234567/messages", csrf,
		`{"text":"hi","account_id":"acct-b@x.test"}`, map[string]string{"X-Account-Id": "acct-b@x.test", "X-Pakka-Grant": "pg_forged", "Authorization": "Bearer nope"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: delta") {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	got := (*seen)[0]
	if got.account != "acct-a@x.test" || got.key != "svc-key" || got.grant == "pg_forged" || got.grant == "" {
		t.Errorf("forwarded %+v", got)
	}
	// The conversation id is checked before it becomes part of an upstream path.
	for _, id := range []string{"..%2Fx", "abc", "0123456789abcdef0123456G"} {
		if w := apiReq(s, adult, "DELETE", "/account/pakka/api/conversations/"+id, csrf, "", nil); w.Code != 404 {
			t.Errorf("id %q: %d", id, w.Code)
		}
	}
}

func TestPakkaToolsAreScopedByGrant(t *testing.T) {
	s, _, _, _ := setup(t)
	b := s.Pakka
	call := func(key, grant, name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/internal/pakka/tool/"+name, nil)
		req.SetPathValue("name", name)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("X-Pakka-Grant", grant)
		w := httptest.NewRecorder()
		b.ToolHandler(w, req)
		return w
	}
	ga := b.MintGrant(pakkachat.Principal{AccountID: "acct-a@x.test"})
	gb := b.MintGrant(pakkachat.Principal{AccountID: "acct-b@x.test"})
	if w := call("", ga, "friends"); w.Code != 401 {
		t.Errorf("no key: %d", w.Code)
	}
	if w := call("wrong", ga, "friends"); w.Code != 401 {
		t.Errorf("wrong key: %d", w.Code)
	}
	if w := call("svc-key", "pg_unknown", "friends"); w.Code != 401 {
		t.Errorf("unknown grant: %d", w.Code)
	}
	w := call("svc-key", ga, "consoles")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Switch") {
		t.Errorf("a consoles: %d %s", w.Code, w.Body.String())
	}
	// B's grant never yields A's console, and B is a child so every account read is refused.
	if w := call("svc-key", gb, "consoles"); w.Code != 403 {
		t.Errorf("child consoles: %d %s", w.Code, w.Body.String())
	}
	if w := call("svc-key", gb, "eligibility"); !strings.Contains(w.Body.String(), `"eligible":false`) {
		t.Errorf("child eligibility: %s", w.Body.String())
	}
	if w := call("svc-key", ga, "eligibility"); !strings.Contains(w.Body.String(), `"eligible":true`) {
		t.Errorf("adult eligibility: %s", w.Body.String())
	}
	if w := call("svc-key", ga, "password"); w.Code != 404 {
		t.Errorf("unknown tool: %d", w.Code)
	}
}

func TestPakkaAbsentWithoutConfig(t *testing.T) {
	s, adult, _, _ := setup(t)
	s.Pakka = nil
	if b := apiReq(s, adult, "GET", "/account", "", "", nil).Body.String(); strings.Contains(b, "/account/pakka") {
		t.Error("entry point without Pakka")
	}
	if w := apiReq(s, adult, "GET", "/account/pakka/api/state", "", "", nil); w.Code != 503 {
		t.Errorf("api: %d", w.Code)
	}
}

func TestPakkaPrivacySectionInEveryLanguage(t *testing.T) {
	s, _, _, _ := setup(t)
	for _, lang := range i18n.Supported() {
		req := httptest.NewRequest("GET", "/privacy", nil)
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: lang})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		b := w.Body.String()
		if w.Code != 200 || !strings.Contains(b, `id="pakka"`) || !strings.Contains(b, "muse-spark-1.3-contributor") || !strings.Contains(b, "dev.meta.ai/legal/terms-of-service") || strings.Contains(b, "pakka.priv") {
			t.Errorf("%s privacy: %d", lang, w.Code)
		}
	}
}
