package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type apiBoards struct{}

func (apiBoards) Boards(context.Context) ([]companion.Board, error) {
	return []companion.Board{{ID: "all", Label: "All"}}, nil
}
func (apiBoards) Ranking(_ context.Context, _, acct string) (companion.Ranking, error) {
	return companion.Ranking{Entries: []companion.Entry{{Name: "Ana", Value: 83456, Display: "1:23.456", Me: acct != ""}}}, nil
}

type apiStats struct{}

func (apiStats) Stats(_ context.Context, acct string) (companion.Card, error) {
	return companion.Card{Linked: true, Stats: []companion.Stat{{Key: "k", Value: acct, N: 1}}, Rows: []companion.Row{}}, nil
}

type apiHistory struct{}

func (apiHistory) PlayHistory(_ context.Context, _, ns string, _ int) ([]core.PlayEntry, error) {
	if ns != "switch" {
		return nil, nil
	}
	return []core.PlayEntry{{Namespace: "switch", TitleID: "0100E65002BB8000", Sessions: 1, Total: time.Hour, Last: time.Now()}}, nil
}

func TestCompanionAPI(t *testing.T) {
	s, _, db := newTestAPI(t)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Mario Kart Wii", Console: "Wii", Status: "beta"},
		{Kind: "title", Name: "Halo 3", Console: "Xbox 360", Status: "beta"},
		{Kind: "title", Name: "Stardew Valley", Console: "Switch", Status: "live", TitleID: "0100E65002BB8000", TitleIDs: []string{"0100E65002BB8000"}},
	})
	if rec := do(t, s, http.MethodGet, "/api/v1/companion/titles", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", rec.Code)
	}
	c := companion.New(db)
	c.History = apiHistory{}
	c.Register(companion.Source{Names: []string{"Mario Kart Wii"}, Rankings: apiBoards{}, Stats: apiStats{}})
	s.Companion = c

	rec := do(t, s, http.MethodGet, "/api/v1/companion/titles", "", nil)
	var list struct {
		Titles []companionTitle `json:"titles"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Titles) != 1 || list.Titles[0].Slug != "mario-kart-wii" || len(list.Titles[0].Features) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/halo-3", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("title without features: %d", rec.Code)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/mario-kart-wii/rankings", "", nil)
	var rk companion.Ranking
	_ = json.Unmarshal(rec.Body.Bytes(), &rk)
	if rec.Code != 200 || rk.Board != "all" || len(rk.Entries) != 1 || rk.Entries[0].Display != "1:23.456" || rk.Entries[0].Me {
		t.Fatalf("rankings: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/mario-kart-wii/rankings?board=x", "", nil); rec.Code != 404 {
		t.Errorf("unknown board: %d", rec.Code)
	}

	// Stats and activity need a token.
	if rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/mario-kart-wii/stats", "", nil); rec.Code != 401 {
		t.Errorf("stats without a token: %d", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)
	if rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/mario-kart-wii/stats", minted.Token, nil); rec.Code != 200 {
		t.Errorf("stats: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/v1/companion/titles/mario-kart-wii/rankings", minted.Token, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rk)
	if !rk.Entries[0].Me {
		t.Errorf("the bearer's rows are not marked: %s", rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/activity", minted.Token, nil)
	var act companion.Activity
	_ = json.Unmarshal(rec.Body.Bytes(), &act)
	if rec.Code != 200 || len(act.Titles) != 1 || act.Titles[0].Title != "Stardew Valley" || act.TotalSeconds != 3600 {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodGet, "/api/v1/me/activity", "", nil); rec.Code != 401 {
		t.Errorf("activity without a token: %d", rec.Code)
	}
}
