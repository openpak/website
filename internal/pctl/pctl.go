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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
// while it is still live. The PIN is not here: it travels only through
// UnlockCode, one console at a time, so a list can never leak it.
type Device struct {
	MoonID      string   `json:"moon_id"`
	Paired      bool     `json:"paired"`
	PairingCode string   `json:"pairing_code,omitempty"`
	ExpiresAt   int64    `json:"expires_at,omitempty"`
	ActivatedAt int64    `json:"activated_at,omitempty"`
	UpdatedAt   int64    `json:"updated_at,omitempty"`
	Owners      []string `json:"owners,omitempty"`
	HasSetting  bool     `json:"has_setting"`
	// Nickname is the name to show: the parent's (Named), else the end of the
	// console's serial number, else "". The console never sends a name.
	Nickname     string `json:"nickname"`
	Named        bool   `json:"named"`
	SerialNumber string `json:"serial_number"`
	Firmware     string `json:"firmware"`
	// Setting is the document as nx-baas serves it to the console.
	Setting map[string]any `json:"setting,omitempty"`
	Players []Player       `json:"players"`
	// SyncedAt is the console's last receipt, 0 if none; Synced says that
	// receipt names the document currently served.
	SyncedAt int64 `json:"synced_at"`
	Synced   bool  `json:"synced"`
	// WrongPinAttempts are when a wrong PIN was typed on the console, unix
	// seconds, newest first.
	WrongPinAttempts []int64 `json:"wrong_pin_attempts"`
}

// Player is one user on a paired console, by the console's own id. Its picture
// is PlayerImage.
type Player struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
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

// UnlockCode is the PIN of one console this account manages, "" when the
// console has not named it or the console is not this account's.
func (c *Client) UnlockCode(ctx context.Context, accountID, moonID string) (string, error) {
	var out struct {
		Devices []struct {
			MoonID     string `json:"moon_id"`
			UnlockCode string `json:"unlock_code"`
		} `json:"devices"`
	}
	err := c.doJSON(ctx, http.MethodGet,
		"/internal/pctl/devices?account_id="+url.QueryEscape(accountID), nil, &out)
	for _, d := range out.Devices {
		if d.MoonID == moonID {
			return d.UnlockCode, err
		}
	}
	return "", err
}

