// Package adapter asks the Switch adapter the things only it knows.
//
// The core records that an account has a console linked, and the subject of
// that link is the BAAS user id. It is not the friend code: codes are minted
// by the adapter, in its own format, and live in its projection store. The
// site used to format the hex subject as SW-xxxx-xxxx-xxxx, which produced a
// plausible-looking identifier no console would ever accept.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-nx-baas:20070
	Key     string
	HTTP    *http.Client
}

func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil // not configured: callers show no code rather than a wrong one
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 3 * time.Second}}
}

// FriendCode returns the console friend code for an account, or "" when there
// is none. An unreachable adapter gives the same answer as "no code": a page
// has to render either way, and a made-up code is worse than a missing one.
func (c *Client) FriendCode(ctx context.Context, accountID string) string {
	if c == nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/switch/friend-code?account_id=%s", c.BaseURL, url.QueryEscape(accountID)), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		FriendCode string `json:"friend_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.FriendCode
}
