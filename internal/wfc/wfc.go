// Package wfc is the website's client of the Wii/DS server's pairing API (wfc-api). WFC has no
// login: an unlinked Wii or DS is refused online and shown a pairing code (an 8-symbol code in
// Mario Kart Wii, a 6-digit link number elsewhere); the player enters it at /link, and wfc-api
// makes the account-core link (namespace "wfc") itself, as xb-account does for the Xbox
// (prds repos/wfc-link-first-design.md). The website passes the signed-in account and never the
// browser's word for it.
package wfc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-wfc-api:20209
	Secret  string // wfc-api's WFC_API_SECRET
	HTTP    *http.Client
}

func New(baseURL, secret string) *Client {
	if baseURL == "" {
		return nil // not configured: the page offers no Wii/DS link
	}
	return &Client{BaseURL: baseURL, Secret: secret, HTTP: &http.Client{Timeout: 3 * time.Second}}
}

var (
	// ErrPairingCode is a code or number that is unknown, expired, used, or being claimed by
	// someone else (one answer on purpose).
	ErrPairingCode = errors.New("wfc: pairing code unknown, expired or used")
	// ErrConflict: the console is linked to another account, or this account holds the most
	// consoles it may.
	ErrConflict = errors.New("wfc: console linked elsewhere or console limit reached")
	// ErrApprovalFailed: the account core refused the link; the console should connect again.
	ErrApprovalFailed = errors.New("wfc: the account core refused the link")
	// ErrNotFound: no such console on this account.
	ErrNotFound = errors.New("wfc: no such console")
)

// Pairing is the console a live pairing code belongs to.
type Pairing struct {
	Unit          string    `json:"unit"` // "wii" or "ds"
	GameCode      string    `json:"gsbrcd"`
	Name          string    `json:"name"` // in-game name as the console sent it
	ExpiresAt     time.Time `json:"expires_at"`
	AlreadyLinked bool      `json:"already_linked"`
}

// Console is one linked Wii or DS.
type Console struct {
	LinkID   string    `json:"link_id"`
	Unit     string    `json:"unit"`
	Games    []string  `json:"games"`
	LinkedAt time.Time `json:"linked_at"`
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
	case http.StatusConflict:
		return ErrConflict
	case http.StatusUnprocessableEntity:
		return ErrApprovalFailed
	default:
		return fmt.Errorf("wfc: %s", resp.Status)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// PairPreview says which console a code or link number belongs to, without spending it.
//
// A 5-digit number (what most consoles show) resolves only when clientIP is on the console's
// network or name is the console's in-game name; an 8-symbol code needs neither.
func (c *Client) PairPreview(ctx context.Context, code, clientIP, name string) (Pairing, error) {
	var p Pairing
	if err := c.post(ctx, "/api/pair/preview", map[string]any{"code": code, "client_ip": clientIP, "name": name}, &p); err != nil {
		return Pairing{}, err
	}
	if p.Unit == "" {
		return Pairing{}, ErrPairingCode
	}
	return p, nil
}

// PairApprove links the console behind a code to the signed-in account. accountID must come
// from the server session.
func (c *Client) PairApprove(ctx context.Context, code, accountID, clientIP, name string) (Pairing, error) {
	var p Pairing
	if err := c.post(ctx, "/api/pair/approve", map[string]any{"code": code, "core_account_id": accountID, "client_ip": clientIP, "name": name}, &p); err != nil {
		return Pairing{}, err
	}
	return p, nil
}

// Consoles lists the account's linked Wii and DS consoles.
func (c *Client) Consoles(ctx context.Context, accountID string) ([]Console, error) {
	var out struct {
		Consoles []Console `json:"consoles"`
	}
	if err := c.post(ctx, "/api/pair/consoles", map[string]any{"core_account_id": accountID}, &out); err != nil {
		return nil, err
	}
	return out.Consoles, nil
}

// Unlink revokes one console of the account; erase also deletes the console's online game data.
func (c *Client) Unlink(ctx context.Context, accountID, linkID string, erase bool) error {
	err := c.post(ctx, "/api/pair/unlink", map[string]any{"core_account_id": accountID, "link_id": linkID, "erase": erase}, nil)
	if errors.Is(err, ErrPairingCode) {
		return ErrNotFound
	}
	return err
}
