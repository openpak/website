package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/wfc"
)

// TestLinkWiiFromFriendCode: a friend code typed on the site becomes a "wfc" link on the
// account, the page shows it, and a second account cannot claim the same console.
func TestLinkWiiFromFriendCode(t *testing.T) {
	s, fc, _ := newTestServer(t)
	nnwfc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("secret") != "s3" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		if strings.ReplaceAll(r.URL.Query().Get("fc"), "-", "") != "123456789012" {
			http.Error(w, `{"error":"invalid_profile_id"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pid":3039,"user_id":8899001122,"gsbrcd":"RMCP","fc":"1234-5678-9012","name":"Alice"}`))
	}))
	defer nnwfc.Close()
	s.WFC = wfc.New(nnwfc.URL, "s3")

	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.accounts["b@x.test"], fc.verified["b@x.test"] = "password-B-1", true
	a := login(t, s, "a@x.test", "password-A-1")

	rec := authedRequest(s, a, http.MethodPost, "/account/link/wfc", &url.Values{"friend_code": {"9999-9999-9999"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "wfc-notfound") {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/link/wfc", &url.Values{"friend_code": {"1234-5678-9012"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "wfc-linked") {
		t.Fatalf("link: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if fc.wfcLinks["acct-a@x.test"] != "8899001122" {
		t.Fatalf("core link = %q, want the console user id", fc.wfcLinks["acct-a@x.test"])
	}
	rec = authedRequest(s, a, http.MethodGet, "/account", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Wii / DS") || !strings.Contains(rec.Body.String(), "8899001122") {
		t.Fatalf("account page does not show the Wii link: %d", rec.Code)
	}

	b := login(t, s, "b@x.test", "password-B-1")
	rec = authedRequest(s, b, http.MethodPost, "/account/link/wfc", &url.Values{"friend_code": {"1234-5678-9012"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "wfc-taken") {
		t.Fatalf("second account: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

// TestUnlinkOnlyOwnLink: the revoke form names a link id, and a signed-in account that
// posts somebody else's must not revoke it.
func TestUnlinkOnlyOwnLink(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["b@x.test"], fc.verified["b@x.test"] = "password-B-1", true
	fc.links["acct-a@x.test"] = "0123456789ab"
	b := login(t, s, "b@x.test", "password-B-1")

	rec := authedRequest(s, b, http.MethodPost, "/account/link/revoke", &url.Values{"link_id": {"link-acct-a@x.test"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoking another account's link: %d", rec.Code)
	}
	if _, ok := fc.links["acct-a@x.test"]; !ok {
		t.Fatal("A's link was revoked by B")
	}
}
