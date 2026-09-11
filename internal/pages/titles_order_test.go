package pages

import (
	"testing"

	"github.com/openpak/website/internal/store"
)

// The list shows what can be played first, by console, alphabetically; the long tail follows
// the same way. Filtering by console or status must not disturb that, so the keys are fixed.
func TestCatalogOrder(t *testing.T) {
	in := []store.Request{
		{Name: "Zelda", Console: "3DS", Status: "requestable"},
		{Name: "Splatoon", Console: "Wii U", Status: "beta"},
		{Name: "ARMS", Console: "Switch", Status: "beta"},
		{Name: "Stardew Valley", Console: "Switch", Status: "live"},
		{Name: "Mario Kart 7", Console: "3DS", Status: "beta"},
		{Name: "Animal Crossing: New Horizons", Console: "Switch", Status: "beta"},
		{Name: "Bayonetta 2", Console: "Wii U", Status: "requestable"},
	}
	got := catalogOrder(in)
	want := []string{"Mario Kart 7", "Stardew Valley", "Animal Crossing: New Horizons", "ARMS", "Splatoon", "Zelda", "Bayonetta 2"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("position %d: got %q, want %q (full: %v)", i, got[i].Name, w, names(got))
		}
	}
}

func names(rs []store.Request) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}
