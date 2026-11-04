package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type testBoards struct {
	entries []companion.Entry
	total   int // when set, the board is paged: this many ranks, entries is one page
}

func (testBoards) Boards(context.Context) ([]companion.Board, error) {
	return []companion.Board{{ID: "one", Label: "Board One"}, {ID: "two", Label: "Board Two"}}, nil
}
func (b testBoards) Ranking(_ context.Context, _ string, q companion.RankQuery) (companion.Ranking, error) {
	r := companion.Ranking{Entries: b.entries}
	if b.total > 0 {
		r.Total, r.Limit, r.Offset = b.total, len(b.entries), q.Offset
	}
	return r, nil
}

// testEmpty is a game whose boards exist but nobody has a score on; none lists no boards at all.
type testEmpty struct{ none bool }

func (e testEmpty) Boards(context.Context) ([]companion.Board, error) {
	if e.none {
		return []companion.Board{}, nil
	}
	return []companion.Board{{ID: "0", Label: "Time trial", NoScores: true}, {ID: "1", Label: "Board 2", NoScores: true}}, nil
}
func (testEmpty) Ranking(context.Context, string, companion.RankQuery) (companion.Ranking, error) {
	return companion.Ranking{Entries: []companion.Entry{}}, nil
}

type testStats struct{ linked bool }

func (s testStats) Stats(context.Context, string) (companion.Card, error) {
	return companion.Card{Linked: s.linked, Stats: []companion.Stat{{Key: "activity.games", Value: "7"}},
		Rows: []companion.Row{{Title: "Row one", Value: "3", Percent: 40}}}, nil
}

type testHistory struct{}

func (testHistory) PlayHistory(_ context.Context, _, ns string, _ int) ([]core.PlayEntry, error) {
	if ns != "switch" {
		return nil, nil
	}
	return []core.PlayEntry{{Namespace: "switch", TitleID: "0100E65002BB8000", Sessions: 2, Total: 95 * time.Minute,
		First: time.Now().Add(-72 * time.Hour), Last: time.Now().Add(-time.Hour)}}, nil
}

func companionServer(t *testing.T) (*Server, *fakeCore) {
	t.Helper()
	s, fc, db := newTestServer(t)
	seedTwoAccounts(fc)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Mario Kart Wii", Console: "Wii", Status: "beta"},
		{Kind: "title", Name: "Stardew Valley", Console: "Switch", Status: "live", TitleID: "0100E65002BB8000", TitleIDs: []string{"0100E65002BB8000"}},
		{Kind: "title", Name: "Halo 3", Console: "Xbox 360", Status: "beta"},
		{Kind: "title", Name: "Aaa Empty Racer", Console: "Wii", Status: "beta"},
		{Kind: "title", Name: "No Boards Yet", Console: "PS3", Status: "beta"},
	})
	c := companion.New(db)
	c.History = testHistory{}
	c.Register(companion.Source{Names: []string{"Mario Kart Wii"}, Rankings: testBoards{entries: []companion.Entry{
		{Name: "Ana", Display: "1:23.456"}, {Name: "Bo", Display: "1:24.000"}, {Name: "Cy", Display: "1:25.500"},
		{Name: "Di", Display: "1:26.000"}, {Name: "Ed", Display: "1:27.250", Me: true}}}, Stats: testStats{linked: true}})
	c.Register(companion.Source{Names: []string{"Aaa Empty Racer"}, Rankings: testEmpty{}})
	c.Register(companion.Source{Names: []string{"No Boards Yet"}, Rankings: testEmpty{none: true}})
	s.Companion = c
	return s, fc
}

func getPath(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestCompanionPages(t *testing.T) {
	s, _ := companionServer(t)

	w := getPath(s, "/titles/mario-kart-wii")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Mario Kart Wii") || !strings.Contains(w.Body.String(), `href="/titles/mario-kart-wii/rankings"`) {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String())
	}
	w = getPath(s, "/titles/mario-kart-wii/rankings?board=two")
	body := w.Body.String()
	for _, want := range []string{"Board Two", "podium", "Ana", "1:27.250", `aria-current="true"`, "Sign in"} {
		if !strings.Contains(body, want) {
			t.Errorf("rankings page lacks %q", want)
		}
	}
	if w = getPath(s, "/titles/mario-kart-wii/rankings?board=nope"); w.Code != 404 {
		t.Errorf("unknown board: %d", w.Code)
	}
	if w = getPath(s, "/titles/halo-3"); w.Code != 404 {
		t.Errorf("a title with no feature has a page: %d", w.Code)
	}
	if w = getPath(s, "/titles/mario-kart-wii/gallery"); w.Code != 404 {
		t.Errorf("unregistered tab: %d", w.Code)
	}
	// The icon route must keep winning over /titles/{slug}/{tab}.
	if w = getPath(s, "/titles/icon/0100E65002BB8000"); w.Code == 404 && strings.Contains(w.Body.String(), "Mario") {
		t.Errorf("icon route shadowed")
	}
	// Signed out, the stats tab asks for a sign-in.
	if w = getPath(s, "/titles/mario-kart-wii/stats"); w.Code != 200 || !strings.Contains(w.Body.String(), "Sign in to see your stats") {
		t.Errorf("stats signed out: %d", w.Code)
	}
}

