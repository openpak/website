// Package xbl talks to the xbl service's private approval API on behalf of a
// signed-in account, for the /link page: a game shows a code (Microsoft-style
// device sign-in) and the person approves it here. Contract: xbl's README,
// "private approval API" (internal/msa Internal()).
//
//	GET  /internal/v1/link/{user_code}        200 {user_code, client_id, expires_at} | 404 {error, reason}
//	POST /internal/v1/link/approve {user_code, account_id}
//	                                          200 {approved, gamertag} | 404 {error, reason} | 403 | 400 | 401
package xbl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is nil when the API is not configured; every method then answers
// ErrUnavailable.
type Client struct {
	BaseURL string // e.g. http://openpak-xbl:23513
	Key     string
	HTTP    *http.Client
}

// New returns nil when baseURL is empty.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

var (
	ErrUnavailable = errors.New("xbl: approval API not configured or not answering")
	ErrUnknown     = errors.New("xbl: unknown code")
	ErrExpired     = errors.New("xbl: code expired")
	ErrUsed        = errors.New("xbl: code already used")
	ErrAccount     = errors.New("xbl: account cannot sign in")
)

// Pending is a code waiting for approval.
type Pending struct {
	UserCode  string    `json:"user_code"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NormalizeCode upper-cases a code and drops spaces and dashes, as xbl does.
func NormalizeCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

// Lookup reports the pending code, or ErrUnknown, ErrExpired, ErrUsed.
func (c *Client) Lookup(ctx context.Context, code string) (Pending, error) {
	var p Pending
	err := c.do(ctx, http.MethodGet, "/internal/v1/link/"+url.PathEscape(NormalizeCode(code)), nil, &p)
	return p, err
}

// Approve gives the code to accountID and returns the name the game will show.
func (c *Client) Approve(ctx context.Context, code, accountID string) (string, error) {
	var out struct {
		Gamertag string `json:"gamertag"`
	}
	err := c.do(ctx, http.MethodPost, "/internal/v1/link/approve",
		map[string]string{"user_code": NormalizeCode(code), "account_id": accountID}, &out)
	return out.Gamertag, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if c == nil {
		return ErrUnavailable
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(raw, out)
	case http.StatusNotFound:
		var e struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &e)
		switch e.Reason {
		case "expired":
			return ErrExpired
		case "used":
			return ErrUsed
		}
		return ErrUnknown
	case http.StatusForbidden:
		return ErrAccount
	default:
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
}
