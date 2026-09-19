package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeCoreHTTP answers the v0.9.0 routes the way the account core does
// (internal/web/server.go in the account repo).
func fakeCoreHTTP(t *testing.T) *HTTPClient {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /v1/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Email string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Email == "2fa@x.test" {
			write(w, http.StatusAccepted, map[string]any{"two_factor_required": true, "challenge": "ch-1"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "openpak_session", Value: "sess-1"})
		write(w, http.StatusOK, map[string]string{"message": "signed in", "account_id": "acct-1"})
	})
	mux.HandleFunc("POST /v1/login/2fa", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Challenge    string `json:"challenge"`
			Code         string `json:"code"`
			RecoveryCode string `json:"recovery_code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case req.Challenge != "ch-1":
			write(w, http.StatusUnauthorized, map[string]string{"error": "sign-in challenge is invalid or expired; sign in again"})
		case req.Code == "999999":
			w.Header().Set("Retry-After", "900")
			write(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts; try again in 15 minutes"})
		case req.Code != "123456" && req.RecoveryCode != "abcde-fghij":
			write(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
		default:
			http.SetCookie(w, &http.Cookie{Name: "openpak_session", Value: "sess-2"})
			write(w, http.StatusOK, map[string]string{"message": "signed in", "account_id": "acct-2"})
		}
	})
	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("openpak_session"); err != nil || c.Value != "sess-1" {
			write(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired session"})
			return
		}
		write(w, http.StatusOK, map[string]any{"account_id": "acct-1", "email": "a@x.test", "display_name": "Leia",
			"status": "active", "email_verified": true, "two_factor_enabled": false})
	})
	mux.HandleFunc("POST /v1/2fa/setup", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusForbidden, map[string]string{"error": "password is incorrect"})
	})
	mux.HandleFunc("POST /v1/2fa/enable", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusOK, map[string]any{"recovery_codes": []string{"a", "b"}})
	})
	mux.HandleFunc("POST /v1/2fa/disable", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusConflict, map[string]string{"error": "two-factor authentication is not enabled"})
	})
	mux.HandleFunc("POST /v1/email/confirm", func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusBadRequest, map[string]string{"error": "confirmation link is invalid or expired"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := NewHTTP(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoginTwoFactorAndWhoami(t *testing.T) {
	c := fakeCoreHTTP(t)
	ctx := context.Background()

	sid, err := c.Login(ctx, "a@x.test", "pw")
	if err != nil || sid != "sess-1" {
		t.Fatalf("login: %q %v", sid, err)
	}
	me, err := c.Whoami(ctx, sid)
	if err != nil || me.ID != "acct-1" || me.Email != "a@x.test" || me.DisplayName != "Leia" || !me.EmailVerified || me.Status != "active" {
		t.Fatalf("whoami: %+v %v", me, err)
	}
	if _, err := c.Whoami(ctx, "dead"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("whoami on a dead session: %v", err)
	}

	_, err = c.Login(ctx, "2fa@x.test", "pw")
	var ch *LoginChallenge
	if !errors.As(err, &ch) || ch.Challenge != "ch-1" {
		t.Fatalf("2fa login: %v", err)
	}
	for _, tc := range []struct {
		challenge, code, recovery string
		want                      error
	}{
		{"ch-x", "123456", "", ErrInvalidCode},
		{"ch-1", "000000", "", ErrInvalidCode},
		{"ch-1", "999999", "", ErrTwoFactorLocked},
	} {
		if _, err := c.Login2FA(ctx, tc.challenge, tc.code, tc.recovery); !errors.Is(err, tc.want) {
			t.Errorf("%+v: got %v", tc, err)
		}
	}
	if sid, err := c.Login2FA(ctx, "ch-1", "", "abcde-fghij"); err != nil || sid != "sess-2" {
		t.Fatalf("recovery code: %q %v", sid, err)
	}
}

func TestTwoFactorAndEmailErrors(t *testing.T) {
	c := fakeCoreHTTP(t)
	ctx := context.Background()
	if _, err := c.TwoFactorSetup(ctx, "s", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("setup: %v", err)
	}
	if codes, err := c.TwoFactorEnable(ctx, "s", "123456"); err != nil || len(codes) != 2 {
		t.Errorf("enable: %v %v", codes, err)
	}
	if err := c.TwoFactorDisable(ctx, "s", "pw", "1"); !errors.Is(err, ErrConflict) {
		t.Errorf("disable: %v", err)
	}
	if err := c.ConfirmEmail(ctx, "bad"); !errors.Is(err, ErrNotFound) {
		t.Errorf("confirm: %v", err)
	}
}

func TestWithLangSendsAcceptLanguage(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Language")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Register(WithLang(context.Background(), "pt-BR"), "a@x.test", "pw", "A")
	if got != "pt-BR" {
		t.Errorf("Accept-Language %q", got)
	}
}
