// Package xbaccount is the website's client for xb-account, the original-Xbox identity service.
// The one thing the site does there is change the gamertag of the signed-in account
// (internal research notes, "Gamertag is its own field"). The service listens on
// a private mTLS address and accepts this call only from the SPIFFE identity
// spiffe://openpak/xb-provisioner, so the client certificate configured here must carry it.
// Reading the gamertag needs no call: it is the public code of the account's "xbox" link in core.
package xbaccount

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// The service's error codes, one sentinel each.
var (
	ErrInvalid       = errors.New("xbaccount: invalid_gamertag")
	ErrReserved      = errors.New("xbaccount: reserved_gamertag")
	ErrTaken         = errors.New("xbaccount: gamertag_taken")
	ErrConsoleBound  = errors.New("xbaccount: console_bound")
	ErrRequestReused = errors.New("xbaccount: request_id_reused")
	ErrNoIdentity    = errors.New("xbaccount: no_xbox_identity")
	// ErrUnavailable is also what any transport failure or unexpected answer maps to: the
	// change is then not known to have happened, and the page must not claim it did.
	ErrUnavailable = errors.New("xbaccount: unavailable")
)

var byCode = map[string]error{
	"invalid_gamertag":  ErrInvalid,
	"reserved_gamertag": ErrReserved,
	"gamertag_taken":    ErrTaken,
	"console_bound":     ErrConsoleBound,
	"request_id_reused": ErrRequestReused,
	"no_xbox_identity":  ErrNoIdentity,
}

// Result is the service's 200 answer.
type Result struct {
	XUID     string `json:"xuid"`
	Gamertag string `json:"gamertag"`
	Previous string `json:"previous"`
	Changed  bool   `json:"changed"`
}

// Client calls xb-account. A nil *Client means the integration is not configured.
type Client struct {
	base string
	http *http.Client
}

// New builds a client for base (https://host:port) that uses cfg for the TLS handshake.
func New(base string, cfg *tls.Config) *Client {
	return &Client{base: base, http: &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cfg},
		// A redirect would carry the request somewhere the certificate was not meant for.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// FromEnv reads XB_ACCOUNT_URL and the certificate files. An empty URL returns (nil, nil):
// the account page then offers no gamertag form. A set URL with unusable files is an error.
func FromEnv() (*Client, error) {
	base := os.Getenv("XB_ACCOUNT_URL")
	if base == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(os.Getenv("XB_ACCOUNT_CLIENT_CERT_FILE"), os.Getenv("XB_ACCOUNT_CLIENT_KEY_FILE"))
	if err != nil {
		return nil, fmt.Errorf("xbaccount: client certificate: %w", err)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair},
		ServerName: os.Getenv("XB_ACCOUNT_SERVER_NAME")}
	if f := os.Getenv("XB_ACCOUNT_CA_FILE"); f != "" {
		pem, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("xbaccount: CA file: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, errors.New("xbaccount: CA file holds no certificate")
		}
	}
	return New(base, cfg), nil
}

// SetGamertag changes the gamertag of coreAccountID, which the caller must have taken from the
// authenticated session. requestID makes a repeated submission a no-op; actor is the audit
// label. force_console_identity is never sent, so the service refuses a console-bound identity.
func (c *Client) SetGamertag(ctx context.Context, coreAccountID, gamertag, requestID, actor string) (Result, error) {
	if c == nil {
		return Result{}, ErrUnavailable
	}
	body, err := json.Marshal(struct {
		Gamertag  string `json:"gamertag"`
		RequestID string `json:"request_id,omitempty"`
		Actor     string `json:"actor"`
	}{gamertag, requestID, actor})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/internal/v1/accounts/"+url.PathEscape(coreAccountID)+"/gamertag", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode == http.StatusOK {
		var res Result
		if err := json.Unmarshal(raw, &res); err != nil || res.Gamertag == "" {
			return Result{}, fmt.Errorf("%w: unreadable answer", ErrUnavailable)
		}
		return res, nil
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	if sentinel, ok := byCode[e.Error]; ok {
		return Result{}, sentinel
	}
	return Result{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
}
