// Package discordlink is the website's side of linking a Discord account to an
// OpenPak account: Discord OAuth2 with the identify scope only, nothing else.
// The access token is used once to read the Discord user id and name and is
// then revoked; the website keeps the id and the name, never the token.
package discordlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Scope is the only scope ever requested.
const Scope = "identify"

// CallbackPath is the page Discord returns the person to. The full redirect
// URI (https://<site host><CallbackPath>) must be registered, exactly, in the
// Discord developer portal under OAuth2 > Redirects.
const CallbackPath = "/account/link/discord/callback"

// Client talks to Discord. A nil *Client means linking is not configured.
type Client struct {
	ClientID     string
	ClientSecret string
	// AuthorizeURL and APIBase are Discord's; tests point them at a fake.
	AuthorizeURL string
	APIBase      string
	HTTP         *http.Client
}

// User is what identify returns that the website keeps.
type User struct {
	ID       string
	Username string
}

// FromEnv builds the client from DISCORD_OAUTH_CLIENT_ID and
// DISCORD_OAUTH_CLIENT_SECRET; nil when either is missing.
func FromEnv() *Client {
	id, secret := strings.TrimSpace(os.Getenv("DISCORD_OAUTH_CLIENT_ID")), strings.TrimSpace(os.Getenv("DISCORD_OAUTH_CLIENT_SECRET"))
	if id == "" || secret == "" {
		return nil
	}
	return New(id, secret)
}

// New returns a client for Discord's production endpoints.
func New(id, secret string) *Client {
	return &Client{
		ClientID: id, ClientSecret: secret,
		AuthorizeURL: "https://discord.com/oauth2/authorize",
		APIBase:      "https://discord.com/api",
		HTTP:         &http.Client{Timeout: 10 * time.Second},
	}
}

// AuthURL is where the person is sent to approve the link.
func (c *Client) AuthURL(redirectURI, state string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"response_type": {"code"},
		"scope":         {Scope},
		"redirect_uri":  {redirectURI},
		"state":         {state},
		"prompt":        {"consent"},
	}
	return c.AuthorizeURL + "?" + q.Encode()
}

var snowflake = regexp.MustCompile(`^[0-9]{5,25}$`)

// ValidID reports whether s can be a Discord user id (a snowflake).
func ValidID(s string) bool { return snowflake.MatchString(s) }

// Identify trades the authorisation code for the person's Discord id and
// name, then revokes the token. It fails if Discord returns anything but a
// plain snowflake id.
func (c *Client) Identify(ctx context.Context, code, redirectURI string) (User, error) {
	tok, err := c.exchange(ctx, code, redirectURI)
	if err != nil {
		return User{}, err
	}
	defer c.revoke(tok)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/users/@me", nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	var me struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	}
	if err := c.do(req, &me); err != nil {
		return User{}, err
	}
	if !ValidID(me.ID) {
		return User{}, errors.New("discord: unexpected user id")
	}
	name := me.Username
	if name == "" {
		name = me.GlobalName
	}
	return User{ID: me.ID, Username: clip(name, 64)}, nil
}

func (c *Client) exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{
		"client_id": {c.ClientID}, "client_secret": {c.ClientSecret},
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("discord: no access token")
	}
	return out.AccessToken, nil
}

// revoke is best effort: the token only ever had identify.
func (c *Client) revoke(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	form := url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+"/oauth2/token/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := c.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord: status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("discord: bad response: %w", err)
	}
	return nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
