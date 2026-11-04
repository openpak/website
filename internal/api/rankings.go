package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/openpak/website/internal/companion"
)

// The public rankings API: the rankings contract every game server implements
// (docs/rankings-contract.md), with player names and pictures resolved through the account core.
//
//	GET /api/v1/rankings                         the games that have rankings
//	GET /api/v1/rankings/{slug}                  one game's boards
//	GET /api/v1/rankings/{slug}/{board}          one board: ?limit=&offset=&around=<account id>
//
// Read-only, public, cached for 30 seconds. Avatar is a site-relative URL.
func (s *Server) registerRankings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/rankings", s.handleRankingsGames)
	mux.HandleFunc("GET /api/v1/rankings/{slug}", s.handleRankingsBoards)
	mux.HandleFunc("GET /api/v1/rankings/{slug}/{board}", s.handleRankingsBoard)
}

type rankedGame struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Console string `json:"console"`
	Boards  int    `json:"boards"`
	// HasScores is false while no board of the game has a score; ScoredBoards counts those that do,
	// and BoardNames names every board (so an empty game can still say what it ranks).
	HasScores    bool     `json:"has_scores"`
	ScoredBoards int      `json:"scored_boards"`
	BoardNames   []string `json:"board_names"`
}

type rankBoard struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"`
	Order  string `json:"order,omitempty"`
	Unit   string `json:"unit,omitempty"`
	Season string `json:"season,omitempty"`
	// HasScores is false for a board nobody has a score on yet.
	HasScores bool `json:"has_scores"`
}

type rankRow struct {
	Rank      int       `json:"rank"`
	AccountID string    `json:"account_id,omitempty"`
	Name      string    `json:"name"`
	Avatar    string    `json:"avatar,omitempty"`
	Score     int64     `json:"score"`
	Display   string    `json:"display"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

func rankRows(es []companion.Entry) []rankRow {
	out := make([]rankRow, 0, len(es))
	for _, e := range es {
		out = append(out, rankRow{Rank: e.Rank, AccountID: e.AccountID, Name: e.Name, Avatar: e.Avatar,
			Score: e.Value, Display: e.Display, UpdatedAt: e.Updated})
	}
	return out
}

func rankBoards(bs []companion.Board) []rankBoard {
	out := make([]rankBoard, 0, len(bs))
	for _, b := range bs {
		out = append(out, rankBoard{ID: b.ID, Name: b.Label, Kind: b.Kind, Order: b.Order, Unit: b.Unit, Season: b.Season, HasScores: !b.NoScores})
	}
	return out
}

func rankingsHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func (s *Server) rankedTitle(w http.ResponseWriter, r *http.Request) (companion.Title, bool) {
	t, ok := s.companionTitle(w, r)
	if ok && !t.Has(companion.FeatureRankings) {
		writeError(w, http.StatusNotFound, "not found")
		return t, false
	}
	return t, ok
}

func (s *Server) handleRankingsGames(w http.ResponseWriter, r *http.Request) {
	if s.Companion == nil {
		writeError(w, http.StatusServiceUnavailable, "rankings are not available on this deployment")
		return
	}
	titles, err := s.Companion.Ranked(r.Context())
	if err != nil {
		s.companionError(w, "rankings", err)
		return
	}
	games := []rankedGame{}
	for _, t := range titles {
		bs, err := s.Companion.BoardsOf(r.Context(), t)
		if err != nil {
			continue
		}
		g := rankedGame{Slug: t.Slug, Name: t.Name, Console: t.Console, Boards: len(bs), BoardNames: []string{}}
		for _, b := range bs {
			g.BoardNames = append(g.BoardNames, b.Label)
			if !b.NoScores {
				g.ScoredBoards++
			}
		}
		g.HasScores = g.ScoredBoards > 0
		games = append(games, g)
	}
	// Games with scores first within each console.
	sort.SliceStable(games, func(i, j int) bool {
		if games[i].Console != games[j].Console {
			return games[i].Console < games[j].Console
		}
		return games[i].HasScores && !games[j].HasScores
	})
	rankingsHeaders(w)
	writeJSON(w, http.StatusOK, map[string]any{"games": games})
}

func (s *Server) handleRankingsBoards(w http.ResponseWriter, r *http.Request) {
	t, ok := s.rankedTitle(w, r)
	if !ok {
		return
	}
	bs, err := s.Companion.BoardsOf(r.Context(), t)
	if err != nil {
		s.companionError(w, "rankings", err)
		return
	}
	rankingsHeaders(w)
	writeJSON(w, http.StatusOK, map[string]any{"slug": t.Slug, "name": t.Name, "console": t.Console, "boards": rankBoards(bs)})
}

func (s *Server) handleRankingsBoard(w http.ResponseWriter, r *http.Request) {
	t, ok := s.rankedTitle(w, r)
	if !ok {
		return
	}
	q := companion.QueryOf(r.URL.Query(), r.URL.Query().Get("around"))
	if q.Limit == 0 {
		q.Limit = 25
	}
	out, err := s.Companion.Ranking(r.Context(), t, r.PathValue("board"), q)
	if err != nil {
		s.companionError(w, "rankings", err)
		return
	}
	var board rankBoard
	for _, b := range rankBoards(out.Boards) {
		if b.ID == out.Board {
			board = b
		}
	}
	rankingsHeaders(w)
	writeJSON(w, http.StatusOK, map[string]any{"slug": t.Slug, "board": board, "total": out.Total,
		"offset": out.Offset, "limit": out.Limit, "rows": rankRows(out.Entries), "around": rankRows(out.Around)})
}
