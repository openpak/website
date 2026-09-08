// Package push delivers notifications to companion apps.
//
// FCM's HTTP v1 API is a plain REST endpoint authenticated with an OAuth2
// access token, which is itself obtained by signing a JWT with the service
// account's key. That is two HTTP calls and some crypto from the standard
// library, so this package carries no Firebase SDK and no OAuth2 dependency.
//
// FCM reaches Android directly and iOS through APNs, which is why one transport
// covers both platforms — but the iOS half still needs an APNs key uploaded to
// the Firebase project, and that key comes from a paid Apple Developer account.
// Without it, iOS installs simply never receive anything.
package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	tokenURL   = "https://oauth2.googleapis.com/token"
	fcmScope   = "https://www.googleapis.com/auth/firebase.messaging"
	sendURLFmt = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
)

// ErrGone reports a token the transport says is dead: the app was uninstalled
// or the token rotated. The caller deletes it rather than retrying forever.
var ErrGone = errors.New("push: token no longer valid")

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// FCM sends through Firebase Cloud Messaging.
type FCM struct {
	sa   serviceAccount
	key  *rsa.PrivateKey
	http *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewFCM reads a service-account JSON file. An empty path is not an error: it
// means push is not configured, and the caller runs without a notifier rather
// than failing to start.
func NewFCM(path string) (*FCM, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("push: read service account: %w", err)
	}
	var sa serviceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("push: parse service account: %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("push: service account is missing project_id, client_email or private_key")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("push: service account private_key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("push: service account key is not RSA")
		}
		key = rk
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		return nil, errors.New("push: cannot parse the service account private key")
	}
	return &FCM{sa: sa, key: key, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// accessToken returns a cached token, refreshing a minute before expiry so a
// send never races the boundary.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expiry.Add(-time.Minute)) {
		return f.token, nil
	}
	now := time.Now()
	claims := map[string]any{
		"iss": f.sa.ClientEmail, "scope": fcmScope, "aud": tokenURL,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	assertion, err := signJWT(map[string]any{"alg": "RS256", "typ": "JWT"}, claims, f.key)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push: token exchange: %s: %s", resp.Status, truncate(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("push: token exchange returned no access token")
	}
	f.token = out.AccessToken
	f.expiry = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	return f.token, nil
}

// Send delivers one notification. data travels alongside the visible text so
// the app can open the right screen when it is tapped.
func (f *FCM) Send(ctx context.Context, token, title, body string, data map[string]string) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	msg := map[string]any{"message": map[string]any{
		"token":        token,
		"notification": map[string]any{"title": title, "body": body},
		"data":         data,
	}}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf(sendURLFmt, f.sa.ProjectID), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	// A dead install: the token is gone for good, so the caller forgets it
	// instead of retrying it on every future notification.
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	default:
		return fmt.Errorf("push: send: %s: %s", resp.Status, truncate(respBody))
	}
}

func signJWT(header, claims map[string]any, key *rsa.PrivateKey) (string, error) {
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := b64(hb) + "." + b64(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// truncate keeps an upstream error readable in a log line without pasting a
// whole error document into it.
func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
