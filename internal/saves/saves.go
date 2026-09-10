// Package saves talks to the saves service's internal API on behalf of a signed-in account.
// The website decides who the account is; the service holds the index and the bytes.
package saves

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-saves:20020
	Key     string
	HTTP    *http.Client
}

// New returns nil when the service is not configured; every method on a nil client answers
// ErrUnavailable so the pages can say so instead of failing.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

var ErrUnavailable = fmt.Errorf("saves service not configured")

// Error is a non-2xx answer from the service, with what it said.
type Error struct {
	Status int
	Code   string
	Detail string
	Used   int64
	Limit  int64
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

type Version struct {
	ID        int64      `json:"id"`
	Number    int        `json:"number"`
	Base      *int       `json:"base,omitempty"`
	Conflict  bool       `json:"conflict"`
	Pinned    bool       `json:"pinned"`
	Size      int64      `json:"size"`
	SHA256    string     `json:"sha256,omitempty"`
	Device    string     `json:"device,omitempty"`
	Backend   string     `json:"backend"`
	Parts     int        `json:"parts"`
	SavedAt   *time.Time `json:"saved_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Save struct {
	Platform string    `json:"platform"`
	TitleID  string    `json:"title_id"`
	Versions []Version `json:"versions"`
}

type Usage struct {
	AllowanceUsed int64 `json:"allowance_used"`
	Allowance     int64 `json:"allowance"`
	Total         int64 `json:"total"`
}

type Storage struct {
	Connected  bool       `json:"connected"`
	Kind       string     `json:"kind"`
	Endpoint   string     `json:"endpoint"`
	Region     string     `json:"region"`
	Bucket     string     `json:"bucket"`
	Prefix     string     `json:"prefix"`
	AccessKey  string     `json:"access_key"`
	PathStyle  bool       `json:"path_style"`
	Insecure   bool       `json:"insecure"`
	LastTestAt *time.Time `json:"last_test_at"`
	LastTestOK *bool      `json:"last_test_ok"`
	LastError  string     `json:"last_error"`
	MoveState  string     `json:"move_state"`
	Usage      Usage      `json:"usage"`
}

// S3Config is what the settings form submits. The secret is optional on an edit: empty keeps
// the stored one when the access key is unchanged.
type S3Config struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Insecure  bool   `json:"insecure"`
	PathStyle bool   `json:"path_style"`
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, size int64) (*http.Response, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		e := &Error{Status: resp.StatusCode}
		var body struct {
			Error  string `json:"error"`
			Detail string `json:"detail"`
			Used   int64  `json:"used"`
			Limit  int64  `json:"limit"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
		e.Code, e.Detail, e.Used, e.Limit = body.Error, body.Detail, body.Used, body.Limit
		if e.Code == "" {
			e.Code = resp.Status
		}
		return nil, e
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	size := int64(0)
	if in != nil {
		b, _ := json.Marshal(in)
		body, size = bytes.NewReader(b), int64(len(b))
	}
	resp, err := c.do(ctx, method, path, body, size)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func acct(account string) string { return "/internal/accounts/" + url.PathEscape(account) }

func (c *Client) List(ctx context.Context, account string) ([]Save, Usage, error) {
	var out struct {
		Saves []Save `json:"saves"`
		Usage Usage  `json:"usage"`
	}
	err := c.doJSON(ctx, http.MethodGet, acct(account)+"/saves", nil, &out)
	return out.Saves, out.Usage, err
}

func (c *Client) Storage(ctx context.Context, account string) (Storage, error) {
	var out Storage
	err := c.doJSON(ctx, http.MethodGet, acct(account)+"/storage", nil, &out)
	return out, err
}

// Connect tests then stores the bucket. The returned warning is advisory (a public bucket).
func (c *Client) Connect(ctx context.Context, account string, cfg S3Config) (string, error) {
	var out struct {
		Warning string `json:"warning"`
	}
	err := c.doJSON(ctx, http.MethodPut, acct(account)+"/storage", cfg, &out)
	return out.Warning, err
}

func (c *Client) Disconnect(ctx context.Context, account string) error {
	return c.doJSON(ctx, http.MethodDelete, acct(account)+"/storage", nil, nil)
}

func (c *Client) Move(ctx context.Context, account string) error {
	return c.doJSON(ctx, http.MethodPost, acct(account)+"/storage/move", nil, nil)
}

func (c *Client) Pin(ctx context.Context, account string, id int64, pinned bool) error {
	return c.doJSON(ctx, http.MethodPost, acct(account)+"/versions/"+strconv.FormatInt(id, 10)+"/pin", map[string]bool{"pinned": pinned}, nil)
}

func (c *Client) Restore(ctx context.Context, account string, id int64) error {
	return c.doJSON(ctx, http.MethodPost, acct(account)+"/versions/"+strconv.FormatInt(id, 10)+"/restore", nil, nil)
}

func (c *Client) Delete(ctx context.Context, account string, id int64) error {
	return c.doJSON(ctx, http.MethodDelete, acct(account)+"/versions/"+strconv.FormatInt(id, 10), nil, nil)
}

// Download streams one version's bytes; the caller closes the body.
func (c *Client) Download(ctx context.Context, account string, id int64) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, acct(account)+"/versions/"+strconv.FormatInt(id, 10), nil, -1)
}

// Newest streams the current save of a title, or a 204 response when there is none.
func (c *Client) Newest(ctx context.Context, account, platform, title string) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, acct(account)+"/saves/"+url.PathEscape(platform)+"/"+url.PathEscape(title), nil, -1)
}

// Upload streams a new version. base is the version number the client loaded from ("" for none).
func (c *Client) Upload(ctx context.Context, account, platform, title, base, device string, body io.Reader, size int64) (Version, error) {
	q := url.Values{}
	if base != "" {
		q.Set("base", base)
	}
	if device != "" {
		q.Set("device", device)
	}
	var v Version
	resp, err := c.do(ctx, http.MethodPut, acct(account)+"/saves/"+url.PathEscape(platform)+"/"+url.PathEscape(title)+"?"+q.Encode(), body, size)
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	return v, json.NewDecoder(resp.Body).Decode(&v)
}
