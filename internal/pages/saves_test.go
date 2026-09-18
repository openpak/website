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
