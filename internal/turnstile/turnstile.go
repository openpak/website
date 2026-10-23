// Package turnstile checks Cloudflare Turnstile answers on the forms bots go
// for: creating an account and asking for a password reset.
//
// A nil *Verifier is "not configured": Verify passes everything, so development
// and tests run without a Cloudflare account. A configured one fails closed:
// if Cloudflare cannot be asked, the form is refused.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FormField is the name the widget gives its hidden answer field.
const FormField = "cf-turnstile-response"

// SiteVerifyURL is Cloudflare's server-side check.
const SiteVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// ErrFailed is an answer Cloudflare did not accept, or no answer at all.
var ErrFailed = errors.New("turnstile: challenge not passed")

// Verifier holds the site's key pair.
type Verifier struct {
	SiteKey string // public, rendered into the widget
	Secret  string
	URL     string // SiteVerifyURL unless a test points it elsewhere
	HTTP    *http.Client
}

// New returns nil (checks off) unless both keys are set.
func New(siteKey, secret string) *Verifier {
	if siteKey == "" || secret == "" {
		return nil
	}
	return &Verifier{SiteKey: siteKey, Secret: secret, URL: SiteVerifyURL,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Key is the site key for the template; "" draws no widget.
func (v *Verifier) Key() string {
	if v == nil {
		return ""
	}
	return v.SiteKey
}

// Verify asks Cloudflare whether token is a passed challenge from remoteIP.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if v == nil {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrFailed
	}
	form := url.Values{"secret": {v.Secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: siteverify answered %d", resp.StatusCode)
	}
	var out struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	if !out.Success {
		return fmt.Errorf("%w: %s", ErrFailed, strings.Join(out.ErrorCodes, ","))
	}
	return nil
}
