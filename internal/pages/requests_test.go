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
	pg := featuresPage{baseData{Lang: "en", Path: "/features", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, rows, []string{"planned"}}
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
