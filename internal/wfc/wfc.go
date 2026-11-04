// Package wfc asks the Wii/DS server (nn-wfc) the one thing the website needs from it: which
// console a friend code belongs to. WFC has no login, so a Wii or DS is linked here, from a
// friend code the player reads off a game, and the core holds the link under the "wfc"
// namespace with the console-level user id as subject.
package wfc

import (
	"bytes"
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

// ErrPairingCode is a pairing code that is unknown, expired or already used.
var ErrPairingCode = errors.New("wfc: pairing code unknown, expired or used")

// Pairing is the console a live pairing code belongs to.
type Pairing struct {
	UserID    uint64    `json:"user_id"`
	GameCode  string    `json:"gsbrcd"`
	Name      string    `json:"name"` // the in-game name of the profile that signed in
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) post(ctx context.Context, path string, body map[string]any, out any) error {
	if c == nil {
		return errors.New("wfc: not configured")
	}
	body["secret"] = c.Secret
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
	case http.StatusNotFound:
		return ErrPairingCode
	default:
		return fmt.Errorf("wfc: %s", resp.Status)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// PairLookup says which console a code a Wii or DS game showed belongs to, without spending it.
func (c *Client) PairLookup(ctx context.Context, code string) (Pairing, error) {
	var p Pairing
	if err := c.post(ctx, "/api/pair/lookup", map[string]any{"code": code}, &p); err != nil {
		return Pairing{}, err
	}
	if p.UserID == 0 {
		return Pairing{}, ErrPairingCode
	}
	return p, nil
}

// PairRedeem spends the code for good, once the core link exists.
func (c *Client) PairRedeem(ctx context.Context, code string) (uint64, error) {
	var p Pairing
	if err := c.post(ctx, "/api/pair/redeem", map[string]any{"code": code}, &p); err != nil {
		return 0, err
	}
	return p.UserID, nil
}

// PairForget tells the Wii/DS server a console was unlinked, so its next login is shown a
// code again. Best effort: the friend-code path works regardless.
func (c *Client) PairForget(ctx context.Context, userID uint64) error {
	return c.post(ctx, "/api/pair/forget", map[string]any{"user_id": userID}, nil)
}
