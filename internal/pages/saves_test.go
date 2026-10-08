package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/saves"
)

// The saves page against a stub of the service: the list renders with the allowance, a
// storage form submission reaches the service as JSON for the signed-in account, and the
// service's refusal comes back to the player in its own words.
func TestSavesPage(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	var gotPut string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "k" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/saves"):
			w.Write([]byte(`{"saves":[{"platform":"citron","title_id":"0100abcd","versions":[{"id":7,"number":2,"conflict":true,"size":2048,"backend":"local","parts":1,"created_at":"2026-09-10T10:00:00Z"}]}],"usage":{"allowance_used":2048,"allowance":268435456,"total":2048}}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/storage"):
			w.Write([]byte(`{"connected":false,"usage":{"allowance_used":2048,"allowance":268435456,"total":2048}}`))
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/storage"):
			gotPut = r.URL.Path
			w.WriteHeader(422)
			w.Write([]byte(`{"error":"storage_test_failed","detail":"write: access denied"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer stub.Close()
	s.Saves = saves.New(stub.URL, "k")

	rec := login(t, s, emailA, "password-A-1")
	page := authedRequest(s, rec, http.MethodGet, "/account/saves", nil)
	if page.Code != 200 {
		t.Fatalf("saves page: %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{"0100abcd", "Version 2", "Conflict", "2 kB", "256.0 MB", `value="7"`, `formaction="/account/saves/download"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("saves page lacks %q", want)
		}
	}

	form := url.Values{"endpoint": {"https://s3.example"}, "bucket": {"b"}, "access_key": {"a"}, "secret_key": {"s"}, "path_style": {"on"}}
	post := authedRequest(s, rec, http.MethodPost, "/account/saves/storage", &form)
	if post.Code != http.StatusSeeOther || !strings.Contains(post.Header().Get("Location"), "storage-failed") || !strings.Contains(post.Header().Get("Location"), "access+denied") {
		t.Fatalf("storage failure not relayed: %d %s", post.Code, post.Header().Get("Location"))
	}
	if !strings.HasSuffix(gotPut, "/internal/accounts/acct-a@x.test/storage") {
		t.Fatalf("storage settings went to %q, not the signed-in account", gotPut)
	}
}

// Managing backups: the page lists date, size and console without a device id, deleting is
// two steps (a confirmation page, then a POST that says confirm=yes), and a delete for the
// whole title goes to the service for the signed-in account only.
func TestSavesManageAndDelete(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	var calls []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/saves"):
			w.Write([]byte(`{"saves":[{"platform":"switch","title_id":"0100abcd","versions":[{"id":7,"number":2,"size":2048,"console":"Harbor","backend":"local","parts":2,"created_at":"2026-09-10T10:00:00Z"},{"id":6,"number":1,"size":1024,"console":"Switch","backend":"local","parts":2,"created_at":"2026-09-09T10:00:00Z"}]}],"usage":{"allowance_used":3072,"allowance":268435456,"total":3072}}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/storage"):
			w.Write([]byte(`{"connected":false,"usage":{}}`))
		case r.Method == "DELETE":
			calls = append(calls, r.Method+" "+r.URL.Path)
			if strings.Contains(r.URL.Path, "/saves/") {
				w.Write([]byte(`{"deleted":2}`))
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer stub.Close()
	s.Saves = saves.New(stub.URL, "k")
	rec := login(t, s, emailA, "password-A-1")

	page := authedRequest(s, rec, http.MethodGet, "/account/saves/manage?platform=switch&title=0100abcd", nil)
	body := page.Body.String()
	if page.Code != 200 {
		t.Fatalf("manage: %d", page.Code)
	}
	for _, want := range []string{"2026-09-10", "2 kB", "Harbor", "1 kB", "Switch", "/account/saves/delete?version=7", "Delete all backups"} {
		if !strings.Contains(body, want) {
			t.Fatalf("manage page lacks %q", want)
		}
	}

	// A delete POST without confirm=yes deletes nothing and sends the player to the question.
	form := url.Values{"version": {"7"}}
	post := authedRequest(s, rec, http.MethodPost, "/account/saves/delete", &form)
	if post.Code != http.StatusSeeOther || post.Header().Get("Location") != "/account/saves/delete?version=7" || len(calls) != 0 {
		t.Fatalf("unconfirmed delete: %d %s %v", post.Code, post.Header().Get("Location"), calls)
	}
	q := authedRequest(s, rec, http.MethodGet, "/account/saves/delete?version=7", nil)
	if q.Code != 200 || !strings.Contains(q.Body.String(), "Delete this backup?") || !strings.Contains(q.Body.String(), `name="confirm" value="yes"`) {
		t.Fatalf("confirmation page: %d", q.Code)
	}
	form = url.Values{"version": {"7"}, "confirm": {"yes"}}
	post = authedRequest(s, rec, http.MethodPost, "/account/saves/delete", &form)
	if post.Code != http.StatusSeeOther || len(calls) != 1 || !strings.HasSuffix(calls[0], "/internal/accounts/acct-a@x.test/versions/7") {
		t.Fatalf("confirmed delete: %d %v", post.Code, calls)
	}

	// Every backup of a title: same two steps.
	form = url.Values{"platform": {"switch"}, "title": {"0100abcd"}}
	post = authedRequest(s, rec, http.MethodPost, "/account/saves/delete-title", &form)
	if post.Code != http.StatusSeeOther || len(calls) != 1 || !strings.HasPrefix(post.Header().Get("Location"), "/account/saves/delete?platform=switch") {
		t.Fatalf("unconfirmed title delete: %d %s %v", post.Code, post.Header().Get("Location"), calls)
	}
	q = authedRequest(s, rec, http.MethodGet, "/account/saves/delete?platform=switch&title=0100abcd", nil)
	if q.Code != 200 || !strings.Contains(q.Body.String(), "Delete all backups of this game?") {
		t.Fatalf("title confirmation page: %d", q.Code)
	}
	form = url.Values{"platform": {"switch"}, "title": {"0100abcd"}, "confirm": {"yes"}}
	post = authedRequest(s, rec, http.MethodPost, "/account/saves/delete-title", &form)
	if post.Code != http.StatusSeeOther || len(calls) != 2 || !strings.HasSuffix(calls[1], "/internal/accounts/acct-a@x.test/saves/switch/0100abcd") {
		t.Fatalf("confirmed title delete: %d %v", post.Code, calls)
	}

	// No session, no deletion; a POST without the CSRF token is refused.
	anon := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/account/saves/delete-title", strings.NewReader("platform=switch&title=0100abcd&confirm=yes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(anon, req)
	if anon.Code == http.StatusSeeOther && !strings.Contains(anon.Header().Get("Location"), "login") || len(calls) != 2 {
		t.Fatalf("anonymous delete: %d %s %v", anon.Code, anon.Header().Get("Location"), calls)
	}
	noCSRF := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/account/saves/delete-title", strings.NewReader("platform=switch&title=0100abcd&confirm=yes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	s.Handler().ServeHTTP(noCSRF, req)
	if noCSRF.Code != http.StatusForbidden || len(calls) != 2 {
		t.Fatalf("delete without CSRF: %d %v", noCSRF.Code, calls)
	}
}