// PlayerImage is a player's JPEG. nx-baas answers 404 for a console another
// account manages, the same as for one that does not exist.
func (c *Client) PlayerImage(ctx context.Context, accountID, moonID, playerID string) ([]byte, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/internal/pctl/devices/"+
		url.PathEscape(moonID)+"/players/"+url.PathEscape(playerID)+"/image?account_id="+url.QueryEscape(accountID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Status: resp.StatusCode, Code: resp.Status}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Pair mints the code to type on the console. displayName is what the console
// shows as the parent before it is linked; it may be empty.
func (c *Client) Pair(ctx context.Context, accountID, displayName string) (Pairing, error) {
	var out Pairing
	err := c.doJSON(ctx, http.MethodPost, "/internal/pctl/pairing",
		map[string]string{"account_id": accountID, "display_name": displayName}, &out)
	return out, err
}

// Saved is nx-baas's answer to a parent's write: the ETag the console will be
// served, and when the record changed (unix seconds).
type Saved struct {
	ETag      string `json:"etag"`
	UpdatedAt int64  `json:"updated_at"`
}

// SetSetting stores the whole setting document for the console. nx-baas refuses
// anything the console's parse table would not take with a 400 naming its path
// (Error.Path); nothing is stored then.
func (c *Client) SetSetting(ctx context.Context, accountID, moonID string, setting map[string]any) (Saved, error) {
	var out Saved
	err := c.doJSON(ctx, http.MethodPut, devicePath(accountID, moonID, "/setting"), map[string]any{"setting": setting}, &out)
	return out, err
}

// SetUnlockCode gives the console a new PIN at its next sync. Never logged.
func (c *Client) SetUnlockCode(ctx context.Context, accountID, moonID, code string) error {
	return c.doJSON(ctx, http.MethodPut, devicePath(accountID, moonID, "/setting"), map[string]string{"unlock_code": code}, &Saved{})
}

// ValidPin is exactly four ASCII digits: the console's parser takes up to
// eight, but its entry field has four places (hardware, 2026-09-27).
func ValidPin(code string) bool {
	if len(code) != 4 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ErrNameUnsupported is a nx-baas that cannot store a console's name yet: it
// answers 404 for a console this account does manage.
var ErrNameUnsupported = fmt.Errorf("naming a console is not available yet")

// SetName names the console for its parent; "" clears it. The name never goes
// to the console.
func (c *Client) SetName(ctx context.Context, accountID, moonID, name string) error {
	err := c.doJSON(ctx, http.MethodPut, devicePath(accountID, moonID, "/name"), map[string]string{"name": name}, nil)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		if mine, lerr := c.manages(ctx, accountID, moonID); lerr != nil {
			return lerr
		} else if mine {
			return ErrNameUnsupported
		}
	}
	return err
}

// manages says whether moonID is one of this account's consoles.
func (c *Client) manages(ctx context.Context, accountID, moonID string) (bool, error) {
	devices, err := c.Devices(ctx, accountID)
	return slices.ContainsFunc(devices, func(d Device) bool { return d.MoonID == moonID }), err
}

// ValidName is at most 32 characters and no control characters, as nx-baas
// takes it; the caller trims it first.
func ValidName(name string) bool {
	return utf8.RuneCountInString(name) <= 32 && !strings.ContainsFunc(name, unicode.IsControl)
}

// Remove takes the console off this account's list. The console is not told.
func (c *Client) Remove(ctx context.Context, accountID, moonID string) error {
	return c.doJSON(ctx, http.MethodDelete, devicePath(accountID, moonID, ""), nil, nil)
}

// Day is one day's play on a console, as nx-baas totals it from the console's
// events. Name is filled in by the caller from the titles catalogue.
type Day struct {
	Date         string      `json:"date"`
	TotalMinutes int         `json:"total_minutes"`
	Titles       []TitleTime `json:"titles"`
}

type TitleTime struct {
	TitleID string `json:"title_id"`
	Name    string `json:"name,omitempty"`
	Minutes int    `json:"minutes"`
	// Players split the title's time by the console's own user ids, most
	// first; AnonymousMinutes is time played with no user selected.
	Players          []PlayerTime `json:"players"`
	AnonymousMinutes int          `json:"anonymous_minutes"`
}

// PlayerTime is one user's share of a title. Nickname is filled in by
// NamePlayers, and stays empty for a user the console no longer lists.
type PlayerTime struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname,omitempty"`
	Minutes  int    `json:"minutes"`
}

// NamePlayers names each player in the activity from the console's own list
// of its users.
func NamePlayers(days []Day, players []Player) {
	names := map[string]string{}
	for _, p := range players {
		names[p.ID] = p.Nickname
	}
	for i := range days {
		for j := range days[i].Titles {
			for k, p := range days[i].Titles[j].Players {
				days[i].Titles[j].Players[k].Nickname = names[p.ID]
			}
		}
	}
}

// Activity is the last days of play, newest first, empty days left out. A
// nx-baas that does not serve activity yet answers 404 for every console, so a
// 404 for a console this account does manage reads as no activity.
func (c *Client) Activity(ctx context.Context, accountID, moonID string, days int) ([]Day, error) {
	var out struct {
		Days []Day `json:"days"`
	}
	err := c.doJSON(ctx, http.MethodGet, devicePath(accountID, moonID, "/activity")+"&days="+strconv.Itoa(days), nil, &out)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		if mine, lerr := c.manages(ctx, accountID, moonID); lerr != nil {
			return nil, lerr
		} else if mine {
			return []Day{}, nil
		}
	}
	if out.Days == nil {
		out.Days = []Day{}
	}
	// An nx-baas before v0.48.0 does not split a title's time by player.
	for i := range out.Days {
		for j := range out.Days[i].Titles {
			if out.Days[i].Titles[j].Players == nil {
				out.Days[i].Titles[j].Players = []PlayerTime{}
			}
		}
	}
	return out.Days, err
}

// Path is the key of the setting document nx-baas refused, dotted from the
// document's root, or "" when the error is not such a refusal.
func (e *Error) Path() string {
	if e.Status != http.StatusBadRequest {
		return ""
	}
	if p, ok := strings.CutPrefix(e.Code, "not in the console's parse table: "); ok {
		return p
	}
	if p, ok := strings.CutPrefix(e.Code, "setting."); ok {
		return strings.TrimSuffix(p, " required")
	}
	return ""
}

func devicePath(accountID, moonID, rest string) string {
	return "/internal/pctl/devices/" + url.PathEscape(moonID) + rest + "?account_id=" + url.QueryEscape(accountID)
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
