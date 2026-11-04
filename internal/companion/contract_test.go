package companion

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/openpak/website/rankingsapi"
)

type srv struct{ boards []rankingsapi.Board }

func (s srv) Boards(context.Context) ([]rankingsapi.Board, error) { return s.boards, nil }
func (srv) Rows(_ context.Context, _ string, q rankingsapi.Query) (rankingsapi.Page, error) {
	return rankingsapi.Window(rankingsapi.KindTime, []rankingsapi.Row{{PID: "10", Score: 83456}, {PID: "11", Score: 90001}, {PID: "12", Score: 99000}}, q), nil
}

func TestContractResolvesPeople(t *testing.T) {
	ts := httptest.NewServer(rankingsapi.Handler("wiiu", srv{boards: []rankingsapi.Board{{ID: "5", Name: "Coconut Mall", Kind: "time", Order: "asc"}}}))
	defer ts.Close()
	people := &People{
		AccountByPID: func(_ context.Context, network, pid string) (string, error) {
			if network != "wiiu" {
				t.Errorf("network %q", network)
			}
			if pid == "11" {
				return "", errors.New("unlinked")
			}
			return "acct-" + pid, nil
		},
		PIDByAccount: func(_ context.Context, _, acct string) (string, error) { return acct[len("acct-"):], nil },
		Name:         func(_ context.Context, acct string) (string, error) { return "Name " + acct, nil },
		Avatar:       func(_ context.Context, acct string) string { return "/media/avatars/" + acct + "/128" },
	}
	c := NewContract(ts.URL, people)
	if !c.Available(context.Background()) {
		t.Fatal("not available")
	}
	r, err := c.Ranking(context.Background(), "5", RankQuery{AccountID: "acct-12", Limit: 10})
	if err != nil || len(r.Entries) != 3 || r.Total != 3 {
		t.Fatalf("ranking: %+v %v", r, err)
	}
	if e := r.Entries[0]; e.Name != "Name acct-10" || e.Display != "1:23.456" || e.Avatar == "" || e.AccountID != "acct-10" {
		t.Fatalf("row 0: %+v", e)
	}
	if e := r.Entries[1]; e.Name != "Player" || e.AccountID != "" {
		t.Fatalf("unlinked player: %+v", e)
	}
	if !r.Entries[2].Me || len(r.Around) == 0 {
		t.Fatalf("me/around: %+v %+v", r.Entries[2], r.Around)
	}
}

func TestContractWithoutBoardsStillHasATab(t *testing.T) {
	ts := httptest.NewServer(rankingsapi.Handler("wiiu", srv{}))
	defer ts.Close()
	c := NewContract(ts.URL, nil)
	if !c.Available(context.Background()) {
		t.Fatal("a reachable server with no boards has no Rankings tab")
	}
	if bs, err := c.Boards(context.Background()); err != nil || len(bs) != 0 {
		t.Fatalf("boards: %v %v", bs, err)
	}
	if NewContract("http://127.0.0.1:1", nil).Available(context.Background()) {
		t.Fatal("a down server has a Rankings tab")
	}
}

func TestBoardLabelNeverShowsAnId(t *testing.T) {
	for _, c := range []struct{ name, id, want string }{
		{"Distance", "0", "Distance"}, {"", "7", "Board 2"}, {"Board 7", "7", "Board 7"},
		{"Board c9f1a", "c9f1a", "Board 2"}, {"c9f1a", "c9f1a", "Board 2"},
	} {
		if got := boardLabel(c.name, c.id, 2); got != c.want {
			t.Errorf("boardLabel(%q,%q) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
}
