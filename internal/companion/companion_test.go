package companion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type fakeRank struct{}

func (fakeRank) Boards(context.Context) ([]Board, error) {
	return []Board{{ID: "a", Label: "Course A"}, {ID: "b", Label: "Course B"}}, nil
}
func (fakeRank) Ranking(_ context.Context, board string, q RankQuery) (Ranking, error) {
	return Ranking{Entries: []Entry{{Name: "x-" + board, Value: 1, Display: "0:01.000", Me: q.AccountID == "me"}}}, nil
}

type fakeHistory map[string][]core.PlayEntry

func (f fakeHistory) PlayHistory(_ context.Context, _, ns string, _ int) ([]core.PlayEntry, error) {
	if ns == "wfc" {
		return nil, errors.New("down") // one network failing costs only its rows
	}
	return f[ns], nil
}

func seeded(t *testing.T) (*Service, *store.Memory) {
	t.Helper()
	db := store.NewMemory()
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Mario Kart Wii", Console: "Wii", Status: "beta"},
		{Kind: "title", Name: "Minecraft", Console: "Switch", Status: "live", TitleID: "0100D7A01B7A2000"},
		{Kind: "title", Name: "Minecraft", Console: "Wii U", Status: "live"},
		{Kind: "title", Name: "Pokémon X", Console: "3DS", Status: "live"},
	})
	return New(db), db
}

func TestSlugs(t *testing.T) {
	svc, db := seeded(t)
	reqs, _ := db.ListRequests(context.Background(), "title", "")
	got := map[string]bool{}
	for _, s := range Slugs(reqs) {
		got[s] = true
	}
	for _, want := range []string{"mario-kart-wii", "minecraft-switch", "minecraft-wii-u", "pokemon-x"} {
		if !got[want] {
			t.Errorf("slug %q missing from %v", want, got)
		}
	}
	_ = svc
}

func TestResolveOnlyTitlesWithFeatures(t *testing.T) {
	svc, _ := seeded(t)
	svc.Register(Source{Names: []string{"mario kart wii"}, Rankings: fakeRank{}})
	ctx := context.Background()
	title, err := svc.Resolve(ctx, "mario-kart-wii")
	if err != nil || !title.Has(FeatureRankings) || title.Has(FeatureStats) {
		t.Fatalf("resolve: %+v %v", title, err)
	}
	if _, err := svc.Resolve(ctx, "pokemon-x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a title with no feature has a page: %v", err)
	}
	if _, err := svc.Resolve(ctx, "nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown slug: %v", err)
	}
	with, _ := svc.WithFeatures(ctx)
	if len(with) != 1 {
		t.Fatalf("WithFeatures = %d", len(with))
	}
}

func TestRankingBoards(t *testing.T) {
	svc, _ := seeded(t)
	svc.Register(Source{Names: []string{"Mario Kart Wii"}, Console: "Wii", Rankings: fakeRank{}})
	ctx := context.Background()
	title, _ := svc.Resolve(ctx, "mario-kart-wii")
	r, err := svc.Ranking(ctx, title, "", RankQuery{AccountID: "me"})
	if err != nil || r.Board != "a" || r.Label != "Course A" || len(r.Boards) != 2 || r.Entries[0].Rank != 1 || !r.Entries[0].Me {
		t.Fatalf("default board: %+v %v", r, err)
	}
	if r, _ = svc.Ranking(ctx, title, "b", RankQuery{}); r.Entries[0].Name != "x-b" {
		t.Fatalf("board b: %+v", r)
	}
	if _, err = svc.Ranking(ctx, title, "zzz", RankQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown board: %v", err)
	}
	if _, err = svc.Stats(ctx, title, "me"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stats on a title without them: %v", err)
	}
}

func TestActivity(t *testing.T) {
	svc, _ := seeded(t)
	if _, err := svc.Activity(context.Background(), "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no history backend: %v", err)
	}
	now := time.Now().UTC()
	svc.History = fakeHistory{
		"switch": {{Namespace: "switch", TitleID: "0100d7a01b7a2000", Sessions: 3, Total: 2 * time.Hour, First: now.Add(-48 * time.Hour), Last: now.Add(-time.Hour)}},
		"wiiu":   {{Namespace: "wiiu", TitleID: "0005000012345600", Sessions: 1, Total: 10 * time.Minute, First: now.Add(-24 * time.Hour), Last: now}},
	}
	a, err := svc.Activity(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Titles) != 2 || a.TotalSeconds != 7800 || a.Sessions != 4 {
		t.Fatalf("activity totals: %+v", a)
	}
	if a.Titles[0].Network != "wiiu" || a.Titles[1].Title != "Minecraft" {
		t.Fatalf("order or names: %+v", a.Titles)
	}
	if len(a.Networks) != 2 || a.Networks[0] != "wiiu" {
		t.Fatalf("networks: %v", a.Networks)
	}
}

func TestSourceMatchesTitleID(t *testing.T) {
	s := Source{TitleIDs: []string{"4D5307E6"}, Console: "Xbox 360"}
	if !s.matches(store.Request{Name: "Anything", Console: "Xbox 360", TitleIDs: []string{"4d5307e6"}}) {
		t.Error("id should match")
	}
	if s.matches(store.Request{Name: "Halo 3", Console: "Xbox 360", TitleIDs: []string{"4D53085B"}}) {
		t.Error("another id must not match")
	}
	if s.matches(store.Request{Console: "PS3", TitleIDs: []string{"4D5307E6"}}) {
		t.Error("console must agree")
	}
	if !(Source{Names: []string{"Halo 3"}, Console: "Xbox 360"}).matches(store.Request{Name: "halo 3", Console: "Xbox 360"}) {
		t.Error("name still matches")
	}
}
