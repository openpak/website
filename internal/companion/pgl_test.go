package companion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func pglServer(t *testing.T) *PGL {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/battle/formats", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"formats":[{"generation":6,"format":"single","seasons":["2026-11"],"battles":3}]}`))
	})
	mux.HandleFunc("/api/v1/showcase", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("generation") != "6" && r.URL.Query().Get("generation") != "" {
			t.Errorf("query: %v", r.URL.RawQuery)
		}
		w.Write([]byte(`{"battles":3,"pokemon":[{"species":25,"uses":2,"usage_pct":0.67,"wins":1,"losses":1,"win_rate":0.5}]}`))
	})
	mux.HandleFunc("/api/v1/gts/listings", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total":1,"listings":[{"id":7,"created_at":1793550000,"generation":6,"species":25,"level":30,"shiny":true,"nickname":"PIKA","request":{"species":0,"level_min":0,"level_max":100}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewPGL(srv.URL)
}

func TestPGLSource(t *testing.T) {
	p := pglServer(t)
	ctx := context.Background()
	svc, _ := seeded(t)
	svc.Register(Source{Match: IsPokemonHandheld, Rankings: p, Listings: p})
	title, err := svc.Resolve(ctx, "pokemon-x")
	if err != nil || !title.Has(FeatureRankings) || !title.Has(FeatureListings) {
		t.Fatalf("resolve: %+v %v", title, err)
	}
	r, err := svc.Ranking(ctx, title, "6:single:2026-11", RankQuery{})
	if err != nil || len(r.Boards) != 2 || r.Entries[0].Name != "Pokémon #25" || r.Entries[0].Display != "67%" {
		t.Fatalf("ranking: %+v %v", r, err)
	}
	card, err := svc.Listings(ctx, title)
	if err != nil || len(card.Rows) != 1 || card.Stats[0].Value != "1" || card.Rows[0].Value != "PIKA" {
		t.Fatalf("listings: %+v %v", card, err)
	}
}

func TestEmptyBoards(t *testing.T) {
	svc, _ := seeded(t)
	svc.Register(Source{Names: []string{"Mario Kart Wii"}, Rankings: noBoards{}})
	title, _ := svc.Resolve(context.Background(), "mario-kart-wii")
	r, err := svc.Ranking(context.Background(), title, "", RankQuery{})
	if err != nil || len(r.Entries) != 0 {
		t.Fatalf("a source with no boards is %v %+v", err, r)
	}
}

type noBoards struct{}

func (noBoards) Boards(context.Context) ([]Board, error)                     { return nil, nil }
func (noBoards) Ranking(context.Context, string, RankQuery) (Ranking, error) { return Ranking{}, nil }

func TestTrophyCard(t *testing.T) {
	now := time.Now()
	c := TrophyCard([]TrophyGame{{CommunicationID: "NPWR1_00", Trophies: []Trophy{{ID: 0, Grade: "B", EarnedAt: now}, {ID: 1, Grade: "P", EarnedAt: now}}}}, "someone")
	if !c.Linked || c.Heading != "someone" || c.Stats[0].N != 2 || c.Stats[1].N != 195 || len(c.Rows) != 1 || c.Rows[0].Value != "2" {
		t.Fatalf("card: %+v", c)
	}
	if e := TrophyCard(nil, ""); e.Linked {
		t.Fatalf("an empty account is linked: %+v", e)
	}
}
