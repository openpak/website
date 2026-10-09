// Package titleart names the game tiles under web/static/art/titles: one 256×256
// JPEG per non-Switch title, cut from the game's published art by
// scripts/title-art. The site and the generator share Slug so the file a title
// is written to is the file the site asks for.
package titleart

import (
	"os"
	"strings"
)

// Dir is where the tiles live, relative to the static root (served at /static/).
const Dir = "art/titles"

// Slug is the stable file stem for a title: console and name, lower case, ASCII,
// words joined by "-" ("Wii U", "Mario Kart 8" → "wii-u-mario-kart-8").
func Slug(console, name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(console + " " + name) {
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		case r == '\'' || r == '’' || r == '.':
			// "Devil's", "Dr. Mario", "S.T.E.A.M." keep their words whole
		default:
			dash = true
		}
	}
	return b.String()
}

// fold maps the accented letters game names use to their plain letter.
var fold = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ñ': 'n', 'ç': 'c',
}

// Set is the tiles present on disk, by slug.
type Set map[string]bool

// Load lists the tiles in dir once; a missing directory is an empty set.
func Load(dir string) Set {
	s := Set{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".jpg") {
			s[strings.TrimSuffix(n, ".jpg")] = true
		}
	}
	return s
}

// Path is the URL path of a title's tile under /static/, or "" when it has none.
// Switch titles never use it: they draw their own icon.
func (s Set) Path(console, name string) string {
	if console == "Switch" {
		return ""
	}
	if slug := Slug(console, name); s[slug] {
		return "/static/" + Dir + "/" + slug + ".jpg"
	}
	return ""
}
