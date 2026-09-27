// Package pctl talks to nx-baas's internal parental-controls API on behalf of a
// signed-in account. The device records live with the console-facing service,
// because that is what the console asks; the website only decides who is asking
// and relays. Contract: the docs repo's
// docs/switch/parental-controls-firmware-2026-09-26.md, and nx-baas's README
// internal-API table.
package pctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-nx-baas:20070
	Key     string
	HTTP    *http.Client
}

// New returns nil when nx-baas's internal API is not configured. Every method on
// a nil client answers ErrUnavailable, so the app is told the feature is off
// rather than shown a failure it cannot act on.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

var ErrUnavailable = fmt.Errorf("parental controls service not configured")

// Error is a non-2xx from nx-baas, with what it said.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }

// Device is one console's record as the app reads it. PairingCode is set only
// while it is still live.
type Device struct {
	MoonID      string   `json:"moon_id"`
	Paired      bool     `json:"paired"`
	PairingCode string   `json:"pairing_code,omitempty"`
	ExpiresAt   int64    `json:"expires_at,omitempty"`
	ActivatedAt int64    `json:"activated_at,omitempty"`
	UpdatedAt   int64    `json:"updated_at,omitempty"`
	Owners      []string `json:"owners,omitempty"`
	HasSetting  bool     `json:"has_setting"`
}

// Pairing is a freshly minted code and the record behind it.
type Pairing struct {
	MoonID      string `json:"moon_id"`
	PairingCode string `json:"pairing_code"`
	ExpiresAt   int64  `json:"expires_at"`
}

// Devices lists what this account manages.
func (c *Client) Devices(ctx context.Context, accountID string) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	err := c.doJSON(ctx, http.MethodGet,
		"/internal/pctl/devices?account_id="+url.QueryEscape(accountID), nil, &out)
	return out.Devices, err
}

// Pair mints the code to type on the console. displayName is what the console
// shows as the parent before it is linked; it may be empty.
func (c *Client) Pair(ctx context.Context, accountID, displayName string) (Pairing, error) {
	var out Pairing
	err := c.doJSON(ctx, http.MethodPost, "/internal/pctl/pairing",
		map[string]string{"account_id": accountID, "display_name": displayName}, &out)
	return out, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	if c == nil {
		return ErrUnavailable
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
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
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &Error{Status: resp.StatusCode, Code: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
