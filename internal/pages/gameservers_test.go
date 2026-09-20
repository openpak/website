package pages

import (
	"context"
	"testing"
)

// A game server with no title id can never be credited with its players: the
// account core counts by title, and the row looked it up by a field nobody
// had filled in (2026-09-20, Mario Kart 8 Deluxe reading zero with two
// people racing).
func TestEveryGameServerCarriesItsTitleId(t *testing.T) {
	for _, g := range CheckGameServers(context.Background()) {
		if g.TitleID == "" {
			t.Errorf("%s has no title id, so its players are never counted", g.Name)
		}
	}
}

// A served title's name belongs to the catalogue: typed in two places it
// drifts, and the id is what both sides agree on.
func TestGameServerNamesComeFromTheCatalogue(t *testing.T) {
	got := CheckGameServers(context.Background(), map[string]string{
		"0100152000022000": "Mario Kart 8 Deluxe (catalogue)",
	})
	var seen bool
	for _, g := range got {
		if g.TitleID == "0100152000022000" {
			seen = true
			if g.Name != "Mario Kart 8 Deluxe (catalogue)" {
				t.Fatalf("name = %q, want the catalogue's", g.Name)
			}
		}
	}
	if !seen {
		t.Fatal("Mario Kart 8 Deluxe is not in the server list")
	}
	// Without a catalogue the rows still name themselves.
	for _, g := range CheckGameServers(context.Background()) {
		if g.Name == "" {
			t.Fatalf("%s has no name without a catalogue", g.TitleID)
		}
	}
}
