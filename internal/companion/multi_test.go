package companion

import (
	"context"
	"errors"
	"testing"
)

type fakeSrc struct {
	boards []Board
	err    error
	rows   map[string]string
}

func (f fakeSrc) Boards(context.Context) ([]Board, error) { return f.boards, f.err }
func (f fakeSrc) Ranking(_ context.Context, b string, _ RankQuery) (Ranking, error) {
	return Ranking{Entries: []Entry{{Rank: 1, Name: f.rows[b]}}}, nil
}

func TestMultiRegions(t *testing.T) {
	m := Multi{
		{"US", fakeSrc{boards: []Board{{ID: "0", Label: "Score"}}, rows: map[string]string{"0": "us-player"}}},
		{"EU", fakeSrc{boards: nil}}, // no scores yet: left out
		{"JP", fakeSrc{boards: []Board{{ID: "0", Label: "Score"}, {ID: "1", Label: "Time"}}, rows: map[string]string{"0": "jp0", "1": "jp1"}}},
	}
	ctx := context.Background()
	bs, err := m.Boards(ctx)
	if err != nil || len(bs) != 3 {
		t.Fatalf("boards %v %v", bs, err)
	}
	if bs[0].ID != "0:0" || bs[0].Label != "US: Score" || bs[2].ID != "2:1" || bs[2].Label != "JP: Time" {
		t.Errorf("labels/ids wrong: %+v", bs)
	}
	if !m.Available(ctx) {
		t.Error("not available")
	}
	r, err := m.Ranking(ctx, "2:1", RankQuery{})
	if err != nil || r.Entries[0].Name != "jp1" {
		t.Errorf("JP board: %+v %v", r, err)
	}
	if r, _ := m.Ranking(ctx, "", RankQuery{}); r.Entries[0].Name != "us-player" {
		t.Errorf("default board: %+v", r)
	}
	for _, bad := range []string{"9:0", "x", "1:"} {
		if bad == "1:" {
			continue // region 1 exists; its board id is not checked here, the Service checks ids against Boards
		}
		if _, err := m.Ranking(ctx, bad, RankQuery{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	down := Multi{{"US", fakeSrc{err: errors.New("down")}}, {"EU", fakeSrc{boards: []Board{{ID: "a", Label: "A"}}}}}
	if bs, err := down.Boards(ctx); err != nil || len(bs) != 1 {
		t.Errorf("one region down: %v %v", bs, err)
	}
	if _, err := (Multi{{"US", fakeSrc{err: errors.New("down")}}}).Boards(ctx); err == nil {
		t.Error("all regions down should fail")
	}
}
