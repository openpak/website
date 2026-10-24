package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/turnstile"
	"github.com/openpak/website/internal/wfc"
)

// The account actions the website had and the app did not: creating an account, the
// forgotten-password mail and the reset, changing the password, deleting the account, linking
// a Wii/DS and unlinking a console.

// handleAuthConfig: GET /api/v1/auth/config, public. What the app needs to know before it
// shows the signup and reset forms: whether they want a Turnstile answer, and where the
// widget page is. The widget page is on this site, so its hostname is the one the
// Turnstile widget already allows.
func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	ts := map[string]any{"enabled": s.Turnstile != nil}
	if s.Turnstile != nil {
		ts["site_key"] = s.Turnstile.Key()
		ts["widget_path"] = "/turnstile-widget"
	}
	writeJSON(w, http.StatusOK, map[string]any{"turnstile": ts})
}

// humanCheck verifies the request's turnstile_token when Turnstile is configured, the
// check the website's forms make, with the same client address, and answers the request
// itself when it fails. Cloudflare not answering is a refusal too: the check fails closed.
func (s *Server) humanCheck(w http.ResponseWriter, r *http.Request, token string) bool {
	err := s.Turnstile.Verify(r.Context(), token, clientKey(r))
	switch {
	case err == nil:
		return true
	case errors.Is(err, turnstile.ErrFailed):
		writeError(w, http.StatusBadRequest, "the human check did not pass; try again")
	default:
		s.log.Error("turnstile", "err", err)
		writeError(w, http.StatusServiceUnavailable, "the human check is not answering; try again")
	}
	return false
}

// handleRegister: POST /api/v1/register. The same answer whether or not the email is
// taken; the core resends to a taken-but-unverified address, so nothing is enumerable.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	if cfg, err := s.DB.GetSiteConfig(r.Context()); err == nil && !cfg.RegistrationsOpen {
		writeError(w, http.StatusForbidden, "registrations are closed")
		return
	}
	var req struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Handle      string `json:"handle"`
		Country     string `json:"country"`
		Birthday    string `json:"birthday"`
		Turnstile   string `json:"turnstile_token"`
	}
	if !decodeSmall(w, r, &req) || !s.humanCheck(w, r, req.Turnstile) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if err := accounts.CheckAge(req.Birthday, s.MinAge, time.Now()); err != nil {
		msg := "birthday must be a past date, YYYY-MM-DD"
		if errors.Is(err, accounts.ErrTooYoung) {
			msg = fmt.Sprintf("you must be at least %d to create an OpenPak account", s.MinAge)
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// Same answer past the per-address mail budget, without the mail.
	if !accounts.MailAllowed(s.MailLimit, email) {
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "if the address can be registered, a verification mail is on its way"})
		return
	}
	if err := s.Core.RegisterWithProfile(r.Context(), email, req.Password,
		strings.TrimSpace(req.DisplayName), req.Handle, strings.TrimSpace(req.Country), strings.TrimSpace(req.Birthday)); err != nil {
		if core.HandleErrorMessage(err) != "" {
			code := http.StatusBadRequest
			if errors.Is(err, core.ErrHandleTaken) {
				code = http.StatusConflict
			}
			writeError(w, code, err.Error())
			return
		}
		s.log.Info("api register rejected", "err", err)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "if the address can be registered, a verification mail is on its way"})
}

// handleForgotPassword: POST /api/v1/password/forgot. The mail links to the website's
// reset page; an app that opens that link itself finishes with /password/reset.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	var req struct {
		Email     string `json:"email"`
		Turnstile string `json:"turnstile_token"`
	}
	if !decodeSmall(w, r, &req) || !s.humanCheck(w, r, req.Turnstile) {
		return
	}
	if email := strings.ToLower(strings.TrimSpace(req.Email)); accounts.MailAllowed(s.MailLimit, email) {
		_ = s.Core.ForgotPassword(r.Context(), email)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "if the address has an account, a reset mail is on its way"})
}

