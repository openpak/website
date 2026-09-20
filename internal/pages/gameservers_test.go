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
