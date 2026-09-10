package pages

import (
	"bytes"
	"testing"

	"github.com/openpak/website/internal/store"
)

type titlesPage struct {
	baseData
	Titles   []store.Request
	Consoles []string
	Statuses []string
	ByVotes  bool
	Back     string
}

func renderTitles(t *testing.T, pg titlesPage) string {
	t.Helper()
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "titles.html", pg); err != nil {
		t.Fatalf("titles.html failed to render: %v", err)
	}
	return b.String()
}

// A votable (requestable/alpha/planned) title carries a vote control; a live or
// beta title does not — just its status. Render both, signed in and out, so a
// scope bug or a gating regression fails a test, not production.
func TestTitlesVoteGating(t *testing.T) {
	votableRow := []store.Request{{ID: 7, Name: "Tetris 99", Console: "Switch", Status: "requestable", Votes: 3, Voted: true}}
	betaRow := []store.Request{{ID: 8, Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "beta", Votes: 0}}

	// Signed out, votable: a count and a sign-in affordance, no form.
	out := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles"}, votableRow, []string{"Switch"}, []string{"requestable"}, false, "/titles"})
	for _, want := range []string{"Tetris 99", "/static/titles.js", "Sign in to vote"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("signed-out votable missing %q", want)
		}
	}
	if bytes.Contains([]byte(out), []byte(`action="/request/vote"`)) {
		t.Error("signed-out must not show a vote form")
	}

	// Signed in, votable: a vote form with the id and CSRF token.
	in := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, votableRow, []string{"Switch"}, []string{"requestable"}, false, "/titles"})
	for _, want := range []string{`action="/request/vote"`, `name="id" value="7"`, `value="tok"`, "Voted"} {
		if !bytes.Contains([]byte(in), []byte(want)) {
			t.Errorf("signed-in votable missing %q", want)
		}
	}

	// Signed in, NON-votable (beta): status shown, but no vote UI at all.
	beta := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, betaRow, []string{"Switch"}, []string{"beta"}, false, "/titles"})
	if !bytes.Contains([]byte(beta), []byte("Mario Kart 8 Deluxe")) || !bytes.Contains([]byte(beta), []byte(">beta<")) {
		t.Error("beta row should still show name and status")
	}
	for _, forbidden := range []string{`action="/request/vote"`, "Sign in to vote", `value="8"`} {
		if bytes.Contains([]byte(beta), []byte(forbidden)) {
			t.Errorf("non-votable beta title must not show %q", forbidden)
		}
	}
}

func TestMostRequestedOrdersByVotes(t *testing.T) {
	got := mostRequested([]store.Request{
		{Name: "Low", Sort: 0, Votes: 1},
		{Name: "High", Sort: 1, Votes: 9},
		{Name: "TieA", Sort: 2, Votes: 5},
		{Name: "TieB", Sort: 3, Votes: 5},
	})
	want := []string{"High", "TieA", "TieB", "Low"} // votes desc, ties by catalog sort
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, n)
		}
	}
}

func TestVotableStatuses(t *testing.T) {
	for _, s := range []string{"requestable", "alpha", "planned"} {
		if !votable(s) {
			t.Errorf("%q should be votable", s)
		}
	}
	for _, s := range []string{"live", "beta", "working", "partial", "broken", ""} {
		if votable(s) {
			t.Errorf("%q should not be votable", s)
		}
	}
}
