package pages

import (
	"context"
	"testing"
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
