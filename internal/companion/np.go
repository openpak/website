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

// NPTrophy is the PS3 trophy service's private API (np-trophy: GET /v1/users/{account}/trophies,
// bearer NP_TROPHY_KEY), read-only here.
type NPTrophy struct {
	BaseURL, Key string
	HTTP         *http.Client
}

// NewNPTrophy returns nil when NP_TROPHY_URL is not set.
func NewNPTrophy(baseURL, key string) *NPTrophy {
	if baseURL == "" {
		return nil
	}
	return &NPTrophy{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Trophies implements TrophySource.
func (n *NPTrophy) Trophies(ctx context.Context, accountID string) ([]TrophyGame, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.BaseURL+"/v1/users/"+url.PathEscape(strings.ToLower(accountID))+"/trophies", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+n.Key)
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("np-trophy: %s", resp.Status)
	}
	var out struct {
		Games []struct {
			CommunicationID string `json:"communication_id"`
			Trophies        []struct {
				ID       int    `json:"id"`
				EarnedAt int64  `json:"earned_at"`
				Grade    string `json:"grade"`
			} `json:"trophies"`
		} `json:"games"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	games := make([]TrophyGame, 0, len(out.Games))
	for _, g := range out.Games {
		tg := TrophyGame{CommunicationID: g.CommunicationID}
		for _, t := range g.Trophies {
			tg.Trophies = append(tg.Trophies, Trophy{ID: t.ID, EarnedAt: time.Unix(t.EarnedAt, 0).UTC(), Grade: t.Grade})
		}
		games = append(games, tg)
	}
	return games, nil
}
