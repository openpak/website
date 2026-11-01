// Package rewards is the website's client for the rewards service (platforms/core/infra/rewards):
// points, missions, the shop and the inventory for a signed-in account, and the admin flags queue.
// User calls carry the account-core session id as the bearer, so the service itself decides who is
// asking; admin calls carry the admin key and the acting admin's account id.
package rewards

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

// Client talks to the rewards service on the private network.
type Client struct {
	BaseURL     string // http://openpak-rewards:20180
	InternalKey string
	AdminKey    string
	HTTP        *http.Client
}

// New returns nil when REWARDS_URL is not set; every page then says rewards are not available.
func New(baseURL, internalKey, adminKey string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), InternalKey: internalKey, AdminKey: adminKey,
		HTTP: &http.Client{Timeout: 8 * time.Second}}
}

// ErrUnavailable is a rewards service this deployment does not have.
var ErrUnavailable = errors.New("rewards: not configured")

// Error is a non-2xx answer from the service, with its message.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("rewards: %d %s", e.Status, e.Message) }

// Account is the points header.
type Account struct {
	AccountID           string `json:"account_id"`
	Balance             int    `json:"balance"`
	LifetimeEarned      int    `json:"lifetime_earned"`
	LifetimePlaySeconds int64  `json:"lifetime_play_seconds"`
	Frozen              bool   `json:"frozen"`
	Paused              bool   `json:"paused"`
	ShowcasePublic      bool   `json:"showcase_public"`
}

// Today is the day's earning caps.
type Today struct {
	Day         string `json:"day"`
	PlayPoints  int    `json:"play_points"`
	PlayCap     int    `json:"play_cap"`
	PlaySeconds int64  `json:"play_seconds"`
	CheckinDone bool   `json:"checkin_done"`
	NewTitles   int    `json:"new_titles"`
	NewTitleCap int    `json:"new_titles_cap"`
}

// Me is GET /v1/me.
type Me struct {
	Account  Account           `json:"account"`
	Today    Today             `json:"today"`
	Equipped map[string]string `json:"equipped"`
}

