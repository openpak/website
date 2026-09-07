package core

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

// coreSessionCookie is the account core's web session cookie name. The
// website's own cookie carries the same id under a different name
// (WEB-02: cookie session bound to a core web session id).
const coreSessionCookie = "openpak_session"

// HTTPClient talks to the account core's public /v1/* routes. It never
// touches core storage. Authentication is performed by presenting the core
// web session id as the core's own session cookie.
type HTTPClient struct {
	base    *url.URL
	hc      *http.Client
	timeout time.Duration
}

// NewHTTP builds the core HTTP client.
func NewHTTP(baseURL string, timeout time.Duration) (*HTTPClient, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("core: invalid CORE_BASE_URL: %w", err)
	}
	return &HTTPClient{
		base:    u,
		hc:      &http.Client{Timeout: timeout},
		timeout: timeout,
	}, nil
}

// call performs one JSON round trip. coreSessionID, when non-empty, is sent
// as the core's session cookie. No cookie jar is used: the website serves
// many users from one client and presents each session id deliberately.
func (c *HTTPClient) call(ctx context.Context, method, path string, body any, coreSessionID string) (int, map[string]any, []*http.Cookie, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	// Split any query off before JoinPath, which would escape the "?" into the path.
	pathOnly, rawQuery, _ := strings.Cut(path, "?")
	u := c.base.JoinPath(pathOnly)
	u.RawQuery = rawQuery
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if coreSessionID != "" {
		req.AddCookie(&http.Cookie{Name: coreSessionCookie, Value: coreSessionID})
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("core: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, resp.Cookies(), err
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out) // healthz and error shapes both decode; empty is fine
	return resp.StatusCode, out, resp.Cookies(), nil
}

// Register proxies POST /v1/register. Enumeration-resistant: both fresh
// success and "email taken" are reported as success (the core emails the
// verification link either way).
func (c *HTTPClient) Register(ctx context.Context, email, password, displayName string) error {
	code, _, _, err := c.call(ctx, http.MethodPost, "v1/register", map[string]string{
		"email": email, "password": password, "display_name": displayName,
	}, "")
	if err != nil {
		return err
	}
	if code == http.StatusCreated || code == http.StatusOK {
		return nil
	}
	return fmt.Errorf("core: registration failed (status %d)", code)
}

// VerifyEmail proxies GET /v1/verify.
func (c *HTTPClient) VerifyEmail(ctx context.Context, token string) error {
	// url.URL.JoinPath escapes what it is given, so a query string cannot ride along in
	// the path: "v1/verify?token=x" would reach the core as "/v1/verify%3Ftoken=x" and 404.
	code, _, _, err := c.call(ctx, http.MethodGet, verifyPath(token), nil, "")
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	return ErrNotFound
}

// Login proxies POST /v1/login and extracts the core web session id from
// the response cookie. The website's own cookie wraps this id (WEB-02).
func (c *HTTPClient) Login(ctx context.Context, email, password string) (string, error) {
	code, _, cookies, err := c.call(ctx, http.MethodPost, "v1/login", map[string]string{
		"email": email, "password": password,
	}, "")
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", ErrInvalidCredentials
	}
	// The core returns the session id only as a Set-Cookie header; capture
	// it explicitly.
	for _, ck := range cookies {
		if ck.Name == coreSessionCookie && ck.Value != "" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("core: login succeeded but no session cookie was returned")
}

// Logout proxies POST /v1/logout.
func (c *HTTPClient) Logout(ctx context.Context, coreSessionID string) error {
	code, _, _, err := c.call(ctx, http.MethodPost, "v1/logout", nil, coreSessionID)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return ErrSessionInvalid
	}
	return nil
}

// ListSessions proxies GET /v1/sessions. A 401 maps to ErrSessionInvalid so
// callers can treat it as "core session no longer valid".
func (c *HTTPClient) ListSessions(ctx context.Context, coreSessionID string) ([]Session, error) {
	code, body, _, err := c.call(ctx, http.MethodGet, "v1/sessions", nil, coreSessionID)
	if err != nil {
		return nil, err
	}
	if code == http.StatusUnauthorized {
		return nil, ErrSessionInvalid
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("core: list sessions failed (status %d)", code)
	}
	raw, _ := body["sessions"].([]any)
	out := make([]Session, 0, len(raw))
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		s := Session{ID: str(m["id"])}
		s.CreatedAt = timeMap(m["created_at"])
		s.ExpiresAt = timeMap(m["expires_at"])
		out = append(out, s)
	}
	return out, nil
}

// RevokeSession proxies DELETE /v1/sessions/{id}.
func (c *HTTPClient) RevokeSession(ctx context.Context, coreSessionID, sessionID string) error {
	code, _, _, err := c.call(ctx, http.MethodDelete, "v1/sessions/"+url.PathEscape(sessionID), nil, coreSessionID)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("core: revoke failed (status %d)", code)
	}
}

// ForgotPassword proxies POST /v1/forgot-password. Always succeeds from the
// caller's perspective (enumeration resistance is enforced core-side).
// ChangePassword proxies POST /v1/change-password for a signed-in account. The core requires
// the current password, so a stolen session cannot take the account over.
func (c *HTTPClient) ChangePassword(ctx context.Context, coreSessionID, current, next string) error {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/change-password", map[string]string{
		"current_password": current, "new_password": next,
	}, coreSessionID)
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	if msg, ok := body["error"].(string); ok && msg != "" {
		return errors.New(msg)
	}
	return ErrNotFound
}

func (c *HTTPClient) ForgotPassword(ctx context.Context, email string) error {
	_, _, _, err := c.call(ctx, http.MethodPost, "v1/forgot-password", map[string]string{"email": email}, "")
	return err
}

// ResetPassword proxies POST /v1/reset-password.
func (c *HTTPClient) ResetPassword(ctx context.Context, token, password string) error {
	code, _, _, err := c.call(ctx, http.MethodPost, "v1/reset-password", map[string]string{
		"token": token, "password": password,
	}, "")
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	return ErrNotFound
}

// Healthy proxies GET /healthz for the status page.
func (c *HTTPClient) Healthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	code, _, _, err := c.call(ctx, http.MethodGet, "healthz", nil, "")
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	return fmt.Errorf("core unhealthy (status %d)", code)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func timeMap(v any) time.Time {
	s, ok := v.(string)
	if !ok {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// verifyPath returns the core's verify path with the token as a real query parameter.
func verifyPath(token string) string {
	return "v1/verify?" + url.Values{"token": {token}}.Encode()
}
