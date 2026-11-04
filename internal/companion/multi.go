package companion

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Region is one regional server of a title that keeps its boards under several ids (a PS3 game
// has one NP communication id per region, so US, EU and JP scores are separate boards).
type Region struct {
	Label string
	Src   RankingSource
}

// Multi is a RankingSource over several regional sources. It does not merge rows: ranks are
// counted per server, so a merged table could not keep them honest. Every board of every region
// is listed on its own, labelled "<region>: <board>", and a region with no board is left out.
// Board ids are "<region index>:<board id>".
type Multi []Region

func (m Multi) regionBoards(ctx context.Context) (out []Board, errs []error) {
	for i, r := range m {
		bs, err := r.Src.Boards(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, b := range bs {
			b.ID = strconv.Itoa(i) + ":" + b.ID
			b.Label = r.Label + ": " + b.Label
			out = append(out, b)
		}
	}
	return out, errs
}

// Boards implements RankingSource. It fails only when every region failed.
func (m Multi) Boards(ctx context.Context) ([]Board, error) {
	out, errs := m.regionBoards(ctx)
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Available implements Availability: some region answers.
func (m Multi) Available(ctx context.Context) bool {
	_, errs := m.regionBoards(ctx)
	return len(errs) < len(m)
}

// Ranking implements RankingSource; board "" is the first board of the first region that has one.
func (m Multi) Ranking(ctx context.Context, board string, q RankQuery) (Ranking, error) {
	idx, id, ok := strings.Cut(board, ":")
	if board == "" {
		bs, err := m.Boards(ctx)
		if err != nil {
			return Ranking{}, err
		}
		if len(bs) == 0 {
			return Ranking{}, ErrNotFound
		}
		idx, id, ok = strings.Cut(bs[0].ID, ":")
	}
	i, err := strconv.Atoi(idx)
	if !ok || err != nil || i < 0 || i >= len(m) {
		return Ranking{}, ErrNotFound
	}
	// The Service fills in the board id, label and board list from Boards.
	return m[i].Src.Ranking(ctx, id, q)
}
