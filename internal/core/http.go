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

type langKey struct{}

// WithLang marks ctx with the language of the person being served; calls made
// with it send Accept-Language, which picks the language of the core's emails.
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
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
	if lang, _ := ctx.Value(langKey{}).(string); lang != "" {
		req.Header.Set("Accept-Language", lang)
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
// Register creates an account. Country and birthday are part of registration because the
// console's user object requires both and rejects it outright when either is missing.
func (c *HTTPClient) Register(ctx context.Context, email, password, displayName string) error {
	return c.RegisterWithProfile(ctx, email, password, displayName, displayName, "", "")
}

func (c *HTTPClient) RegisterWithProfile(ctx context.Context, email, password, displayName, handle, country, birthday string) error {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/register", map[string]string{
		"email": email, "password": password, "display_name": displayName, "handle": handle,
		"country": country, "birthday": birthday,
	}, "")
	if err != nil {
		return err
	}
	if code == http.StatusCreated || code == http.StatusOK {
		return nil
	}
	switch str(body["error"]) {
	case "handle_taken":
		return ErrHandleTaken
	case "handle_invalid":
		return ErrHandleInvalid
	case "handle_reserved":
		return ErrHandleReserved
	case "handle_profane":
		return ErrHandleProfane
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
// the response cookie. The website's own cookie wraps this id (WEB-02). A 2FA
// account answers 202 with a challenge and no cookie: *LoginChallenge.
func (c *HTTPClient) Login(ctx context.Context, email, password string) (string, error) {
	code, body, cookies, err := c.call(ctx, http.MethodPost, "v1/login", map[string]string{
		"email": email, "password": password,
	}, "")
	if err != nil {
		return "", err
	}
	if code == http.StatusAccepted && body["two_factor_required"] == true {
		// Password right, second factor owed: the core set no cookie.
		return "", &LoginChallenge{Challenge: str(body["challenge"])}
	}
	if code != http.StatusOK {
		return "", ErrInvalidCredentials
	}
	return sessionFrom(cookies)
}

// Login2FA proxies POST /v1/login/2fa.
func (c *HTTPClient) Login2FA(ctx context.Context, challenge, code, recoveryCode string) (string, error) {
	req := map[string]string{"challenge": challenge}
	if recoveryCode != "" {
		req["recovery_code"] = recoveryCode
	} else {
		req["code"] = code
	}
	status, body, cookies, err := c.call(ctx, http.MethodPost, "v1/login/2fa", req, "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", twoFactorErr(status, body)
	}
	return sessionFrom(cookies)
}

// sessionFrom picks the core session out of a login answer: the core returns
// it only as a Set-Cookie header.
func sessionFrom(cookies []*http.Cookie) (string, error) {
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

// UpdateProfile proxies POST /v1/profile: the display name, country and date of birth of the
// signed-in account. The core keeps the current display name when it is sent empty; country
// and birthday are stored as sent, so an empty one clears it.
func (c *HTTPClient) UpdateProfile(ctx context.Context, coreSessionID, country, birthday, displayName string) error {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/profile", map[string]string{
		"country": country, "birthday": birthday, "display_name": displayName,
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

// Whoami proxies GET /v1/whoami: the account behind a core session (CR-4).
func (c *HTTPClient) Whoami(ctx context.Context, coreSessionID string) (Account, error) {
	code, body, _, err := c.call(ctx, http.MethodGet, "v1/whoami", nil, coreSessionID)
	if err != nil {
		return Account{}, err
	}
	if code == http.StatusUnauthorized {
		return Account{}, ErrSessionInvalid
	}
	if code != http.StatusOK {
		return Account{}, fmt.Errorf("core: whoami failed (status %d)", code)
	}
	a := Account{
		ID: str(body["account_id"]), Email: str(body["email"]), DisplayName: str(body["display_name"]), Handle: str(body["handle"]),
		Status: str(body["status"]),
	}
	a.EmailVerified, _ = body["email_verified"].(bool)
	a.TwoFactorEnabled, _ = body["two_factor_enabled"].(bool)
	if a.ID == "" {
		return Account{}, fmt.Errorf("core: whoami answered without an account id")
	}
	return a, nil
}

// ResendVerification proxies POST /v1/verify/resend. The core answers the same
// whether a mail went out, was throttled, or the address is already verified.
func (c *HTTPClient) ResendVerification(ctx context.Context, coreSessionID string) error {
	code, _, _, err := c.call(ctx, http.MethodPost, "v1/verify/resend", nil, coreSessionID)
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized {
		return ErrSessionInvalid
	}
	if code != http.StatusOK {
		return fmt.Errorf("core: resend failed (status %d)", code)
	}
	return nil
}

// ChangeEmail proxies POST /v1/email/change. The core never says whether the
// new address is taken.
func (c *HTTPClient) ChangeEmail(ctx context.Context, coreSessionID, newEmail, password string) error {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/email/change", map[string]string{
		"new_email": newEmail, "password": password,
	}, coreSessionID)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusForbidden:
		return ErrInvalidCredentials
	case http.StatusUnauthorized:
		return ErrSessionInvalid
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrConflict, coreMessage(body, "invalid email address"))
	default:
		return fmt.Errorf("core: email change failed (status %d)", code)
	}
}

