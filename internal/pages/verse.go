package pages

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/titleart"
)

// verseBase is OpenVerse, the community site. Every game OpenPak serves has a community there,
// created by the sync in nn-juxtaposition (apps/miiverse-api/tools/game-communities).
const verseBase = "https://verse.openpak.org"

// verseOverride lists communities that existed before the sync and keep their old id.
var verseOverride = map[string]string{
	"super-mario-maker": "10457138901939673655",
}

// verseCommunityID is the OpenVerse community id of a game, the twin of communityIdForKey in
// game-communities.ts: sha256("openverse-game:" + slug of the name), first 8 bytes big endian,
// top bit set. The same game on several consoles shares one community, so the console is not part
// of it.
func verseCommunityID(name string) string {
	key := titleart.Slug("", name)
	if id, ok := verseOverride[key]; ok {
		return id
	}
	sum := sha256.Sum256([]byte("openverse-game:" + key))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])|1<<63, 10)
}

// Verse is the address of the title's OpenVerse community, "" for a title that is not served
// (only live and beta titles have one).
func (t titleRow) Verse() string {
	if t.Kind != "title" || (t.Status != "live" && t.Status != "beta") || strings.TrimSpace(t.Name) == "" {
		return ""
	}
	return verseBase + "/communities/" + verseCommunityID(t.Name)
}

// VerseURL is the OpenVerse community address of a catalogue entry, "" when it has none; the app's
// "OpenVerse community" button follows the same rule as the website's.
func VerseURL(r store.Request) string { return titleRow{Request: r}.Verse() }
