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

// PGL reads the Pokémon Global Link service's public JSON API (common/pokemon-global-link,
// docs/api.md): open GTS listings and the Global Showcase usage rankings. It is a title's
// companion data, not a database read.
type PGL struct {
	BaseURL string
	HTTP    *http.Client
}

// NewPGL returns nil when the service URL is not set.
func NewPGL(baseURL string) *PGL {
	if baseURL == "" {
		return nil
	}
	return &PGL{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Second}}
}

func (p *PGL) get(ctx context.Context, path string, q url.Values, out any) error {
	u := p.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pokemon-global-link: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// IsPokemonHandheld matches the DS and 3DS Pokémon games the Global Link serves.
func IsPokemonHandheld(name, console string) bool {
	n := strings.ToLower(name)
	return (strings.HasPrefix(n, "pokémon") || strings.HasPrefix(n, "pokemon")) && (console == "DS" || console == "3DS")
}

const pglAll = "all"

// Boards implements RankingSource: one board for everything, then one per generation, format and
// season the service has battles for.
func (p *PGL) Boards(ctx context.Context) ([]Board, error) {
	var f struct {
		Formats []struct {
			Generation int      `json:"generation"`
			Format     string   `json:"format"`
			Seasons    []string `json:"seasons"`
			Battles    int      `json:"battles"`
		} `json:"formats"`
	}
	if err := p.get(ctx, "/api/v1/battle/formats", nil, &f); err != nil {
		return nil, err
	}
	if len(f.Formats) == 0 {
		return nil, nil
	}
	boards := []Board{{ID: pglAll, Label: "Global Showcase"}}
	for _, ft := range f.Formats {
		for _, s := range ft.Seasons {
			boards = append(boards, Board{ID: fmt.Sprintf("%d:%s:%s", ft.Generation, ft.Format, s),
				Label: fmt.Sprintf("Gen %d %s · %s", ft.Generation, ft.Format, s)})
		}
	}
	return boards, nil
}

// Ranking implements RankingSource: the Pokémon usage ranking of a board. The service reports
// PKHeX species ids and no names, so a row is "Pokémon #25"; the usage figures are what the
// page is for.
func (p *PGL) Ranking(ctx context.Context, board, _ string) (Ranking, error) {
	q := url.Values{"limit": {"30"}}
	if parts := strings.Split(board, ":"); board != pglAll && len(parts) == 3 {
		q.Set("generation", parts[0])
		q.Set("format", parts[1])
		q.Set("season", parts[2])
	}
	var s struct {
		Battles int `json:"battles"`
		Pokemon []struct {
			Species int     `json:"species"`
			Uses    int     `json:"uses"`
			Usage   float64 `json:"usage_pct"`
			Wins    int     `json:"wins"`
			Losses  int     `json:"losses"`
			WinRate float64 `json:"win_rate"`
		} `json:"pokemon"`
	}
	if err := p.get(ctx, "/api/v1/showcase", q, &s); err != nil {
		return Ranking{}, err
	}
	out := Ranking{Entries: []Entry{}}
	for _, m := range s.Pokemon {
		out.Entries = append(out.Entries, Entry{Name: fmt.Sprintf("Pokémon #%d", m.Species), Value: int64(m.Uses),
			Display: fmt.Sprintf("%.0f%%", m.Usage*100), Note: fmt.Sprintf("%d uses · %.0f%% wins", m.Uses, m.WinRate*100)})
	}
	return out, nil
}

// Listings implements ListingsSource: the open GTS listings, newest first.
func (p *PGL) Listings(ctx context.Context) (Card, error) {
	var l struct {
		Total    int `json:"total"`
		Listings []struct {
			ID         int64  `json:"id"`
			CreatedAt  int64  `json:"created_at"`
			Generation int    `json:"generation"`
			Species    int    `json:"species"`
			Level      int    `json:"level"`
			Shiny      bool   `json:"shiny"`
			Nickname   string `json:"nickname"`
			Request    struct {
				Species  int `json:"species"`
				LevelMin int `json:"level_min"`
				LevelMax int `json:"level_max"`
			} `json:"request"`
		} `json:"listings"`
	}
	if err := p.get(ctx, "/api/v1/gts/listings", url.Values{"limit": {"50"}}, &l); err != nil {
		return Card{}, err
	}
	c := Card{Linked: true, Rows: []Row{}, Stats: []Stat{{Key: "companion.listings.open", Value: fmt.Sprint(l.Total), N: int64(l.Total)}}}
	for _, x := range l.Listings {
		want := "any Pokémon"
		if x.Request.Species > 0 {
			want = fmt.Sprintf("Pokémon #%d", x.Request.Species)
		}
		title := fmt.Sprintf("Pokémon #%d · Lv. %d", x.Species, x.Level)
		if x.Shiny {
			title += " ★"
		}
		c.Rows = append(c.Rows, Row{Title: title,
			Sub:   fmt.Sprintf("Gen %d · wants %s (Lv. %d-%d) · %s", x.Generation, want, x.Request.LevelMin, x.Request.LevelMax, time.Unix(x.CreatedAt, 0).UTC().Format("2006-01-02")),
			Value: x.Nickname, Percent: -1})
	}
	return c, nil
}