// ConfirmEmail proxies POST /v1/email/confirm (JSON).
func (c *HTTPClient) ConfirmEmail(ctx context.Context, token string) error {
	code, _, _, err := c.call(ctx, http.MethodPost, "v1/email/confirm", map[string]string{"token": token}, "")
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return ErrNotFound
	default:
		return fmt.Errorf("core: email confirm failed (status %d)", code)
	}
}

// TwoFactorSetup proxies POST /v1/2fa/setup.
func (c *HTTPClient) TwoFactorSetup(ctx context.Context, coreSessionID, password string) (TwoFactorSecret, error) {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/2fa/setup", map[string]string{"password": password}, coreSessionID)
	if err != nil {
		return TwoFactorSecret{}, err
	}
	if code != http.StatusOK {
		return TwoFactorSecret{}, twoFactorErr(code, body)
	}
	return TwoFactorSecret{Secret: str(body["secret"]), OTPAuthURI: str(body["otpauth_uri"])}, nil
}

// TwoFactorEnable proxies POST /v1/2fa/enable.
func (c *HTTPClient) TwoFactorEnable(ctx context.Context, coreSessionID, totp string) ([]string, error) {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/2fa/enable", map[string]string{"code": totp}, coreSessionID)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, twoFactorErr(code, body)
	}
	raw, _ := body["recovery_codes"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s := str(v); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// TwoFactorDisable proxies POST /v1/2fa/disable.
func (c *HTTPClient) TwoFactorDisable(ctx context.Context, coreSessionID, password, totp string) error {
	code, body, _, err := c.call(ctx, http.MethodPost, "v1/2fa/disable", map[string]string{
		"password": password, "code": totp,
	}, coreSessionID)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return twoFactorErr(code, body)
	}
	return nil
}

// twoFactorErr maps the answers every 2FA route shares (core
// docs/two-factor.md): 403 wrong password, 401 wrong code or dead challenge
// (or, from the session check in front, a dead session), 409 wrong state,
// 429 locked.
func twoFactorErr(code int, body map[string]any) error {
	msg := coreMessage(body, "")
	switch code {
	case http.StatusForbidden:
		return ErrInvalidCredentials
	case http.StatusUnauthorized:
		if strings.Contains(msg, "session") && !strings.Contains(msg, "challenge") {
			return ErrSessionInvalid
		}
		return ErrInvalidCode
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", ErrConflict, msg)
	case http.StatusTooManyRequests:
		return ErrTwoFactorLocked
	default:
		return fmt.Errorf("core: two-factor request failed (status %d)", code)
	}
}

// coreMessage is the core's {"error": "..."} text, or def.
func coreMessage(body map[string]any, def string) string {
	if msg, ok := body["error"].(string); ok && msg != "" {
		return msg
	}
	return def
}