func TestCompanionStatsSignedIn(t *testing.T) {
	s, _ := companionServer(t)
	rec := login(t, s, "a@x.test", "password-A-1")
	w := authedRequest(s, rec, http.MethodGet, "/titles/mario-kart-wii/stats", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Row one") || !strings.Contains(w.Body.String(), `value="40"`) {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
}

func TestTitlesPageLinksToCompanion(t *testing.T) {
	s, _ := companionServer(t)
	body := getPath(s, "/titles").Body.String()
	if !strings.Contains(body, `href="/titles/mario-kart-wii"`) {
		t.Error("titles dialog has no link to the companion page")
	}
	if strings.Contains(body, `href="/titles/halo-3"`) {
		t.Error("a title with no feature is linked")
	}
}

func TestActivityPage(t *testing.T) {
	s, _ := companionServer(t)
	if w := getPath(s, "/account/activity"); w.Code != http.StatusSeeOther {
		t.Fatalf("signed out: %d", w.Code)
	}
	rec := login(t, s, "a@x.test", "password-A-1")
	w := authedRequest(s, rec, http.MethodGet, "/account/activity", nil)
	body := w.Body.String()
	for _, want := range []string{"Stardew Valley", "1 h 35 min", "Nintendo Switch", "2 sessions"} {
		if !strings.Contains(body, want) {
			t.Errorf("activity page lacks %q:\n%s", want, body)
		}
	}
	s.Companion.History = nil
	w = authedRequest(s, rec, http.MethodGet, "/account/activity", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "not available") {
		t.Errorf("no backend: %d", w.Code)
	}
}

type testListings struct{}

func (testListings) Listings(context.Context) (companion.Card, error) {
	return companion.Card{Linked: true, Stats: []companion.Stat{{Key: "companion.listings.open", Value: "1"}},
		Rows: []companion.Row{{Title: "Pokémon #25 · Lv. 30", Sub: "Gen 6", Value: "PIKA", Percent: -1}}}, nil
}

type testTrophies struct{}

func (testTrophies) Trophies(context.Context, string) ([]companion.TrophyGame, error) {
	return []companion.TrophyGame{{CommunicationID: "NPWR12345_00", Trophies: []companion.Trophy{{Grade: "G", EarnedAt: time.Now()}}}}, nil
}

func TestListingsAndTrophies(t *testing.T) {
	s, _ := companionServer(t)
	s.Companion.Register(companion.Source{Names: []string{"Halo 3"}, Listings: testListings{}})
	w := getPath(s, "/titles/halo-3/listings")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Pokémon #25") || !strings.Contains(w.Body.String(), "PIKA") {
		t.Fatalf("listings: %d %s", w.Code, w.Body.String())
	}
	s.Companion.TrophySource = testTrophies{}
	s.Companion.PSN = func(context.Context, string) string { return "psn_user" }
	rec := login(t, s, "a@x.test", "password-A-1")
	w = authedRequest(s, rec, http.MethodGet, "/account/trophies", nil)
	for _, want := range []string{"NPWR12345_00", "psn_user", "90", "Gold"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("trophies page lacks %q", want)
		}
	}
}

func TestRankingsHub(t *testing.T) {
	s, _ := companionServer(t)
	w := getPath(s, "/rankings")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `href="/titles/mario-kart-wii/rankings"`) || !strings.Contains(body, "Mario Kart Wii") {
		t.Fatalf("hub: %d %s", w.Code, body)
	}
	if strings.Contains(body, "Halo 3") || strings.Contains(body, "Stardew") {
		t.Fatal("a game without rankings is on the hub")
	}
	// Games nobody has scored in are listed too, after the games with scores, with their board names.
	i, j := strings.Index(body, "Aaa Empty Racer"), strings.Index(body, "Mario Kart Wii")
	if i < 0 || j < 0 || i < j {
		t.Fatalf("empty game missing or sorted before a scored one (%d, %d)", i, j)
	}
	if n := strings.Count(body, "No scores yet"); n != 2 || !strings.Contains(body, "Time trial, Board 2") || !strings.Contains(body, "No Boards Yet") {
		t.Fatalf("empty states: %d\n%s", n, body)
	}
	w = getPath(s, "/titles/aaa-empty-racer/rankings")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No scores yet") || !strings.Contains(w.Body.String(), "Time trial") {
		t.Fatalf("empty title tab: %d %s", w.Code, w.Body.String())
	}
	if w = getPath(s, "/titles/no-boards-yet/rankings"); w.Code != 200 || !strings.Contains(w.Body.String(), "No scores yet") {
		t.Fatalf("title with no boards: %d", w.Code)
	}
}
