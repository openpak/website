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
	ByVotes  bool
	Back     string
}

// features.html is data-driven now; render it both ways so a scope bug fails a
// test rather than the page.
func TestFeaturesPageRenders(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.Request{{ID: 4, Name: "Cloud saves (SCSI)", Category: "Content", Console: "Switch", Status: "planned", Votes: 2}}

	out := featuresPage{baseData{Lang: "en", Path: "/features"}, rows, false, "/features"}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "features.html", out); err != nil {
		t.Fatalf("features.html (signed out) failed: %v", err)
	}
	for _, want := range []string{"Cloud saves (SCSI)", "planned", "Sign in to vote"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("signed-out features.html missing %q", want)
		}
	}

	in := out
	in.baseData = baseData{Lang: "en", Path: "/features", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}
	b.Reset()
	if err := tmpl.ExecuteTemplate(&b, "features.html", in); err != nil {
		t.Fatalf("features.html (signed in) failed: %v", err)
	}
	for _, want := range []string{`action="/request/vote"`, `name="id" value="4"`} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("signed-in features.html missing %q", want)
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
