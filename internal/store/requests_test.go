package store

import (
	"context"
	"errors"
	"testing"
)

func TestVotableRule(t *testing.T) {
	for _, s := range []string{"requestable", "alpha", "planned"} {
		if !(Request{Kind: "title", Status: s}).Votable() {
			t.Errorf("title %q should be votable", s)
		}
	}
	for _, s := range []string{"live", "beta"} {
		if (Request{Kind: "title", Status: s}).Votable() {
			t.Errorf("title %q should not be votable", s)
		}
	}
	// Features are never votable, whatever their status.
	if (Request{Kind: "feature", Status: "planned"}).Votable() {
		t.Error("a feature must never be votable")
	}
}

// The votable rule is enforced in the store, not just the UI: ToggleVote refuses
// a non-votable id and records nothing.
func TestToggleVoteRefusesNonVotable(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.SeedRequests(ctx, []Request{
		{Kind: "title", Name: "Mario Kart 8 Deluxe", Status: "beta", Sort: 1},
		{Kind: "feature", Name: "Cloud saves", Status: "planned", Sort: 2},
		{Kind: "title", Name: "Tetris 99", Status: "requestable", Sort: 3},
	})
	beta := idOf(t, m, "Mario Kart 8 Deluxe")
	feat := idOf(t, m, "Cloud saves")
	ok := idOf(t, m, "Tetris 99")

	if _, err := m.ToggleVote(ctx, beta, "acct-a"); !errors.Is(err, ErrNotVotable) {
		t.Errorf("beta title vote: err = %v, want ErrNotVotable", err)
	}
	if _, err := m.ToggleVote(ctx, feat, "acct-a"); !errors.Is(err, ErrNotVotable) {
		t.Errorf("feature vote: err = %v, want ErrNotVotable", err)
	}
	if _, err := m.ToggleVote(ctx, 99999, "acct-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: err = %v, want ErrNotFound", err)
	}
	// Nothing recorded on the refused ids; the votable one works.
	if v, _, _ := m.RequestVotes(ctx, beta, "acct-a"); v != 0 {
		t.Errorf("beta recorded %d votes, want 0", v)
	}
	if voted, err := m.ToggleVote(ctx, ok, "acct-a"); err != nil || !voted {
		t.Errorf("requestable vote: voted=%v err=%v, want true/nil", voted, err)
	}
}

// idOf finds a seeded request's id by name (memory assigns ids on seed).
func idOf(t *testing.T, m *Memory, name string) int64 {
	t.Helper()
	reqs, _ := m.ListRequests(context.Background(), "", "")
	for _, r := range reqs {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("no request named %q", name)
	return 0
}

func TestCatalogParses(t *testing.T) {
	reqs, err := CatalogRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) < 40 {
		t.Fatalf("catalog has %d entries; expected the full list", len(reqs))
	}
	var titles, features int
	for _, r := range reqs {
		switch r.Kind {
		case "title":
			titles++
		case "feature":
			features++
		default:
			t.Errorf("unexpected kind %q for %q", r.Kind, r.Name)
		}
	}
	if titles == 0 || features == 0 {
		t.Fatalf("expected both titles and features, got %d/%d", titles, features)
	}
}

func TestVoteToggleOnePerAccountIdempotent(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	if err := m.SeedRequests(ctx, []Request{
		{Kind: "title", Name: "Tetris 99", Status: "requestable", Sort: 1},
		{Kind: "title", Name: "Rocket League", Status: "requestable", Sort: 2},
	}); err != nil {
		t.Fatal(err)
	}
	id := idOf(t, m, "Tetris 99")

	// First vote from A: voted=true, count=1.
	if v, _ := m.ToggleVote(ctx, id, "acct-a"); !v {
		t.Fatal("first vote should report voted=true")
	}
	// B votes too: count=2.
	if v, _ := m.ToggleVote(ctx, id, "acct-b"); !v {
		t.Fatal("B's vote should report voted=true")
	}
	reqs, _ := m.ListRequests(ctx, "title", "acct-a")
	for _, r := range reqs {
		if r.ID == id {
			if r.Votes != 2 {
				t.Errorf("votes = %d, want 2", r.Votes)
			}
			if !r.Voted {
				t.Error("A should read voted=true")
			}
		}
	}
	// A toggles again: unvote → count back to 1, and A no longer voted.
	if v, _ := m.ToggleVote(ctx, id, "acct-a"); v {
		t.Fatal("second toggle from A should report voted=false")
	}
	reqs, _ = m.ListRequests(ctx, "title", "acct-a")
	for _, r := range reqs {
		if r.ID == id {
			if r.Votes != 1 {
				t.Errorf("after unvote votes = %d, want 1", r.Votes)
			}
			if r.Voted {
				t.Error("A should read voted=false after unvote")
			}
		}
	}
}

// Reseeding never wipes votes (they are user data) and never changes a status the database
// already holds: the database is the source of truth for status.
func TestReseedPreservesVotes(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.SeedRequests(ctx, []Request{{Kind: "title", Name: "Among Us", Status: "alpha", Sort: 1}})
	id := idOf(t, m, "Among Us")
	_, _ = m.ToggleVote(ctx, id, "acct-a")

	// Reseed with a new status.
	_ = m.SeedRequests(ctx, []Request{{Kind: "title", Name: "Among Us", Status: "beta", Sort: 1}})
	reqs, _ := m.ListRequests(ctx, "title", "acct-a")
	if len(reqs) != 1 {
		t.Fatalf("reseed duplicated the row: %d rows", len(reqs))
	}
	if reqs[0].Status != "alpha" {
		t.Errorf("status = %q, want the database's alpha kept over the catalog's beta", reqs[0].Status)
	}
	if reqs[0].Votes != 1 || !reqs[0].Voted {
		t.Errorf("reseed lost the vote: votes=%d voted=%v", reqs[0].Votes, reqs[0].Voted)
	}
	if reqs[0].ID != id {
		t.Errorf("reseed changed the id: %d -> %d", id, reqs[0].ID)
	}
}

// A feature has no admin status control, so its status follows the catalog; and a row the
// catalog drops goes away unless someone voted for it.
func TestReseedFeaturesFollowCatalogAndPrunes(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.SeedRequests(ctx, []Request{
		{Kind: "feature", Name: "nimbus", Console: "3DS", Status: "planned"},
		{Kind: "feature", Name: "Nintendo DS network", Console: "DS", Status: "planned"},
		{Kind: "title", Name: "Voted", Console: "DS", Status: "requestable"},
		{Kind: "title", Name: "Unvoted", Console: "DS", Status: "requestable"},
	})
	if _, err := m.ToggleVote(ctx, idOf(t, m, "Voted"), "acct-a"); err != nil {
		t.Fatal(err)
	}
	_ = m.SeedRequests(ctx, []Request{{Kind: "feature", Name: "nimbus", Console: "3DS", Status: "alpha"}})
	reqs, _ := m.ListRequests(ctx, "", "")
	got := map[string]string{}
	for _, r := range reqs {
		got[r.Name] = r.Status
	}
	if got["nimbus"] != "alpha" {
		t.Errorf("feature status = %q, want the catalog's alpha", got["nimbus"])
	}
	if _, ok := got["Nintendo DS network"]; ok {
		t.Error("a feature the catalog dropped should be deleted")
	}
	if _, ok := got["Unvoted"]; ok {
		t.Error("an unvoted title the catalog dropped should be deleted")
	}
	if _, ok := got["Voted"]; !ok {
		t.Error("a title with votes must survive being dropped from the catalog")
	}
}
