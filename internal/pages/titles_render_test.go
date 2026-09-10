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

// The /titles page 500'd once because a template used .Lang inside the
// {{with .Titles}} scope, where the dot is the slice, not baseData. Render it
// for real — both signed-out and signed-in — so that class of error fails a
// test, not production.
func TestTitlesPageRenders(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.Request{{ID: 7, Name: "Among Us", Console: "Switch", Status: "alpha", Votes: 3, Voted: true}}

	// Signed out: a count and a sign-in affordance, no vote button.
	out := titlesPage{baseData{Lang: "en", Path: "/titles"}, rows, []string{"Switch"}, []string{"alpha"}, false, "/titles"}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "titles.html", out); err != nil {
		t.Fatalf("titles.html (signed out) failed: %v", err)
	}
	for _, want := range []string{"titles-console", "Among Us", "/static/titles.js", "Sign in to vote"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("signed-out titles.html missing %q", want)
		}
	}
	if bytes.Contains(b.Bytes(), []byte(`action="/request/vote"`)) {
		t.Error("signed-out titles.html must not show a vote form")
	}

	// Signed in: a vote form with the request id and the CSRF token.
	in := out
	in.baseData = baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}
	b.Reset()
	if err := tmpl.ExecuteTemplate(&b, "titles.html", in); err != nil {
		t.Fatalf("titles.html (signed in) failed: %v", err)
	}
	for _, want := range []string{`action="/request/vote"`, `name="id" value="7"`, `value="tok"`, "Voted"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("signed-in titles.html missing %q", want)
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
