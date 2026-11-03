// Package titlestorage is the website's small client of the title storage service's internal API:
// the health probe behind /healthz/title-storage and the account-erasure call. Nothing else of the
// service concerns the site; console adapters talk to it over gRPC.
package titlestorage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-title-storage:20230
	Key     string
	HTTP    *http.Client
}

// New returns nil when the service is not configured.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

var ErrUnavailable = fmt.Errorf("title storage service not configured")

// EraseAccount deletes every variable and object the account owns in every title (account erasure, after the
// grace period). Idempotent.
func (c *Client) EraseAccount(ctx context.Context, account string) error {
	if c == nil {
		return ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/internal/accounts/"+url.PathEscape(account)+"/data", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("title storage service: %s: %s", resp.Status, string(detail))
	}
	return nil
}

// Healthy asks the service's /healthz with a short deadline of its own.
func (c *Client) Healthy(ctx context.Context) error {
	if c == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}
