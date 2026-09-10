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

// A not-started (requestable/planned) title carries a Request control; live,
// beta and now alpha titles just show a status pill. The vote ranking is
// admin-only. Render the combinations so a gating regression fails a test, not
// production.
func TestTitlesVoteGating(t *testing.T) {
	votableRow := []store.Request{{ID: 7, Kind: "title", Name: "Tetris 99", Console: "Switch", Status: "requestable", Votes: 3, Voted: true}}
	betaRow := []store.Request{{ID: 8, Kind: "title", Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "beta", Votes: 0}}
	alphaRow := []store.Request{{ID: 9, Kind: "title", Name: "Fall Guys", Console: "Switch", Status: "alpha", Votes: 4}}

	// Signed out, not-started: a Request affordance to sign in, no form, and a
	// console logo for the row.
	out := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles"}, votableRow, []string{"Switch"}, []string{"requestable"}, false, "/titles"})
	for _, want := range []string{"Tetris 99", "/static/titles.js", "Sign in to request", "Request", "/static/consoles/switch.svg"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("signed-out not-started missing %q", want)
		}
	}
	if bytes.Contains([]byte(out), []byte(`action="/request/vote"`)) {
		t.Error("signed-out must not show a request form")
	}

	// Signed in, not-started: a request form with the id and CSRF token, the
	// "Requested" label (Voted), and NO vote count (ranking is admin-only).
	in := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, votableRow, []string{"Switch"}, []string{"requestable"}, false, "/titles"})
	for _, want := range []string{`action="/request/vote"`, `name="id" value="7"`, `value="tok"`, "Requested"} {
		if !bytes.Contains([]byte(in), []byte(want)) {
			t.Errorf("signed-in not-started missing %q", want)
		}
	}
	if bytes.Contains([]byte(in), []byte("· 3")) {
		t.Error("non-admin must not see the vote count")
	}

	// Admin, not-started: the vote count rides the button, and the scoreboard
	// toggle is present.
	adm := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok", Admin: true}, votableRow, []string{"Switch"}, []string{"requestable"}, false, "/titles"})
	for _, want := range []string{"· 3", `href="/titles?sort=votes"`} {
		if !bytes.Contains([]byte(adm), []byte(want)) {
			t.Errorf("admin view missing %q", want)
		}
	}

	// Signed in, alpha: a status pill, never a request form (alpha is started).
	al := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, alphaRow, []string{"Switch"}, []string{"alpha"}, false, "/titles"})
	if !bytes.Contains([]byte(al), []byte(">alpha<")) {
		t.Error("alpha row should show its status pill")
	}
	if bytes.Contains([]byte(al), []byte(`action="/request/vote"`)) {
		t.Error("alpha title must not show a request form")
	}

	// Signed in, NON-votable (beta): status shown, but no request UI at all.
	beta := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, betaRow, []string{"Switch"}, []string{"beta"}, false, "/titles"})
	if !bytes.Contains([]byte(beta), []byte("Mario Kart 8 Deluxe")) || !bytes.Contains([]byte(beta), []byte(">beta<")) {
		t.Error("beta row should still show name and status")
	}
	for _, forbidden := range []string{`action="/request/vote"`, "Sign in to request", `value="8"`} {
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
