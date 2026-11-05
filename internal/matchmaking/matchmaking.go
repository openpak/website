// Package matchmaking is the website's small client of the matchmaking core: the health probe behind
// /healthz/matchmaking. Erasure goes through ERASURE_SERVICES; console adapters talk to it over gRPC.
package matchmaking

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-matchmaking:20240
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

var ErrUnavailable = fmt.Errorf("matchmaking service not configured")

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
