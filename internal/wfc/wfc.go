// Package wfc asks the Wii/DS server (nn-wfc) the one thing the website needs from it: which
// console a friend code belongs to. WFC has no login, so a Wii or DS is linked here, from a
// friend code the player reads off a game, and the core holds the link under the "wfc"
// namespace with the console-level user id as subject.
package wfc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ErrNotFound is a friend code no profile answers to.
var ErrNotFound = errors.New("wfc: no profile for that friend code")

type Client struct {
	BaseURL string // e.g. http://openpak-nn-wfc:20120
	Secret  string // nn-wfc's WFC_API_SECRET
	HTTP    *http.Client
}

// Profile is the console a friend code resolves to.
type Profile struct {
	ProfileID  uint32 `json:"pid"`
	UserID     uint64 `json:"user_id"` // the console; every game profile hangs off it
	GameCode   string `json:"gsbrcd"`
	FriendCode string `json:"fc"`
	Name       string `json:"name"`
}

func New(baseURL, secret string) *Client {
	if baseURL == "" {
		return nil // not configured: the page offers no Wii/DS link
	}
	return &Client{BaseURL: baseURL, Secret: secret, HTTP: &http.Client{Timeout: 3 * time.Second}}
}

// Resolve looks a typed friend code up. The server verifies the code against the profile's
// own game, so a typo is ErrNotFound rather than somebody else's console.
func (c *Client) Resolve(ctx context.Context, friendCode string) (Profile, error) {
	if c == nil {
		return Profile{}, errors.New("wfc: not configured")
	}
	q := url.Values{"fc": {friendCode}, "secret": {c.Secret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/profile?"+q.Encode(), nil)
	if err != nil {
		return Profile{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Profile{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return Profile{}, ErrNotFound
	default:
		return Profile{}, fmt.Errorf("wfc: %s", resp.Status)
	}
	var out Profile
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Profile{}, err
	}
	if out.UserID == 0 {
		return Profile{}, ErrNotFound
	}
	return out, nil
}
