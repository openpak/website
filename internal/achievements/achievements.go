// Package achievements is the website's small client of the achievements service's internal API:
// the health probe behind /healthz/achievements and the account-erasure call. The trophies the
// site shows come through np-trophy's private API for now.
package achievements

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-achievements:20220
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

var ErrUnavailable = fmt.Errorf("achievements service not configured")

// EraseAccount deletes every unlock and title-history row the account holds (account erasure, after the
// grace period). Idempotent.
func (c *Client) EraseAccount(ctx context.Context, account string) error {
	if c == nil {
		return ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/internal/accounts/"+url.PathEscape(account)+"/achievements", nil)
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
		return fmt.Errorf("achievements service: %s: %s", resp.Status, string(detail))
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
