package pages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Deleting the account asks for the tick box and the password; with both, the account goes,
// the browser is signed out and the website forgets the session.
func TestDeleteAccount(t *testing.T) {
	s, fc, db := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")

	rec := authedRequest(s, a, http.MethodPost, "/account/delete", &url.Values{"password": {"password-A-1"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "delete-unconfirmed") {
		t.Fatalf("unconfirmed: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/delete", &url.Values{"password": {"wrong"}, "confirm": {"yes"}})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not deleted") {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	if len(fc.deleted) != 0 {
		t.Fatal("deletion started without the password")
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/delete", &url.Values{"password": {"password-A-1"}, "confirm": {"yes"}})
	if rec.Code != http.StatusOK || len(fc.deleted) != 1 || fc.deleted[0] != "acct-"+emailA {
		t.Fatalf("delete: %d, deleted %v", rec.Code, fc.deleted)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == "opak_session" && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("the session cookie was not cleared")
	}
	if _, err := db.GetWebSession(t.Context(), "sess-"+emailA); err == nil {
		t.Fatal("the web session row outlived the account")
	}
}
