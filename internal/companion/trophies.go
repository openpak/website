package companion

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// TrophyGame is one game's earned trophies for an account, as the PS3 trophy service keeps them:
// by communication id and zero-based trophy index, with the grade ("B", "S", "G", "P") and when.
type TrophyGame struct {
	CommunicationID string
	Trophies        []Trophy
}

// Trophy is one unlocked trophy.
type Trophy struct {
	ID       int
	EarnedAt time.Time
	Grade    string
}

// TrophySource reads an account's trophies from the PS3 trophy service.
type TrophySource interface {
	Trophies(ctx context.Context, accountID string) ([]TrophyGame, error)
}

// PSNLinker names the PSN online id linked to an account ("" when none).
type PSNLinker func(ctx context.Context, accountID string) string

// trophyPoints is the PS3 score of a grade.
var trophyPoints = map[string]int64{"B": 15, "S": 30, "G": 90, "P": 180}

// TrophyCard is the PS3 trophy page as the shared card: counts by grade, points, then one row per
// game. The service stores unlocks only: it has no trophy names, icons or per-game totals (those
// are in the game's TROPHY.TRP), so a game is its communication id and shows how many were earned.
func TrophyCard(games []TrophyGame, psn string) Card {
	c := Card{Linked: psn != "" || len(games) > 0, Heading: psn, Rows: []Row{}}
	counts := map[string]int64{}
	var total, points int64
	sort.SliceStable(games, func(i, j int) bool { return lastEarned(games[i]).After(lastEarned(games[j])) })
	for _, g := range games {
		for _, t := range g.Trophies {
			counts[t.Grade]++
			total++
			points += trophyPoints[t.Grade]
		}
		c.Rows = append(c.Rows, Row{Title: g.CommunicationID, Sub: lastEarned(g).UTC().Format("2006-01-02"),
			Value: fmt.Sprint(len(g.Trophies)), Percent: -1})
	}
	c.Stats = []Stat{
		{Key: "trophies.total", Value: fmt.Sprint(total), N: total},
		{Key: "trophies.points", Value: fmt.Sprint(points), N: points},
		{Key: "trophies.platinum", Value: fmt.Sprint(counts["P"]), N: counts["P"]},
		{Key: "trophies.gold", Value: fmt.Sprint(counts["G"]), N: counts["G"]},
		{Key: "trophies.silver", Value: fmt.Sprint(counts["S"]), N: counts["S"]},
		{Key: "trophies.bronze", Value: fmt.Sprint(counts["B"]), N: counts["B"]},
	}
	return c
}

func lastEarned(g TrophyGame) time.Time {
	var last time.Time
	for _, t := range g.Trophies {
		if t.EarnedAt.After(last) {
			last = t.EarnedAt
		}
	}
	return last
}

// Trophies reads the account's trophies and linked PSN id.
func (s *Service) Trophies(ctx context.Context, accountID string) (Card, error) {
	if s.TrophySource == nil {
		return Card{}, ErrUnavailable
	}
	games, err := s.TrophySource.Trophies(ctx, accountID)
	if err != nil {
		return Card{}, err
	}
	var psn string
	if s.PSN != nil {
		psn = s.PSN(ctx, accountID)
	}
	return TrophyCard(games, psn), nil
}
