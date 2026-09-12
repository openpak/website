// Package adapter asks the Switch adapter the things only it knows.
//
// The core records that an account has a console linked, and the subject of
// that link is the BAAS user id. It is not the friend code: codes are minted
// by the adapter, in its own format, and live in its projection store. The
// site used to format the hex subject as SW-xxxx-xxxx-xxxx, which produced a
// plausible-looking identifier no console would ever accept.
package adapter

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

func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil // not configured: callers show no code rather than a wrong one
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 3 * time.Second}}
}

// FriendCode returns the console friend code for an account, or "" when there
// is none. An unreachable adapter gives the same answer as "no code": a page
// has to render either way, and a made-up code is worse than a missing one.
// AccountByFriendCode is the reverse of FriendCode, and the lookup the
// add-a-friend form needs. The core's "switch" link subject is the BAAS user
// id, never the friend code, so asking the core for a link whose subject is a
// friend code could only ever miss -- which is what every Switch friend code
// did. The adapter mints the codes and is the only thing that holds the map.
func (c *Client) AccountByFriendCode(ctx context.Context, code string) string {
	if c == nil || code == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/switch/account-by-friend-code?friend_code=%s", c.BaseURL, url.QueryEscape(code)), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.AccountID
}

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
	req.Header.Set("X-Internal-Key", c.Key)
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

// get performs one internal-key GET and decodes JSON into out. A nil client or any failure is
// an error: unlike FriendCode, these callers must not render a blank instead.
func (c *Client) get(ctx context.Context, path string, out any) error {
	if c == nil {
		return fmt.Errorf("switch adapter not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adapter: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Emulator is the Switch identity of an account as an emulator needs it: PID, friend code,
// the openpak token, and the friend graph with presence. Returned as the adapter sent it.
func (c *Client) Emulator(ctx context.Context, accountID string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/internal/switch/emulator?account_id="+url.QueryEscape(accountID), &out)
	return out, err
}

// Console is the identity of an account for a console family served by another adapter
// (nn-account: "wiiu", "3ds"), the same shape of call as Emulator but on that adapter's
// /internal/<family>/emulator. Returned as the adapter sent it.
func (c *Client) Console(ctx context.Context, family, accountID string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/internal/"+family+"/emulator?account_id="+url.QueryEscape(accountID), &out)
	return out, err
}

// EmulatorFriend performs a friend action for an account by PID or friend code.
func (c *Client) EmulatorFriend(ctx context.Context, accountID, action string, pid uint64, friendCode string) error {
	if c == nil {
		return fmt.Errorf("switch adapter not configured")
	}
	body, _ := json.Marshal(map[string]any{"account_id": accountID, "action": action, "pid": pid, "friend_code": friendCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/internal/switch/emulator/friend", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("X-Internal-Key", c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s", e.Error)
	}
	return nil
}

// CA is the PEM of the CA the console-facing certificates chain to.
func (c *Client) CA(ctx context.Context) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("switch adapter not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/internal/switch/ca", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adapter: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<16))
}
