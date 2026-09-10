package pages

import (
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestPopularTitles(t *testing.T) {
	all := []store.Title{
		{Name: "ARMS"},
		{Name: "Splatoon 3"},
		{Name: "Mario Kart 8 Deluxe"},
		{Name: "Some Unlisted Game"},
	}
	got := popularTitles(all)
	// popularNames order, skipping any not in the catalogue; never invents one.
	want := []string{"Mario Kart 8 Deluxe", "Splatoon 3"}
	if len(got) != len(want) {
		t.Fatalf("got %d titles, want %d: %v", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, n)
		}
	}
	// A title not in popularNames must never appear.
	for _, g := range got {
		if g.Name == "Some Unlisted Game" {
			t.Fatal("popularTitles surfaced a non-popular title")
		}
	}
}
