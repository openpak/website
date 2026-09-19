package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openpak/website/internal/store"
)

// Declining a friend request removes the pending row, as the website's Decline button does.
func TestDeclineFriendRequest(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	tok := signIn(t, s)
	fc.friends["acct-b@x.test"] = []string{"acct-a@x.test"} // B asked A
	rec := do(t, s, http.MethodPost, "/api/v1/me/friends/requests/decline", tok, map[string]string{"account_id": "acct-b@x.test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("decline: %d %s", rec.Code, rec.Body.String())
	}
	if len(fc.friends["acct-b@x.test"]) != 0 {
		t.Fatalf("the request is still pending: %v", fc.friends)
	}
}

// The catalogue needs no bearer and comes in the website's order: playable first, the
// requestable long tail after, marked as such.
func TestTitlesArePublicAndOrdered(t *testing.T) {
	s, _, db := newTestAPI(t)
	if err := db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Zelda", Console: "3DS", Status: "requestable", Sort: 1},
		{Kind: "title", Name: "Stardew Valley", Console: "Switch", Status: "live", Sort: 2},
		{Kind: "feature", Name: "Cloud saves", Status: "working", Sort: 3},
	}); err != nil {
		t.Fatal(err)
	}
	rec := do(t, s, http.MethodGet, "/api/v1/titles", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("titles: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Titles []struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Requestable bool   `json:"requestable"`
		} `json:"titles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Titles) != 2 || out.Titles[0].Name != "Stardew Valley" || out.Titles[0].Requestable ||
		out.Titles[1].Name != "Zelda" || !out.Titles[1].Requestable || out.Titles[1].Status != "requestable" {
		t.Fatalf("titles: %+v", out.Titles)
	}
}

// The reset mail's token and a new password change the password, as on the website; a token
// the core did not issue is a 400 in the usual error shape.
func TestPasswordReset(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	rec := do(t, s, http.MethodPost, "/api/v1/password/reset", "", map[string]string{"token": "forged", "password": "password-A-2"})
	if rec.Code != http.StatusBadRequest || fc.accounts["a@x.test"] != "password-A-1" {
		t.Fatalf("bad token: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodPost, "/api/v1/password/reset", "", map[string]string{"token": "reset-a", "password": "password-A-2"})
	if rec.Code != http.StatusOK || fc.accounts["a@x.test"] != "password-A-2" {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
}
