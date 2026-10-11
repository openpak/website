package pages

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

func TestStatusShowsDontStarveTogetherAndItsPlayers(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("health probe path = %q, want /healthz", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer health.Close()
	t.Setenv("DST_HEALTH_URL", health.URL+"/healthz")
	s, fc, db := newTestServer(t)
	if err := db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Don't Starve Together", Console: "Switch", Status: "live", TitleID: "010090100E334000"},
	}); err != nil {
		t.Fatal(err)
	}
	fc.players = []core.TitlePlayers{
		{TitleID: "010090100e334000", Namespace: "switch", Players: 1},
		{TitleID: "010025901a268000", Namespace: "switch", Players: 1},
	}
	fc.totalPlayers = 2
	fc.networkPlayers = map[string]int{"switch": 2}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, panel := range []struct{ id, count string }{{"srv-h", "1 online"}, {"play-h", "1 player"}} {
		_, section, ok := strings.Cut(body, `aria-labelledby="`+panel.id+`">`)
		if !ok {
			t.Fatalf("missing panel %s", panel.id)
		}
		section, _, _ = strings.Cut(section, "</section>")
		row := regexp.MustCompile(`(?s)<li class="state-row ok">\s*<span class="name">Don't Starve Together</span>\s*<span class="pill">\s*` + regexp.QuoteMeta(panel.count))
		if !row.MatchString(section) {
			t.Errorf("%s should show Don't Starve Together with %s", panel.id, panel.count)
		}
	}
	if strings.Contains(strings.ToLower(body), "010025901a268000") {
		t.Error("DBI must not appear as a game")
	}
	if !strings.Contains(body, "2 online") {
		t.Error("both consoles must still count as online")
	}
}

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