// LedgerEntry is one line of the points history.
type LedgerEntry struct {
	ID        int64     `json:"id"`
	Delta     int       `json:"delta"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Ref       string    `json:"ref"`
	CreatedAt time.Time `json:"created_at"`
}

// Mission is a mission with the caller's progress.
type Mission struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Kind         string `json:"kind"`
	Metric       string `json:"metric"`
	Target       int64  `json:"target"`
	RewardPoints int    `json:"reward_points"`
	RewardItem   string `json:"reward_item"`
	Period       string `json:"period"`
	Progress     int64  `json:"progress"`
	Completed    bool   `json:"completed"`
	Claimed      bool   `json:"claimed"`
	Claimable    bool   `json:"claimable"`
	Locked       bool   `json:"locked"`
}

// Item is a shop or inventory item.
type Item struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Price            *int     `json:"price"`
	Platforms        []string `json:"platforms"`
	Limited          bool     `json:"limited"`
	HasPreview       bool     `json:"has_preview"`
	Owned            bool     `json:"owned"`
	Affordable       bool     `json:"affordable"`
	Equipped         bool     `json:"equipped"`
	RedemptionSource string   `json:"source"`
}

// Flag is one entry of the admin abuse queue.
type Flag struct {
	ID         int64      `json:"id"`
	AccountID  string     `json:"account_id"`
	Reason     string     `json:"reason"`
	Detail     string     `json:"detail"`
	RaisedAt   time.Time  `json:"raised_at"`
	ResolvedBy string     `json:"resolved_by"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

// Stats is the admin overview.
type Stats struct {
	Accounts       int `json:"accounts"`
	PointsIssued   int `json:"points_issued"`
	PointsSpent    int `json:"points_spent"`
	IssuedToday    int `json:"issued_today"`
	OpenFlags      int `json:"open_flags"`
	FrozenAccounts int `json:"frozen_accounts"`
	// Redemptions counts redemptions per item id.
	Redemptions map[string]int64 `json:"redemptions_by_item"`
}

func (c *Client) do(ctx context.Context, method, path string, hdr map[string]string, in, out any) error {
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
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func user(session string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + session}
}

// Me reads the points header for the session's account.
func (c *Client) Me(ctx context.Context, session string) (Me, error) {
	var out Me
	return out, c.do(ctx, http.MethodGet, "/v1/me", user(session), nil, &out)
}

// Ledger reads the newest points entries.
func (c *Client) Ledger(ctx context.Context, session string, limit int) ([]LedgerEntry, error) {
	var out struct {
		Entries []LedgerEntry `json:"entries"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/me/ledger?limit=%d", limit), user(session), nil, &out)
	return out.Entries, err
}

// Missions lists the live missions with the session's progress.
func (c *Client) Missions(ctx context.Context, session string) ([]Mission, error) {
	var out struct {
		Missions []Mission `json:"missions"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/me/missions", user(session), nil, &out)
	return out.Missions, err
}

// Claim collects a completed mission's reward; period "" is the current one.
func (c *Client) Claim(ctx context.Context, session, id, period string) (points int, err error) {
	var out struct {
		Points int `json:"points"`
	}
	in := map[string]string{}
	if period != "" {
		in["period"] = period
	}
	err = c.do(ctx, http.MethodPost, "/v1/me/missions/"+url.PathEscape(id)+"/claim", user(session), in, &out)
	return out.Points, err
}

// Catalog is the shop with ownership and affordability for the session.
func (c *Client) Catalog(ctx context.Context, session string) ([]Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/me/catalog", user(session), nil, &out)
	return out.Items, err
}

// Redeem buys an item; key makes a retry charge once.
func (c *Client) Redeem(ctx context.Context, session, itemID, key string) error {
	h := user(session)
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return c.do(ctx, http.MethodPost, "/v1/me/redeem", h, map[string]string{"item_id": itemID}, nil)
}

// Inventory lists what the session owns.
func (c *Client) Inventory(ctx context.Context, session string) ([]Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/me/inventory", user(session), nil, &out)
	return out.Items, err
}

// Equip puts an owned item in its slot (the slot is the item's kind); itemID "" clears the slot.
func (c *Client) Equip(ctx context.Context, session, slot, itemID string) error {
	if itemID == "" {
		return c.do(ctx, http.MethodDelete, "/v1/me/equipped/"+url.PathEscape(slot), user(session), nil, nil)
	}
	return c.do(ctx, http.MethodPut, "/v1/me/equipped/"+url.PathEscape(slot), user(session), map[string]string{"item_id": itemID}, nil)
}

// Settings sets the showcase and pause switches.
func (c *Client) Settings(ctx context.Context, session string, showcase, paused bool) error {
	return c.do(ctx, http.MethodPut, "/v1/me/settings", user(session),
		map[string]bool{"showcase_public": showcase, "paused": paused}, nil)
}

// Preview fetches an item's preview image (anonymous).
func (c *Client) Preview(ctx context.Context, id string) (body []byte, contentType string, err error) {
	if c == nil {
		return nil, "", ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/items/"+url.PathEscape(id)+"/preview", nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &Error{Status: resp.StatusCode}
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return body, resp.Header.Get("Content-Type"), err
}

func (c *Client) admin(actor string) map[string]string {
	return map[string]string{"X-Admin-Key": c.AdminKey, "X-Admin-Actor": actor}
}

// AdminStats is the admin overview.
func (c *Client) AdminStats(ctx context.Context, actor string) (Stats, error) {
	var out Stats
	if c == nil {
		return out, ErrUnavailable
	}
	return out, c.do(ctx, http.MethodGet, "/internal/v1/admin/stats", c.admin(actor), nil, &out)
}

// AdminFlags is the abuse queue: open flags, or every flag with all.
func (c *Client) AdminFlags(ctx context.Context, actor string, all bool) ([]Flag, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	path := "/internal/v1/admin/flags?limit=100"
	if all {
		path += "&all=1"
	}
	var out struct {
		Flags []Flag `json:"flags"`
	}
	err := c.do(ctx, http.MethodGet, path, c.admin(actor), nil, &out)
	return out.Flags, err
}

// ResolveFlag closes a flag with a note.
func (c *Client) ResolveFlag(ctx context.Context, actor string, id int64, note string) error {
	if c == nil {
		return ErrUnavailable
	}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/internal/v1/admin/flags/%d/resolve", id), c.admin(actor), map[string]string{"note": note}, nil)
}
