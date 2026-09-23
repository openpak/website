package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Gathering is the game session an account is playing in, as the title
// server that owns it reports it.
type Gathering struct {
	Title        string        `json:"title"`
	GID          uint32        `json:"gid"`
	Participants []Participant `json:"participants"`
}

// Participant is one console in a gathering. AccountID is empty for a login
// the title server could not tie to an account.
type Participant struct {
	PID       uint64 `json:"pid"`
	AccountID string `json:"account_id"`
	Nickname  string `json:"nickname"`
}

// Gatherings asks the title servers which gathering an account is in. Each
// URL is a title server's internal base (switch-nex answers
// GET /internal/voice/gathering on any of its titles' healthz ports, for the
// whole process); they are asked in order and the first that knows wins.
type Gatherings struct {
	urls   []string
	key    string
	client *http.Client
}

// NewGatherings returns nil when no title server is configured, and the app
// then only has code rooms.
func NewGatherings(urls []string, key string) *Gatherings {
	var clean []string
	for _, u := range urls {
		if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return &Gatherings{urls: clean, key: key, client: &http.Client{Timeout: 3 * time.Second}}
}

// Of returns the account's gathering, or nil when no title server has it in
// one. An error means a title server could not answer; the others were still
// asked.
func (g *Gatherings) Of(ctx context.Context, accountID string) (*Gathering, error) {
	if g == nil {
		return nil, nil
	}
	var lastErr error
	for _, base := range g.urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/internal/voice/gathering?account_id="+url.QueryEscape(accountID), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Internal-Key", g.key)
		resp, err := g.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var out Gathering
		switch resp.StatusCode {
		case http.StatusOK:
			err = json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			if err != nil {
				return nil, err
			}
			return &out, nil
		case http.StatusNotFound:
			resp.Body.Close()
		default:
			resp.Body.Close()
			lastErr = fmt.Errorf("%s: %s", base, resp.Status)
		}
	}
	return nil, lastErr
}
