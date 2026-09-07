// Package session manages the website's cookie session — a wrapper around a
// core web session id (WEB-02) — plus CSRF tokens derived from the session
// secret, and bearer-token hashing for the mobile API.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

// Manager issues and validates the website session cookie.
type Manager struct {
	CookieName string
	Secret     []byte
	Secure     bool // Secure attribute; true in production
	TTL        time.Duration
}

// New builds a Manager. secret must be >=32 bytes (enforced by config).
func New(cookieName, secret string, secure bool, ttl time.Duration) *Manager {
	return &Manager{CookieName: cookieName, Secret: []byte(secret), Secure: secure, TTL: ttl}
}

// SetCookie writes the session cookie holding the core session id.
func (m *Manager) SetCookie(w http.ResponseWriter, coreSessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.CookieName,
		Value:    coreSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.TTL.Seconds()),
	})
}

// ClearCookie expires the session cookie.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: m.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// CoreSessionID extracts the core session id from the request cookie, if any.
func (m *Manager) CoreSessionID(r *http.Request) string {
	c, err := r.Cookie(m.CookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// CSRFToken derives a per-session CSRF token via HMAC. Stateless and bound
// to the live core session id; empty session means no token.
func (m *Manager) CSRFToken(coreSessionID string) string {
	if coreSessionID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, append([]byte("csrf:"), m.Secret...))
	mac.Write([]byte(coreSessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CheckCSRF reports whether the submitted token matches the session.
func (m *Manager) CheckCSRF(coreSessionID, token string) bool {
	if coreSessionID == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(m.CSRFToken(coreSessionID)), []byte(token))
}

// HashToken is the storage form of bearer tokens (mobile API): salted with
// the site secret and SHA-256 hashed. Raw tokens never touch the database.
func (m *Manager) HashToken(raw string) string {
	mac := hmac.New(sha256.New, append([]byte("bearer:"), m.Secret...))
	mac.Write([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// NewBearer mints a random bearer token.
func NewBearer() (raw string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	return "opk_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// BearerFromRequest extracts an Authorization: Bearer token.
func BearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}