// handleResetPassword: POST /api/v1/password/reset {"token","password"}, the same step as the
// website's reset page. The core revokes every session of the account, and with them every
// bearer, so the app signs in again afterwards.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Token == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "token and password are required")
		return
	}
	if err := s.Core.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "the reset link is invalid or has expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reset": true})
}

// handleChangePassword: POST /api/v1/me/password. The core checks the current password and
// revokes the account's other sessions, and with them every other bearer.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Current == "" || req.New == "" {
		writeError(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}
	if err := s.Core.ChangePassword(r.Context(), tok.CoreSessionID, req.Current, req.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

// handleDeleteAccount: DELETE /api/v1/me {"password", "code"?}. The password is asked for again
// so a bearer lifted off a phone cannot end the account, and an account with two-factor
// authentication also needs a current code (or a recovery code, as "code" or "recovery_code").
// Every bearer of the account, this one included, stops working.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Password     string `json:"password"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	second := req.Code
	if second == "" {
		second = req.RecoveryCode
	}
	err := accounts.Delete(r.Context(), s.Identity, s.DB, tok.AccountID, req.Password, second)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
	case errors.Is(err, core.ErrSecondFactorRequired):
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this account has two-factor authentication: send a code", "code": "two_factor_required", "two_factor_required": true})
	case errors.Is(err, core.ErrInvalidCode):
		writeError(w, http.StatusForbidden, "wrong code")
	case errors.Is(err, core.ErrTwoFactorLocked):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many wrong codes; try again in 15 minutes")
	default:
		s.log.Error("api delete account", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// handleLinkWFC: POST /api/v1/me/links/wfc {"friend_code"}. A Wii or DS cannot sign in, so
// it is linked from a friend code read off a game, as on the website.
func (s *Server) handleLinkWFC(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		FriendCode string `json:"friend_code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if s.WFC == nil {
		writeError(w, http.StatusServiceUnavailable, "Wii/DS linking is not available")
		return
	}
	err := accounts.LinkWFC(r.Context(), s.WFC, s.Links, tok.AccountID, req.FriendCode)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]any{"namespace": "wfc", "linked": true})
	case errors.Is(err, wfc.ErrNotFound):
		writeError(w, http.StatusNotFound, "no Wii/DS profile has that friend code")
	case errors.Is(err, accounts.ErrWFCUnreachable):
		s.log.Error("api wfc resolve", "err", err)
		writeError(w, http.StatusBadGateway, "could not reach the Wii/DS server")
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusConflict, "that console is linked to another account")
	default:
		s.log.Error("api wfc link", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// handleUnlink: DELETE /api/v1/me/links/{namespace}. Addressed by namespace, not link id, so
// the link revoked is always one the token's own account holds.
func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	link, err := s.Links.ActiveLinkByAccount(r.Context(), r.PathValue("namespace"), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no console linked in that namespace")
		return
	}
	if err := s.Links.Unlink(r.Context(), link.LinkID, "unlink:"+tok.AccountID+":"+link.LinkID); err != nil {
		s.log.Error("api unlink", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unlinked": true})
}

// uploadAvatar is the multipart half of POST /api/v1/me/avatar: field "avatar", a PNG or
// JPEG up to media.MaxUploadBytes.
func (s *Server) uploadAvatar(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes+64<<10)
	file, _, err := r.FormFile("avatar")
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload a PNG or JPEG up to 4 MiB in the avatar field")
		return
	}
	defer file.Close()
	if err := s.Media.StoreAvatar(r.Context(), tok.AccountID, file); err != nil {
		if errors.Is(err, media.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "upload a PNG or JPEG up to 4 MiB")
			return
		}
		s.log.Error("api store avatar", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.stampAvatar(r, tok.AccountID)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// stampAvatar bumps the media row the adapter builds the console's ?v= cache key from;
// without it a console keeps showing the old picture (CR-2).
func (s *Server) stampAvatar(r *http.Request, accountID string) {
	if err := s.DB.UpsertMedia(r.Context(), store.MediaRecord{
		AccountID: accountID, Kind: "avatar", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.log.Error("api media metadata", "err", err)
	}
}
