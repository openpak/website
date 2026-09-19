package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/chat"
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

// The display name is edited on the profile form and the header shows the new one straight
// away; a form sent with the name left empty keeps the name rather than erasing it.
func TestDisplayNameCanBeChanged(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	fc.names["acct-"+emailA] = "Alice"
	a := login(t, s, emailA, "password-A-1")
	if body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(); !strings.Contains(body, `value="Alice"`) {
		t.Fatal("the form does not start with the current name")
	}

	rec := authedRequest(s, a, http.MethodPost, "/account/profile", &url.Values{"display_name": {"Leia"}})
	if !strings.Contains(rec.Header().Get("Location"), "profile-saved") {
		t.Fatalf("save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(); !strings.Contains(body, "<h1>Leia</h1>") {
		t.Fatal("the header still shows the old name")
	}
	authedRequest(s, a, http.MethodPost, "/account/profile", &url.Values{"display_name": {""}})
	if fc.names["acct-"+emailA] != "Leia" {
		t.Fatalf("an empty name field erased the name: %q", fc.names["acct-"+emailA])
	}
}

// A block stops a chat message before it reaches the chat service, which only checks
// friendship and would otherwise deliver it: blocking does not end a friendship.
func TestChatSendRefusedWhenBlocked(t *testing.T) {
	s, fc, _ := newTestServer(t)
	sent := 0
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"message":{"id":1,"body":"hi"}}`))
	}))
	defer svc.Close()
	s.Chat = chat.New(svc.URL, "k")
	emailA, emailB := seedTwoAccounts(fc)
	idA, idB := "acct-"+emailA, "acct-"+emailB
	fc.friends = []friendRow{{requester: idA, addressee: idB, accepted: true}}
	fc.blocks = [][2]string{{idB, idA}}
	a := login(t, s, emailA, "password-A-1")

	rec := authedRequest(s, a, http.MethodPost, "/account/chat/send", &url.Values{"peer": {idB}, "body": {"hi"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "m=chat-blocked") || sent != 0 {
		t.Fatalf("blocked: %d %s, %d sent", rec.Code, rec.Header().Get("Location"), sent)
	}
	if !strings.Contains(flashText("chat-blocked"), "blocked") {
		t.Fatal("the refusal has no words")
	}
	fc.blocks = nil
	rec = authedRequest(s, a, http.MethodPost, "/account/chat/send", &url.Values{"peer": {idB}, "body": {"hi"}})
	if rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "m=") || sent != 1 {
		t.Fatalf("unblocked: %d %s, %d sent", rec.Code, rec.Header().Get("Location"), sent)
	}
}
