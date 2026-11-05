package pages

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/openpak/website/internal/wfc/wfctest"
)

// TestFriendCodeLinkIsGone: link-first retired linking a Wii or DS by friend code (anyone who
// knows a friend code could have claimed someone else's console).
func TestFriendCodeLinkIsGone(t *testing.T) {
	s, fc, _ := newTestServer(t)
	_, s.WFC = wfctest.New(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	a := login(t, s, "a@x.test", "password-A-1")
	rec := authedRequest(s, a, http.MethodPost, "/account/link/wfc", &url.Values{"friend_code": {"1234-5678-9012"}})
	if rec.Code == http.StatusSeeOther || rec.Code == http.StatusOK {
		t.Fatalf("friend-code link still answers: %d", rec.Code)
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
