package netprofile

import (
	"os"
	"strings"
	"testing"
)

// NP-3: a Traefik name with no platform map fails the build. The snapshot is
// the live dynamic.yml from production; when routing changes on the box, the
// snapshot is refreshed in the same PR as the map — and if the map was
// forgotten, this test is what says so, with the offending names listed.
func TestEveryRoutedNameIsClaimedByAPlatform(t *testing.T) {
	routes, err := LoadRoutes("testdata/traefik.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Names) < 20 {
		t.Fatalf("only %d routed names parsed from the snapshot; the parser has lost track", len(routes.Names))
	}
	m, err := parseMap(embeddedMap)
	if err != nil {
		t.Fatal(err)
	}
	if bad := Drift(m, routes); len(bad) > 0 {
		t.Fatalf("routed names no platform claims:\n%s", strings.Join(bad, "\n"))
	}
}

func TestDriftCatchesAnUnclaimedName(t *testing.T) {
	routes := Routes{Names: []string{"suddenly.nintendo.example"}}
	m, err := parseMap(embeddedMap)
	if err != nil {
		t.Fatal(err)
	}
	bad := Drift(m, routes)
	if len(bad) != 1 || bad[0] != "suddenly.nintendo.example" {
		t.Fatalf("drift missed an unclaimed name: %v", bad)
	}
}

// A pattern naming somewhere no family reaches is drift too.
func TestDriftCatchesAnUnclaimedPattern(t *testing.T) {
	routes := Routes{Patterns: []string{`^api\.example\.vendor\.tld$`}}
	m, err := parseMap(embeddedMap)
	if err != nil {
		t.Fatal(err)
	}
	if bad := Drift(m, routes); len(bad) != 1 {
		t.Fatalf("drift missed an unclaimed pattern: %v", bad)
	}
}

// The shipped map lies inside the signed ceiling production serves:
// testdata/netprofile-ceiling.json is a copy of the envelope on the box,
// verified here under the pinned key. Adding a family to platforms.yml
// without signing a ceiling that carries it fails here instead of being
// dropped silently in production; after `openpak-policy ceiling add`, copy
// the new envelope over this file in the same change.
func TestEmbeddedMapInsideSignedCeiling(t *testing.T) {
	body, err := os.ReadFile("testdata/netprofile-ceiling.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := VerifyCeiling(body, PinnedKeys)
	if err != nil {
		t.Fatalf("the committed ceiling does not verify under the pinned key: %v", err)
	}
	if _, err := LoadMapWithin(embeddedMap, c); err != nil {
		t.Fatal(err)
	}
}
