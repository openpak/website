package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/wfc"
)

// The account actions the website had and the app did not: creating an account, the
// forgotten-password mail and the reset, changing the password, deleting the account, linking
// a Wii/DS and unlinking a console.

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
		Country     string `json:"country"`
		Birthday    string `json:"birthday"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if err := s.Core.RegisterWithProfile(r.Context(), email, req.Password,
		strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Country), strings.TrimSpace(req.Birthday)); err != nil {
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
		Email string `json:"email"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	_ = s.Core.ForgotPassword(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
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

// handleDeleteAccount: DELETE /api/v1/me {"password"}. The password is asked for again so a
// bearer lifted off a phone cannot end the account; every bearer of the account, this one
// included, stops working.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	err := accounts.Delete(r.Context(), s.Identity, s.DB, tok.AccountID, req.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
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
