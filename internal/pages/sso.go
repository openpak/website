package pages

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
)

// Single sign-on for sites on other openpak.org subdomains; see package sso for the model.
//
//	GET  /account/sso/authorize?client=&redirect_uri=&state=[&code_challenge=&code_challenge_method=S256]
//	POST /internal/sso/token    (server to server, client secret)
//	GET  /account/sso/logout?redirect_uri=   (confirm page) and POST (clears this site's session)

func (s *Server) ssoOn() bool { return s.SSO.Enabled() }

// ssoError tells the person what is wrong with the link. It never redirects: until the
// redirect_uri has been checked there is nowhere safe to send them.
func (s *Server) ssoError(w http.ResponseWriter, r *http.Request, status int) {
	lang := i18n.FromRequest(r)
	s.render(w, status, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, i18n.T(lang, "sso.title")), i18n.T(lang, "sso.bad")})
}

func (s *Server) handleSSOAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn() {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	client, ok := s.SSO.Client(q.Get("client"))
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	switch {
	case !ok, !client.ValidRedirect(q.Get("redirect_uri")):
		s.ssoError(w, r, http.StatusBadRequest)
		return
	case len(state) < 8 || len(state) > 256: // the subsite's own anti-forgery value; it must come back untouched
		s.ssoError(w, r, http.StatusBadRequest)
		return
	case challenge != "" && (q.Get("code_challenge_method") != "S256" || len(challenge) != 43):
		s.ssoError(w, r, http.StatusBadRequest)
		return
	}
	v := s.viewerOf(w, r)
	if v == nil {
		// Through the normal login, then back here with the same query.
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/account/sso/authorize?"+r.URL.RawQuery), http.StatusSeeOther)
		return
	}
	code := s.SSO.Issue(client.ID, v.AccountID, q.Get("redirect_uri"), challenge)
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", state)
	back.RawQuery = bq.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, back.String(), http.StatusSeeOther)
}

func ssoJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleSSOToken turns a code into who signed in. The client authenticates with its secret, in
// HTTP Basic or the client_id and client_secret form fields. The code is spent either way.
func (s *Server) handleSSOToken(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn() {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		ssoJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	client, ok := s.SSO.Client(id)
	if !ok || !client.SecretOK(secret) {
		// Same answer for an unknown client and a wrong secret.
		ssoJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if s.AuthLimit != nil && !s.AuthLimit.Allow("sso:"+client.ID) {
		ssoJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	g, ok := s.SSO.Redeem(client.ID, r.PostFormValue("code"), r.PostFormValue("redirect_uri"), r.PostFormValue("code_verifier"))
	if !ok {
		ssoJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	acct, err := s.Identity.GetAccount(r.Context(), g.AccountID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		ssoJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	case err != nil:
		s.log.Error("sso: account", "err", err)
		ssoJSON(w, http.StatusBadGateway, map[string]string{"error": "temporarily_unavailable"})
		return
	case acct.Status != "" && acct.Status != "active":
		ssoJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	out := map[string]any{"account_id": g.AccountID, "display_name": acct.DisplayName, "wiiu_pid": nil,
		"switch_pid": nil, "wfc_user_id": nil}
	if pid := s.wiiuPID(r, g.AccountID); pid != 0 {
		out["wiiu_pid"] = pid
	}
	if pid := s.switchNEXPID(r, g.AccountID); pid != "" {
		out["switch_pid"] = pid
	}
	if s.Links != nil {
		if link, err := s.Links.ActiveLinkByAccount(r.Context(), "wfc", g.AccountID); err == nil && link.SubjectID != "" {
			out["wfc_user_id"] = accounts.WFCUserID(link.SubjectID)
		}
	}
	ssoJSON(w, http.StatusOK, out)
}

// wiiuPID is the account's Wii U / 3DS PID from nn-account (the PNID, shadow or full), 0 when it
// is not configured or does not answer. Nothing else of the identity (it carries the NEX password)
// is read here.
func (s *Server) wiiuPID(r *http.Request, accountID string) int64 {
	if s.NNAdapter == nil {
		return 0
	}
	id, err := s.NNAdapter.Console(r.Context(), "wiiu", accountID)
	if err != nil {
		s.log.Warn("sso: wiiu pid", "err", err)
		return 0
	}
	if f, ok := id["pid"].(float64); ok && f > 0 {
		return int64(f)
	}
	return 0
}

// switchNEXPID is the NEX principal id Switch titles know the account by: the account's active
// "switch" link subject (the 16-hex baas_user_id) read as a number, as a decimal string. Empty
// when there is no link or the subject is not a nonzero 64-bit hex number.
func (s *Server) switchNEXPID(r *http.Request, accountID string) string {
	if s.Links == nil {
		return ""
	}
	link, err := s.Links.ActiveLinkByAccount(r.Context(), "switch", accountID)
	if err != nil {
		return ""
	}
	n, err := strconv.ParseUint(link.SubjectID, 16, 64)
	if err != nil || n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}

type ssoLogoutPage struct {
	baseData
	RedirectURI string
}

// handleSSOLogoutForm asks before signing out, because a link on another site must not be able to
// sign someone out by itself (the POST carries the CSRF token). Signed out already, it just goes back.
func (s *Server) handleSSOLogoutForm(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn() {
		http.NotFound(w, r)
		return
	}
	uri := r.URL.Query().Get("redirect_uri")
	if !s.ssoRedirectOK(uri) {
		s.ssoError(w, r, http.StatusBadRequest)
		return
	}
	v := s.viewerOf(w, r)
	if v == nil {
		http.Redirect(w, r, uri, http.StatusSeeOther)
		return
	}
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "sso.logout.title"))
	s.render(w, http.StatusOK, "sso_logout.html", ssoLogoutPage{d, uri})
}

func (s *Server) handleSSOLogout(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	uri := r.PostFormValue("redirect_uri")
	if !s.ssoRedirectOK(uri) {
		s.ssoError(w, r, http.StatusBadRequest)
		return
	}
	if v := s.viewerOf(w, r); v != nil {
		if !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		if err := s.Core.Logout(r.Context(), v.CoreSessionID); err != nil {
			s.log.Info("sso logout", "err", err)
		}
		_ = s.DB.DeleteWebSession(r.Context(), v.CoreSessionID)
	}
	s.Sessions.ClearCookie(w)
	http.Redirect(w, r, uri, http.StatusSeeOther)
}

// ssoRedirectOK reports whether uri is on a registered origin of any client.
func (s *Server) ssoRedirectOK(uri string) bool {
	if uri == "" || strings.ContainsAny(uri, "\r\n") {
		return false
	}
	return s.SSO.AnyClientAccepts(uri)
}
