package pages

import (
	"context"
	"testing"

	"github.com/openpak/website/internal/store"
)

// The core reports a title id in lower case and every table here is written
// in upper: a case-sensitive lookup showed a game with a player on it as
// nobody playing (2026-09-20, ARMS).
func TestTitleNamesAreFoundWhateverTheCase(t *testing.T) {
	if got := titleName("0100e65002bb8000"); got != "Stardew Valley" {
		t.Fatalf("titleName(lower) = %q", got)
	}
	if got := titleName("0100E65002BB8000"); got != "Stardew Valley" {
		t.Fatalf("titleName(upper) = %q", got)
	}
	got, _ := FriendGame(context.Background(), map[string]string{"01009B500007C000": "ARMS"}, "switch", "01009b500007c000")
	if got != "ARMS" {
		t.Fatalf("FriendGame(lower) = %q, want the catalogue's name", got)
	}
}

func TestCatalogueTitleNameRequiresDatabaseEntry(t *testing.T) {
	catalogue := map[string]string{
		"01009B500007C000": "ARMS",
		"000500001010ED00": "Mario Kart 8",
		"0004000000030800": "Mario Kart 7",
	}
	for id, want := range map[string]string{
		"01009b500007c000": "ARMS",
		"0005000e1010ed00": "Mario Kart 8",
		"0004000e00030800": "Mario Kart 7",
		"010025901a268000": "",
		"0100E65002BB8000": "", // A static name alone does not put a title in the database.
		"":                 "",
	} {
		if got := CatalogueTitleName(catalogue, id); got != want {
			t.Errorf("CatalogueTitleName(%q) = %q, want %q", id, got, want)
		}
	}
	if got := CatalogueTitleName(nil, "01009b500007c000"); got != "" {
		t.Errorf("an unavailable catalogue resolved %q", got)
	}
}

func TestHasGameServer(t *testing.T) {
	if !hasGameServer(store.Request{TitleID: "010047700D540000"}) {
		t.Error("Clubhouse Games has a game server")
	}
	if !hasGameServer(store.Request{TitleIDs: []string{"0000000000000000", "010047700d540000"}}) {
		t.Error("any of a title's ids counts, in any case")
	}
	if hasGameServer(store.Request{TitleID: "0000000000000000"}) || hasGameServer(store.Request{}) {
		t.Error("a title without a server is not listed")
	}
}
