package pages

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/wfc/wfctest"
)

// TestLinkWiiFromPairingCode: the code (or the six-digit number) an unlinked Wii showed, entered
// at /link, is approved through wfc-api with the signed-in account; a second account is refused;
// the account page lists the console and unlinks it (with erase) through wfc-api.
func TestLinkWiiFromPairingCode(t *testing.T) {
	s, fc, _ := newTestServer(t)
	f, cli := wfctest.New(t)
	f.Codes["QXZP7K2M"] = wfctest.Code{Number: 24829, UserID: 8899001122, Unit: "wii", Game: "RMCP", Name: "Alice",
		Proof: func(ip, name string) bool { return name == "Alice" }}
	s.WFC = cli
	_, s.XBL = newFakeXBL(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	fc.accounts["b@x.test"], fc.verified["b@x.test"] = "password-B-1", true
	a := login(t, s, "a@x.test", "password-A-1")

	// A game device code still reaches xbl while the Wii/DS server is configured.
	rec := authedRequest(s, a, http.MethodPost, "/link", &url.Values{"code": {"GJ45S8Z2"}})
	if !strings.Contains(html.UnescapeString(rec.Body.String()), "Minecraft Dungeons") {
		t.Fatalf("xbl code broke: %d", rec.Code)
	}

	// A number without the second factor reads as unknown.
	rec = authedRequest(s, a, http.MethodPost, "/link", &url.Values{"code": {"24829"}})
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `name="kind" value="wfc"`) {
		t.Fatal("a bare number resolved")
	}
	for _, in := range []string{"qxzp-7k2m", "24 829"} {
		rec = authedRequest(s, a, http.MethodPost, "/link", &url.Values{"code": {in}, "console_name": {"Alice"}})
		body := html.UnescapeString(rec.Body.String())
		if rec.Code != http.StatusOK || !strings.Contains(body, "Link your Wii (RMCP, “Alice”) to a@x.test?") || !strings.Contains(body, `name="kind" value="wfc"`) {
			t.Fatalf("confirm %q: %d %s", in, rec.Code, body)
		}
	}
	if len(f.Linked) != 0 {
		t.Fatal("looking a code up must not link")
	}
	rec = authedRequest(s, a, http.MethodPost, "/link/approve", &url.Values{"code": {"24829"}, "kind": {"wfc"}, "console_name": {"Alice"}})
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Your Wii is linked to a@x.test") || !strings.Contains(body, "Connect again") {
		t.Fatalf("approve: %d %s", rec.Code, body)
	}
	if l := f.Linked[8899001122]; l.Account != "acct-a@x.test" {
		t.Fatalf("approve went out as %q, want the session's account", l.Account)
	}
	// Single use.
	rec = authedRequest(s, a, http.MethodPost, "/link/approve", &url.Values{"code": {"QXZP7K2M"}, "kind": {"wfc"}})
	if rec.Code == http.StatusOK {
		t.Fatalf("a spent code approved again: %d", rec.Code)
	}

	// Another account entering a live code for an already-linked console is refused.
	f.Codes["ABCD2345"] = wfctest.Code{Number: 25111, UserID: 8899001122, Unit: "wii"}
	b := login(t, s, "b@x.test", "password-B-1")
	rec = authedRequest(s, b, http.MethodPost, "/link/approve", &url.Values{"code": {"ABCD2345"}, "kind": {"wfc"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already linked to another account") {
		t.Fatalf("second account: %d %s", rec.Code, rec.Body.String())
	}

	// The account page lists the console (no console id) and unlinks it through wfc-api.
	acct := html.UnescapeString(authedRequest(s, a, http.MethodGet, "/account", nil).Body.String())
	if !strings.Contains(acct, "Wii") || strings.Contains(acct, "8899001122") || strings.Contains(acct, `action="/account/link/wfc"`) {
		t.Fatalf("account page: wii=%v id=%v form=%v", strings.Contains(acct, "Wii"), strings.Contains(acct, "8899001122"), strings.Contains(acct, `action="/account/link/wfc"`))
	}
	i := strings.Index(acct, `name="link_id" value="wfclink-`)
	if i < 0 {
		t.Fatalf("no revoke form for the Wii")
	}
	linkID := acct[i+len(`name="link_id" value="`):]
	linkID = linkID[:strings.Index(linkID, `"`)]
	// B cannot unlink A's console.
	authedRequest(s, b, http.MethodPost, "/account/link/revoke", &url.Values{"link_id": {linkID}, "kind": {"wfc"}})
	if len(f.Unlinked) != 0 {
		t.Fatal("B unlinked A's console")
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/link/revoke", &url.Values{"link_id": {linkID}, "kind": {"wfc"}, "erase": {"1"}})
	if rec.Code != http.StatusSeeOther || len(f.Unlinked) != 1 || len(f.Erased) != 1 {
		t.Fatalf("unlink: %d unlinked %v erased %v", rec.Code, f.Unlinked, f.Erased)
	}
}
