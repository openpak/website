package pages

import (
	"errors"
	"net/http"
	"strings"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
)

// The account's security: the email address, its verification, and
// two-factor authentication. Every step is a form post; nothing needs script.

// handleEmailChange starts moving the account to a new address. The core asks
// for the password and mails the new address a link that lands on
// /email/confirm; it never says whether the address is taken.
func (s *Server) handleEmailChange(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	err := s.Core.ChangeEmail(r.Context(), v.CoreSessionID, normalizeEmail(r.PostFormValue("new_email")), r.PostFormValue("password"))
	switch {
	case err == nil:
		redirectMsg(w, r, "/account", "email-sent")
	case errors.Is(err, core.ErrInvalidCredentials):
		redirectMsg(w, r, "/account", "email-wrongpassword")
	case errors.Is(err, core.ErrConflict):
		redirectMsg(w, r, "/account", "email-invalid")
	default:
		s.log.Error("email change", "err", err)
		http.Error(w, "could not start the change", http.StatusBadGateway)
	}
}

// handleVerifyResend asks the core for a fresh verification link. The answer
// is the same whatever happened (sent, throttled, already verified).
func (s *Server) handleVerifyResend(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if accounts.MailAllowed(s.MailLimit, v.Email) {
		if err := s.Core.ResendVerification(r.Context(), v.CoreSessionID); err != nil {
			s.log.Error("verify resend", "err", err)
		}
	}
	redirectMsg(w, r, "/account", "verify-sent")
}

type emailConfirmView struct {
	baseData
	Token string
}

// handleEmailConfirmForm is where the mailed link lands. It only shows a
// button: a mail scanner that fetches the link changes nothing.
func (s *Server) handleEmailConfirmForm(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	token := r.URL.Query().Get("token")
	if token == "" {
		s.renderMessage(w, r, v, http.StatusBadRequest, "auth.email.confirm.title", "auth.email.confirm.bad")
		return
	}
	s.render(w, http.StatusOK, "email_confirm.html", emailConfirmView{s.data(r, v, i18n.T(i18n.FromRequest(r), "auth.email.confirm.title")), token})
}

// handleEmailConfirmSubmit applies the change. The token is the credential,
// so this works signed in or not; a signed-in viewer's cached address is
// refreshed so the header does not show the old one.
func (s *Server) handleEmailConfirmSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	v := s.viewerOf(w, r)
	if err := s.Core.ConfirmEmail(r.Context(), r.PostFormValue("token")); err != nil {
		if !errors.Is(err, core.ErrNotFound) {
			s.log.Error("email confirm", "err", err)
		}
		s.renderMessage(w, r, v, http.StatusBadRequest, "auth.email.confirm.title", "auth.email.confirm.bad")
		return
	}
	if v != nil {
		s.refreshSession(r, v)
	}
	s.renderMessage(w, r, v, http.StatusOK, "auth.email.confirm.title", "auth.email.confirm.ok")
}

// refreshSession re-reads the account behind the viewer's session into the
// cached row (address and name shown in the header).
func (s *Server) refreshSession(r *http.Request, v *viewer) {
	acct, err := s.Core.Whoami(r.Context(), v.CoreSessionID)
	if err != nil {
		return
	}
	if acct.Email == v.Email && acct.DisplayName == v.DisplayName {
		return
	}
	v.WebSession.Email, v.WebSession.DisplayName = acct.Email, acct.DisplayName
	if err := s.DB.PutWebSession(r.Context(), v.WebSession); err != nil {
		s.log.Error("refresh session", "err", err)
	}
}

func (s *Server) renderMessage(w http.ResponseWriter, r *http.Request, v *viewer, status int, titleKey, msgKey string) {
	s.render(w, status, "message.html", struct {
		baseData
		Message string
	}{s.data(r, v, i18n.T(i18n.FromRequest(r), titleKey)), i18n.T(i18n.FromRequest(r), msgKey)})
}

// twoFactorView is the enrolment page: the secret to type into an
// authenticator app, then the ten recovery codes, once.
type twoFactorView struct {
	baseData
	Secret, URI   string
	Error         string
	RecoveryCodes []string
}

// handle2FASetup starts enrolment. The password is asked for again: a
// borrowed session must not tie the account to someone else's phone.
func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	sec, err := s.Core.TwoFactorSetup(r.Context(), v.CoreSessionID, r.PostFormValue("password"))
	if err != nil {
		s.twoFactorFailed(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "account_2fa.html", twoFactorView{
		baseData: s.data(r, v, i18n.T(i18n.FromRequest(r), "account.2fa.title")), Secret: sec.Secret, URI: sec.OTPAuthURI})
}

// handle2FAEnable finishes enrolment with a first code and shows the recovery
// codes. A wrong code shows the same secret again: it is the viewer's own,
// already on their screen, and the core still holds it as pending.
func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	codes, err := s.Core.TwoFactorEnable(r.Context(), v.CoreSessionID, strings.ReplaceAll(r.PostFormValue("code"), " ", ""))
	if errors.Is(err, core.ErrInvalidCode) {
		s.render(w, http.StatusUnauthorized, "account_2fa.html", twoFactorView{
			baseData: s.data(r, v, i18n.T(i18n.FromRequest(r), "account.2fa.title")),
			Secret:   r.PostFormValue("secret"), URI: r.PostFormValue("uri"),
			Error: i18n.T(i18n.FromRequest(r), "account.2fa.wrongcode")})
		return
	}
	if err != nil {
		s.twoFactorFailed(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "account_2fa.html", twoFactorView{
		baseData: s.data(r, v, i18n.T(i18n.FromRequest(r), "account.2fa.title")), RecoveryCodes: codes})
}

// handle2FADisable turns 2FA off with the password and a current code (or a
// recovery code, for someone who lost the phone).
func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	err := s.Core.TwoFactorDisable(r.Context(), v.CoreSessionID, r.PostFormValue("password"), strings.TrimSpace(r.PostFormValue("code")))
	if err != nil {
		s.twoFactorFailed(w, r, err)
		return
	}
	redirectMsg(w, r, "/account", "2fa-disabled")
}

// twoFactorFailed maps the outcomes the three 2FA forms share back to the
// account page.
func (s *Server) twoFactorFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, core.ErrInvalidCredentials):
		redirectMsg(w, r, "/account", "2fa-wrongpassword")
	case errors.Is(err, core.ErrInvalidCode):
		redirectMsg(w, r, "/account", "2fa-wrongcode")
	case errors.Is(err, core.ErrTwoFactorLocked):
		redirectMsg(w, r, "/account", "2fa-locked")
	case errors.Is(err, core.ErrConflict):
		redirectMsg(w, r, "/account", "2fa-state")
	default:
		s.log.Error("two-factor", "err", err)
		http.Error(w, "the account service did not answer", http.StatusBadGateway)
	}
}
