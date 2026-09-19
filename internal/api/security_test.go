package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/news"
)

// mint signs a@x.test in and returns the bearer.
func mint(t *testing.T, s *Server) string {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Token
}

// A 2FA account's password answers 202 with a challenge and no bearer;
// /token/2fa redeems it with a code or a recovery code.
func TestTokenWithTwoFactor(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.twoFactor["a@x.test"] = true

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var ch struct {
		TwoFactorRequired bool   `json:"two_factor_required"`
		Challenge         string `json:"challenge"`
		Token             string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if rec.Code != http.StatusAccepted || !ch.TwoFactorRequired || ch.Challenge == "" || ch.Token != "" {
		t.Fatalf("password step: %d %s", rec.Code, rec.Body.String())
	}
	if len(fc.sessions) != 0 {
		t.Fatal("a core session exists before the second factor")
	}

	rec = do(t, s, http.MethodPost, "/api/v1/token/2fa", "", map[string]string{"challenge": ch.Challenge, "code": "000000"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/api/v1/token/2fa", "", map[string]string{"challenge": ch.Challenge})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no code: %d", rec.Code)
	}
	for _, body := range []map[string]string{
		{"challenge": ch.Challenge, "code": fakeTOTP},
		{"challenge": ch.Challenge, "recovery_code": fakeRecovery},
	} {
		rec = do(t, s, http.MethodPost, "/api/v1/token/2fa", "", body)
		var tok struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &tok)
		if rec.Code != http.StatusCreated || !strings.HasPrefix(tok.Token, "opk_") {
			t.Fatalf("%v: %d %s", body, rec.Code, rec.Body.String())
		}
		me := do(t, s, http.MethodGet, "/api/v1/me", tok.Token, nil)
		if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"two_factor_enabled":true`) {
			t.Fatalf("me: %d %s", me.Code, me.Body.String())
		}
	}
}

// DELETE /me on a 2FA account needs a code on top of the password.
func TestDeleteAccountStepUpWithTwoFactor(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	tok := mint(t, s)
	fc.twoFactor["a@x.test"] = true

	rec := do(t, s, http.MethodDelete, "/api/v1/me", tok, map[string]string{"password": "password-A-1"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"two_factor_required":true`) || len(fc.deleted) != 0 {
		t.Fatalf("no code: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodDelete, "/api/v1/me", tok, map[string]string{"password": "password-A-1", "code": "999999"})
	if rec.Code != http.StatusForbidden || len(fc.deleted) != 0 {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	rec = do(t, s, http.MethodDelete, "/api/v1/me", tok, map[string]string{"password": "password-A-1", "recovery_code": fakeRecovery})
	if rec.Code != http.StatusOK || len(fc.deleted) != 1 {
		t.Fatalf("with a recovery code: %d %v", rec.Code, fc.deleted)
	}
}

func TestTwoFactorEnrolmentAPI(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	tok := mint(t, s)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/2fa/setup", tok, map[string]string{"password": "nope"}); rec.Code != http.StatusForbidden {
		t.Fatalf("setup, wrong password: %d", rec.Code)
	}
	rec := do(t, s, http.MethodPost, "/api/v1/me/2fa/setup", tok, map[string]string{"password": "password-A-1"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"otpauth_uri":"otpauth://`) || !strings.Contains(rec.Body.String(), `"secret":`) {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/2fa/enable", tok, map[string]string{"code": "1"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("enable, wrong code: %d", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/api/v1/me/2fa/enable", tok, map[string]string{"code": fakeTOTP})
	var codes struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &codes)
	if rec.Code != http.StatusOK || len(codes.RecoveryCodes) != 10 {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodPost, "/api/v1/me/2fa/disable", tok, map[string]string{"password": "password-A-1", "code": fakeRecovery})
	if rec.Code != http.StatusOK || fc.twoFactor["a@x.test"] {
		t.Fatalf("disable: %d", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/api/v1/me/2fa/disable", tok, map[string]string{"password": "password-A-1", "code": fakeTOTP})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not enabled") {
		t.Fatalf("disable twice: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEmailChangeResendAndConfirmAPI(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	tok := mint(t, s)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/email", tok, map[string]string{"new_email": "n@x.test", "password": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec := do(t, s, http.MethodPost, "/api/v1/me/email", tok, map[string]string{"new_email": " N@X.test ", "password": "password-A-1"})
	if rec.Code != http.StatusAccepted || fc.newEmail != "n@x.test" {
		t.Fatalf("change: %d %q", rec.Code, fc.newEmail)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/email/confirm", "", map[string]string{"token": "bad"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad token: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/email/confirm", "", map[string]string{"token": "email-tok"}); rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/verify/resend", tok, nil); rec.Code != http.StatusOK || fc.resent != 1 {
		t.Fatalf("resend: %d %d", rec.Code, fc.resent)
	}
}

func TestBlockedListAPI(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := mint(t, s)
	rec := do(t, s, http.MethodGet, "/api/v1/me/blocked", tok, nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"blocked":[]}` {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body.String())
	}
	do(t, s, http.MethodPost, "/api/v1/me/friends/block", tok, map[string]string{"account_id": "acct-b@x.test"})
	rec = do(t, s, http.MethodGet, "/api/v1/me/blocked", tok, nil)
	if !strings.Contains(rec.Body.String(), `"account_id":"acct-b@x.test"`) || !strings.Contains(rec.Body.String(), `"blocked_at":"2023-11-14T22:13:20Z"`) {
		t.Fatalf("listed: %s", rec.Body.String())
	}
}

// A code the adapter published on the link wins over every fallback.
func TestFriendRequestByPublishedCodeAPI(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.publicCodes["SW-9999-8888-7777"] = "acct-b@x.test"
	tok := mint(t, s)
	rec := do(t, s, http.MethodPost, "/api/v1/me/friends/requests", tok, map[string]string{"friend_code": "SW-9999-8888-7777"})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "acct-b@x.test") {
		t.Fatalf("request: %d %s", rec.Code, rec.Body.String())
	}
}

// /news is public: empty when no news service is configured, the latest
// articles when it is.
func TestNewsAPI(t *testing.T) {
	s, _, _ := newTestAPI(t)
	rec := do(t, s, http.MethodGet, "/api/v1/news", "", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"articles":[]}` {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body.String())
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"articles":[
			{"slug":"two","published_at":"2026-09-02T10:00:00Z","locales":[{"language":"en","title":"Second","body":"B1\n\nB2"}]},
			{"slug":"one","published_at":"2026-09-01T10:00:00Z","locales":[{"language":"en","title":"First","body":"A"}]}]}`))
	}))
	defer srv.Close()
	s.News = news.NewFeed(srv.URL)
	rec = do(t, s, http.MethodGet, "/api/v1/news?limit=1", "", nil)
	var out struct {
		Articles []struct {
			Slug, Title, Summary, Body string
			PublishedAt                time.Time `json:"published_at"`
		} `json:"articles"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || len(out.Articles) != 1 || out.Articles[0].Slug != "two" || out.Articles[0].Summary != "B1" {
		t.Fatalf("news: %d %s", rec.Code, rec.Body.String())
	}
}
