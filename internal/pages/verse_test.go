package pages

import (
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestVerseCommunityID(t *testing.T) {
	// Pinned against communityIdForKey('terraria') in game-communities.test.ts.
	if got := verseCommunityID("Terraria"); got != "12406839772376467666" {
		t.Errorf("Terraria = %s", got)
	}
	if verseCommunityID("Terraria") != verseCommunityID("terraria") {
		t.Error("the id depends on the name's case")
	}
	if got := verseCommunityID("Super Mario Maker"); got != "10457138901939673655" {
		t.Errorf("Super Mario Maker keeps its old id, got %s", got)
	}
}

func TestTitleRowVerse(t *testing.T) {
	row := func(kind, status string) titleRow {
		return titleRow{Request: store.Request{Kind: kind, Name: "Terraria", Status: status}}
	}
	if got := row("title", "live").Verse(); got != "https://verse.openpak.org/communities/12406839772376467666" {
		t.Errorf("live title: %q", got)
	}
	if row("title", "beta").Verse() == "" {
		t.Error("a beta title has a community")
	}
	for _, c := range [][2]string{{"title", "requestable"}, {"title", "planned"}, {"feature", "live"}} {
		if got := row(c[0], c[1]).Verse(); got != "" {
			t.Errorf("%v: %q", c, got)
		}
	}
}
