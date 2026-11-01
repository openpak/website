package companion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MKWRace reads the Mario Kart Wii time-trial leaderboard through wfc-race's own JSON endpoint
// (GET /api/mkw/leaderboard?region=&course=, header X-WFC-Internal-Key).
type MKWRace struct {
	BaseURL, Key string
	HTTP         *http.Client
}

// NewMKWRace returns nil when the race service URL is not set.
func NewMKWRace(baseURL, key string) *MKWRace {
	if baseURL == "" {
		return nil
	}
	return &MKWRace{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// mkwCourseByID names the leaderboard course ids in wfc-common order (0x00 Mario Circuit ... 0x1F GBA Shy Guy Beach).
var mkwCourseByID = []string{
	"Mario Circuit", "Moo Moo Meadows", "Mushroom Gorge", "Grumble Volcano", "Toad's Factory", "Coconut Mall",
	"DK Summit", "Wario's Gold Mine", "Luigi Circuit", "Daisy Circuit", "Moonview Highway", "Maple Treeway",
	"Bowser's Castle", "Rainbow Road", "Dry Dry Ruins", "Koopa Cape", "GCN Peach Beach", "GCN Mario Circuit",
	"GCN Waluigi Stadium", "GCN DK Mountain", "DS Yoshi Falls", "DS Desert Hills", "DS Peach Gardens",
	"DS Delfino Square", "SNES Mario Circuit 3", "SNES Ghost Valley 2", "N64 Mario Raceway", "N64 Sherbet Land",
	"N64 Bowser's Castle", "N64 DK's Jungle Parkway", "GBA Bowser Castle 3", "GBA Shy Guy Beach",
}

// Boards implements RankingSource: one board per course, worldwide.
func (m *MKWRace) Boards(context.Context) ([]Board, error) {
	boards := make([]Board, 0, len(mkwCourseByID))
	for id, name := range mkwCourseByID {
		boards = append(boards, Board{ID: fmt.Sprint(id), Label: name})
	}
	return boards, nil
}

// Ranking implements RankingSource.
func (m *MKWRace) Ranking(ctx context.Context, board, _ string) (Ranking, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.BaseURL+"/api/mkw/leaderboard?region=0&course="+url.QueryEscape(board), nil)
	if err != nil {
		return Ranking{}, err
	}
	req.Header.Set("X-WFC-Internal-Key", m.Key)
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return Ranking{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Ranking{}, fmt.Errorf("wfc-race: %s", resp.Status)
	}
	var out struct {
		Entries []struct {
			Rank   int    `json:"rank"`
			TimeMS int64  `json:"time_ms"`
			Name   string `json:"name"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Ranking{}, err
	}
	r := Ranking{Entries: []Entry{}, Updated: time.Now().UTC()}
	for _, e := range out.Entries {
		name := e.Name
		if name == "" {
			name = "Player"
		}
		r.Entries = append(r.Entries, Entry{Rank: e.Rank, Name: name, Value: e.TimeMS, Display: MKWTime(e.TimeMS)})
	}
	return r, nil
}

// MKWTime is a time in milliseconds as m:ss.mmm.
func MKWTime(ms int64) string {
	return fmt.Sprintf("%d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}
