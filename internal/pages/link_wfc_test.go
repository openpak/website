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

	"github.com/openpak/website/internal/wfc"
)

// fakeWFCPairing is wfc-api's pairing endpoints: one live code for one console.
type fakeWFCPairing struct {
	mu        sync.Mutex
	live      map[string]uint64 // code -> console user id
	redeemed  []string
	forgotten []uint64
}

func newFakeWFCPairing(t *testing.T) (*fakeWFCPairing, *wfc.Client) {
	f := &fakeWFCPairing{live: map[string]uint64{"QXZP7K2M": 8899001122}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var req struct {
			Secret string `json:"secret"`
			Code   string `json:"code"`
			UserID uint64 `json:"user_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPost || req.Secret != "s3" {
			http.Error(w, `{"error":"failed_authentication"}`, http.StatusUnauthorized)
			return
		}
		uid, ok := f.live[req.Code]
		switch r.URL.Path {
		case "/api/pair/lookup":
			if !ok {
				http.Error(w, `{"error":"invalid_pairing_code"}`, http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"user_id":8899001122,"gsbrcd":"RMCP","name":"Alice","expires_at":"2026-11-04T12:30:00Z"}`))
		case "/api/pair/redeem":
			if !ok {
				http.Error(w, `{"error":"invalid_pairing_code"}`, http.StatusNotFound)
				return
			}
			delete(f.live, req.Code)
			f.redeemed = append(f.redeemed, req.Code)
			_, _ = w.Write([]byte(`{"user_id":` + "8899001122" + `}`))
			_ = uid
		case "/api/pair/forget":
			f.forgotten = append(f.forgotten, req.UserID)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, wfc.New(srv.URL, "s3")
}

// TestLinkWiiFromPairingCode: the code a Wii showed, entered at /link, becomes the "wfc" core
// link, is spent, and cannot be used by a second account; unlinking forgets the console.
func TestLinkWiiFromPairingCode(t *testing.T) {
	s, fc, _ := newTestServer(t)
	f, cli := newFakeWFCPairing(t)
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

	rec = authedRequest(s, a, http.MethodPost, "/link", &url.Values{"code": {"qxzp-7k2m"}})
	body := html.UnescapeString(rec.Body.String())
	if rec.Code != http.StatusOK || !strings.Contains(body, "Link your Wii or DS (Alice) to a@x.test?") || !strings.Contains(body, `name="kind" value="wfc"`) {
		t.Fatalf("confirm: %d %s", rec.Code, body)
	}
	if len(f.redeemed) != 0 || fc.wfcLinks["acct-a@x.test"] != "" {
		t.Fatal("looking a code up must not link or spend it")
	}
	rec = authedRequest(s, a, http.MethodPost, "/link/approve", &url.Values{"code": {"QXZP7K2M"}, "kind": {"wfc"}})
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Your Wii or DS is linked to a@x.test") {
		t.Fatalf("approve: %d %s", rec.Code, body)
	}
	if fc.wfcLinks["acct-a@x.test"] != "8899001122" || len(f.redeemed) != 1 {
		t.Fatalf("link %q, redeemed %v", fc.wfcLinks["acct-a@x.test"], f.redeemed)
	}
	// Single use.
	rec = authedRequest(s, a, http.MethodPost, "/link/approve", &url.Values{"code": {"QXZP7K2M"}, "kind": {"wfc"}})
	if rec.Code == http.StatusOK {
		t.Fatalf("a spent code approved again: %d", rec.Code)
	}

	// Another account entering a live code for an already-linked console is refused and the code survives.
	f.live["ABCD2345"] = 8899001122
	b := login(t, s, "b@x.test", "password-B-1")
	rec = authedRequest(s, b, http.MethodPost, "/link/approve", &url.Values{"code": {"ABCD2345"}, "kind": {"wfc"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already linked to another account") {
		t.Fatalf("second account: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := f.live["ABCD2345"]; !ok {
		t.Fatal("a refused link must not spend the code")
	}

	// Unlinking tells the Wii/DS server.
	acct := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	i := strings.Index(acct, `name="link_id" value="`)
	if i < 0 {
		t.Fatalf("no revoke form on the account page")
	}
	linkID := acct[i+len(`name="link_id" value="`):]
	linkID = linkID[:strings.Index(linkID, `"`)]
	authedRequest(s, a, http.MethodPost, "/account/link/revoke", &url.Values{"link_id": {linkID}})
	if len(f.forgotten) != 1 || f.forgotten[0] != 8899001122 {
		t.Fatalf("forgotten = %v", f.forgotten)
	}
}
