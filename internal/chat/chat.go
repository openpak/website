// Package chat talks to the chat service's internal API on behalf of a
// signed-in account. The website decides who the account is; the service
// holds the messages and applies the friendship rule. The same shape as the
// saves client, because it is the same relationship between two services.
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	BaseURL string // e.g. http://openpak-chat:20130
	Key     string
	HTTP    *http.Client
}

// New returns nil when the service is not configured; every method on a nil
// client answers ErrUnavailable so the pages can say so instead of failing.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

var ErrUnavailable = fmt.Errorf("chat service not configured")

type Message struct {
	ID         int64  `json:"id"`
	From       string `json:"from_account"`
	To         string `json:"to_account"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	SentAtUnix int64  `json:"sent_at_unix"`
}

func (m Message) SentAt() time.Time { return time.Unix(m.SentAtUnix, 0) }

type Conversation struct {
	Peer   string  `json:"peer"`
	Newest Message `json:"newest"`
}

func (c *Client) call(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("chat service: %s: %s", resp.Status, string(detail))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Conversations(ctx context.Context, account string) ([]Conversation, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	var wire struct {
		Conversations []struct {
			Peer   string  `json:"peer"`
			Newest Message `json:"newest"`
		} `json:"conversations"`
	}
	if err := c.call(ctx, http.MethodGet, "/internal/accounts/"+account+"/conversations", nil, &wire); err != nil {
		return nil, err
	}
	out := []Conversation{}
	for _, w := range wire.Conversations {
		out = append(out, Conversation{Peer: w.Peer, Newest: w.Newest})
	}
	return out, nil
}

func (c *Client) History(ctx context.Context, account, peer string, after int64) ([]Message, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	var wire struct {
		Messages []Message `json:"messages"`
	}
	if err := c.call(ctx, http.MethodGet, "/internal/accounts/"+account+"/messages/"+peer+"?after="+strconv.FormatInt(after, 10), nil, &wire); err != nil {
		return nil, err
	}
	out := []Message{}
	out = append(out, wire.Messages...)
	return out, nil
}

// Send posts one message; the service announces it to the recipient's
// adapters. dedup may be empty from the website (a form post is its own
// dedup); adapters supply one.
func (c *Client) Send(ctx context.Context, account, peer, body, dedup string) (Message, error) {
	if c == nil {
		return Message{}, ErrUnavailable
	}
	payload, _ := json.Marshal(map[string]string{"body": body, "dedup_key": dedup})
	var wire struct {
		Message Message `json:"message"`
	}
	if err := c.call(ctx, http.MethodPost, "/internal/accounts/"+account+"/messages/"+peer, bytes.NewReader(payload), &wire); err != nil {
		return Message{}, err
	}
	return wire.Message, nil
}

// Healthy asks the service's /healthz, for the admin monitor. A short deadline of its own:
// the client's timeout is generous for slow reads, and a health check that waits that long is
// not answering the question.
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
