package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// The account's security, the block list and the news feed: what the website
// gained with the account core's v0.9.0 contracts, with the same rules on the
// app's side.
func (s *Server) registerSecurity(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }

	h("/me/2fa/setup", s.auth(s.handle2FASetup))
	h("/me/2fa/enable", s.auth(s.handle2FAEnable))
	h("/me/2fa/disable", s.auth(s.handle2FADisable))
	h("/me/email", s.auth(s.handleEmailChange))
	h("/me/verify/resend", s.auth(s.handleVerifyResend))
	g("/me/blocked", s.auth(s.handleBlocked))
	// Public: the mailed link's token is the credential, as on the website.
	h("/email/confirm", s.handleEmailConfirm)
	// Public: the published news, for the app's home screen.
	g("/news", s.handleNews)
}

// writeTwoFactorError answers the outcomes the 2FA routes share.
func (s *Server) writeTwoFactorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
	case errors.Is(err, core.ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, "invalid code")
	case errors.Is(err, core.ErrTwoFactorLocked):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many wrong codes; try again in 15 minutes")
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), core.ErrConflict.Error()+": "))
	default:
		s.log.Error("api two-factor", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
	}
}

// handle2FASetup: POST /api/v1/me/2fa/setup {"password"} → {secret, otpauth_uri}.
func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	sec, err := s.Core.TwoFactorSetup(r.Context(), tok.CoreSessionID, req.Password)
	if err != nil {
		s.writeTwoFactorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": sec.Secret, "otpauth_uri": sec.OTPAuthURI})
}

// handle2FAEnable: POST /api/v1/me/2fa/enable {"code"} → {recovery_codes}, shown once.
func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	codes, err := s.Core.TwoFactorEnable(r.Context(), tok.CoreSessionID, strings.TrimSpace(req.Code))
	if err != nil {
		s.writeTwoFactorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handle2FADisable: POST /api/v1/me/2fa/disable {"password", "code"}; the code may be a
// recovery code.
func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Core.TwoFactorDisable(r.Context(), tok.CoreSessionID, req.Password, strings.TrimSpace(req.Code)); err != nil {
		s.writeTwoFactorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disabled": true})
}

// handleEmailChange: POST /api/v1/me/email {"new_email", "password"}. The link goes to the new
// address; the answer never says whether that address is taken.
func (s *Server) handleEmailChange(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		NewEmail string `json:"new_email"`
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	err := s.Core.ChangeEmail(r.Context(), tok.CoreSessionID, strings.ToLower(strings.TrimSpace(req.NewEmail)), req.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "if the address can be used, a confirmation link has been sent to it"})
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), core.ErrConflict.Error()+": "))
	default:
		s.log.Error("api email change", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
	}
}

// handleEmailConfirm: POST /api/v1/email/confirm {"token"}, for an app that opens the mailed
// link itself.
func (s *Server) handleEmailConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	err := s.Core.ConfirmEmail(r.Context(), req.Token)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"changed": true})
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusBadRequest, "the confirmation link is invalid or has expired")
	default:
		s.log.Error("api email confirm", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
	}
}

// handleVerifyResend: POST /api/v1/me/verify/resend. Always the same answer; the core sends at
// most one link per 10 minutes.
func (s *Server) handleVerifyResend(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if accounts.MailAllowed(s.MailLimit, "acct:"+tok.AccountID) {
		if err := s.Core.ResendVerification(r.Context(), tok.CoreSessionID); err != nil {
			s.log.Error("api verify resend", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "if the address still needs confirming, a new link has been sent"})
}

// handleBlocked: GET /api/v1/me/blocked. Unblocking is POST /me/friends/unblock.
func (s *Server) handleBlocked(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	blocked, err := s.Social.ListBlocks(r.Context(), tok.AccountID)
	if err != nil {
		s.log.Error("api list blocks", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(blocked))
	for _, b := range blocked {
		out = append(out, map[string]any{
			"account_id": b.AccountID, "display_name": b.DisplayName,
			"blocked_at": b.BlockedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocked": out})
}

// handleNews: GET /api/v1/news?limit=N → the latest published articles, newest first. Public,
// on the same per-address budget as /titles. No news service configured is an empty list, not
// an error: the app simply has nothing to show.
func (s *Server) handleNews(w http.ResponseWriter, r *http.Request) {
	if !s.PerToken.Allow("ip:" + clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	limit := 10
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 50)
	}
	out := []map[string]any{}
	for _, a := range s.News.Latest(r.Context(), limit) {
		out = append(out, map[string]any{
			"slug": a.Slug, "title": a.Title, "summary": a.Summary(), "body": a.Body,
			"published_at": a.Published.UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{"articles": out})
}
