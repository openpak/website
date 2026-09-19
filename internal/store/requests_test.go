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
