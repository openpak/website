package pages

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/openpak/website/internal/store"
)

type featuresPage struct {
	baseData
	Features []store.Request
	Statuses []string
	Consoles []string
}

// The feature matrix is display-only: honest status, and NO vote UI (voting is
// for games). Render it signed in to prove the vote controls are gone.
func TestFeaturesPageIsDisplayOnly(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.Request{{ID: 4, Name: "Cloud saves (SCSI)", Category: "Content", Console: "Switch", Status: "planned", Votes: 2}}

	var b bytes.Buffer
	pg := featuresPage{baseData{Lang: "en", Path: "/features", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, rows, []string{"planned"}, []string{"Switch"}}
	if err := tmpl.ExecuteTemplate(&b, "features.html", pg); err != nil {
		t.Fatalf("features.html failed: %v", err)
	}
	for _, want := range []string{"Cloud saves (SCSI)", "planned"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("features.html missing %q", want)
		}
	}
	for _, forbidden := range []string{`action="/request/vote"`, "Sign in to vote", "?sort=votes"} {
		if bytes.Contains(b.Bytes(), []byte(forbidden)) {
			t.Errorf("features.html must not show vote UI, found %q", forbidden)
		}
	}
}

// The vote button end to end: a signed-in POST toggles the stored vote, and a
// second POST removes it.
func TestVoteHandlerTogglesThroughTheSite(t *testing.T) {
	s, fc, db := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	ctx := context.Background()
	if err := db.SeedRequests(ctx, []store.Request{{Kind: "title", Name: "Tetris 99", Status: "requestable", Sort: 1}}); err != nil {
		t.Fatal(err)
	}
	reqs, _ := db.ListRequests(ctx, "title", "")
	id := reqs[0].ID

	rec := login(t, s, emailA, "password-A-1")
	form := &url.Values{"id": {strconv.FormatInt(id, 10)}, "back": {"/titles"}}
	got := authedRequest(s, rec, http.MethodPost, "/request/vote", form)
	if got.Code != http.StatusSeeOther {
		t.Fatalf("vote: status %d, want %d; body=%s", got.Code, http.StatusSeeOther, got.Body.String())
	}
	after, _ := db.ListRequests(ctx, "title", "acct-"+emailA)
	if after[0].Votes != 1 || !after[0].Voted {
		t.Fatalf("after vote: votes=%d voted=%v, want 1/true", after[0].Votes, after[0].Voted)
	}

	// Toggle off.
	form2 := &url.Values{"id": {strconv.FormatInt(id, 10)}, "back": {"/titles"}}
	authedRequest(s, rec, http.MethodPost, "/request/vote", form2)
	after, _ = db.ListRequests(ctx, "title", "acct-"+emailA)
	if after[0].Votes != 0 {
		t.Fatalf("after unvote: votes=%d, want 0", after[0].Votes)
	}
}

func TestVoteRejectsBadBackRedirect(t *testing.T) {
	for in, want := range map[string]string{
		"/titles":            "/titles",
		"/titles?sort=votes": "/titles?sort=votes",
		"/features":          "/features",
		"https://evil.test":  "/titles",
		"//evil.test":        "/titles",
		"":                   "/titles",
	} {
		if got := safeBack(in); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", in, got, want)
		}
	}
}

// The admin form stores product links, refuses one off the shop's site, and is
// admins only.
func TestSetShopLinksThroughTheSite(t *testing.T) {
	s, fc, db := newTestServer(t)
	emailA, emailB := seedTwoAccounts(fc)
	s.AdminAccountIDs = []string{"acct-" + emailA}
	ctx := context.Background()
	if err := db.SeedRequests(ctx, []store.Request{{Kind: "title", Name: "Mario Kart 7", Console: "3DS", Status: "live", Sort: 1}}); err != nil {
		t.Fatal(err)
	}
	reqs, _ := db.ListRequests(ctx, "title", "")
	id := strconv.FormatInt(reqs[0].ID, 10)
	gs := "https://www.gamestop.com/video-games/nintendo-3ds/products/mario-kart-7/10095226.html"

	if got := authedRequest(s, login(t, s, emailB, "password-B-1"), http.MethodPost, "/admin/titles/shops", &url.Values{"id": {id}, "gamestop": {gs}}); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", got.Code)
	}
	admin := login(t, s, emailA, "password-A-1")
	if got := authedRequest(s, admin, http.MethodPost, "/admin/titles/shops", &url.Values{"id": {id}, "gamestop": {"https://evil.test/"}}); got.Code != http.StatusBadRequest {
		t.Fatalf("off-site link: %d", got.Code)
	}
	if got := authedRequest(s, admin, http.MethodPost, "/admin/titles/shops", &url.Values{"id": {id}, "gamestop": {gs}}); got.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", got.Code, got.Body.String())
	}
	if page := authedRequest(s, admin, http.MethodGet, "/titles", nil).Body.String(); !bytes.Contains([]byte(page), []byte(`href="`+gs+`"`)) {
		t.Error("the stored GameStop page is not linked")
	}
	// Blank clears it again.
	authedRequest(s, admin, http.MethodPost, "/admin/titles/shops", &url.Values{"id": {id}})
	if links, _ := db.ListShopLinks(ctx); len(links) != 0 {
		t.Fatalf("cleared links: %+v", links)
	}
}
