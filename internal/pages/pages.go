// Package pages serves the server-rendered website (landing, account
// portal, friends, status). No JavaScript is required for any journey
// (WEB-10): every action is an HTML form.
package pages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// Server wires the page handlers.
type Server struct {
	Core       core.HTTP
	Identity   core.Identity
	Social     core.Social
	Links      core.Links
	Events     core.Events
	Invites    core.InvitationsView
	DB         store.Store
	Media      *media.Service
	Sessions   *session.Manager
	Production bool
	// AdminAccountIDs mirrors the admin allowlist so the portal can show
	// the moderation section to the accounts that can actually open it.
	AdminAccountIDs []string
	AuthLimit       *ratelimit.Limiter
	MediaLimit      *ratelimit.Limiter
	tmpl            *template.Template
	log             *slog.Logger
}

// New compiles templates and returns the server.
func New(tpl *template.Template, log *slog.Logger) *Server {
	return &Server{tmpl: tpl, log: log}
}

// viewer is the signed-in identity for one request.
type viewer struct {
	CoreSessionID string
	store.WebSession
}

// baseData feeds the layout template.
type baseData struct {
	// Asset is a fingerprint of the static files, appended to their URLs. Without it a CDN
	// keeps serving the previous deploy's stylesheet for as long as its TTL says it may —
	// which is exactly what happened: a cached 2.6 KB site.css against a 16 KB one on disk.
	Asset   string
	Lang    string
	Title   string
	Banner  string
	RegOpen bool
	User    *viewUser
	Flash   string
	CSRF    string
	Path    string
	Admin   bool
}

type viewUser struct {
	ID          string
	Email       string
	DisplayName string
}

// data builds baseData for a request.
func (s *Server) data(r *http.Request, v *viewer, title string) baseData {
	d := baseData{Asset: assetVersion(), Lang: i18n.Lang(r.Header.Get("Accept-Language")), Title: title, Path: r.URL.Path}
	if cfg, err := s.DB.GetSiteConfig(r.Context()); err == nil {
		d.Banner = cfg.BannerMessage
		d.RegOpen = cfg.RegistrationsOpen
	}
	if v != nil {
		d.User = &viewUser{ID: v.AccountID, Email: v.Email, DisplayName: v.DisplayName}
		d.Admin = slices.Contains(s.AdminAccountIDs, v.AccountID)
		d.CSRF = s.Sessions.CSRFToken(v.CoreSessionID)
	}
	d.Flash = flashText(r.URL.Query().Get("m"))
	return d
}

// flashText maps short redirect codes to catalogue strings.
func flashText(code string) string {
	switch code {
	case "saved":
		return i18n.T("en", "msg.saved")
	case "pwreset":
		return i18n.T("en", "auth.reset.ok")
	case "frequest":
		return i18n.T("en", "friends.request.ok")
	case "funknown":
		return i18n.T("en", "friends.request.unknown")
	case "fself":
		return i18n.T("en", "friends.request.self")
	case "password-changed":
		return i18n.T("en", "account.password.ok")
	case "password-mismatch":
		return i18n.T("en", "account.password.mismatch")
	case "password-failed":
		return i18n.T("en", "account.password.failed")
	default:
		return ""
	}
}

// render executes a template with the layout.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// viewerOf resolves the signed-in user or nil. A dead core session clears
// the cookie and the index row so state never lies.
func (s *Server) viewerOf(w http.ResponseWriter, r *http.Request) *viewer {
	id := s.Sessions.CoreSessionID(r)
	if id == "" {
		return nil
	}
	ws, err := s.DB.GetWebSession(r.Context(), id)
	if err != nil {
		s.Sessions.ClearCookie(w)
		return nil
	}
	// Validate against the core: a revoked session (logout elsewhere, ban,
	// password reset) ends the website session too.
	if _, err := s.Core.ListSessions(r.Context(), id); err != nil {
		_ = s.DB.DeleteWebSession(r.Context(), id)
		s.Sessions.ClearCookie(w)
		return nil
	}
	return &viewer{CoreSessionID: id, WebSession: ws}
}

// requireViewer redirects signed-out users to the login page.
func (s *Server) requireViewer(w http.ResponseWriter, r *http.Request) *viewer {
	v := s.viewerOf(w, r)
	if v == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	return v
}

// postGuard enforces method + CSRF for state-changing forms. Multipart
// bodies (avatar upload) are parsed too so the csrf field is visible.
func (s *Server) postGuard(w http.ResponseWriter, r *http.Request) (*viewer, bool) {
	v := s.requireViewer(w, r)
	if v == nil {
		return nil, false
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return nil, false
		}
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return nil, false
	}
	if !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return nil, false
	}
	return v, true
}

func redirectMsg(w http.ResponseWriter, r *http.Request, to, msg string) {
	http.Redirect(w, r, to+"?m="+url.QueryEscape(msg), http.StatusSeeOther)
}

// Handler returns a standalone mux with all page routes; used by tests and
// usable for embedding the page surface in another server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return mux
}

// RegisterRoutes wires the public page routes.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.handleLanding)
	mux.HandleFunc("GET /register", s.handleRegisterForm)
	mux.HandleFunc("POST /register", s.handleRegisterSubmit)
	mux.HandleFunc("GET /verify", s.handleVerify)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /forgot-password", s.handleForgotForm)
	mux.HandleFunc("POST /forgot-password", s.handleForgotSubmit)
	mux.HandleFunc("GET /reset-password", s.handleResetForm)
	mux.HandleFunc("POST /reset-password", s.handleResetSubmit)
	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("POST /account/password", s.handleChangePassword)
	mux.HandleFunc("POST /account/sessions/revoke", s.handleRevokeSession)
	mux.HandleFunc("POST /account/link/revoke", s.handleUnlink)
	mux.HandleFunc("POST /account/avatar", s.handleAvatarUpload)
	mux.HandleFunc("GET /media/avatars/{accountID}/{size}", s.handleAvatarServe)
	mux.HandleFunc("GET /account/friends", s.handleFriends)
	mux.HandleFunc("POST /account/friends/request", s.handleFriendRequest)
	mux.HandleFunc("POST /account/friends/accept", s.handleFriendAccept)
	mux.HandleFunc("POST /account/friends/decline", s.handleFriendDecline)
	mux.HandleFunc("POST /account/friends/remove", s.handleFriendRemove)
	mux.HandleFunc("POST /account/friends/block", s.handleFriendBlock)
	mux.HandleFunc("POST /account/friends/unblock", s.handleFriendUnblock)
	mux.HandleFunc("GET /account/invitations", s.handleInvitations)
	mux.HandleFunc("GET /features", s.handleFeatures)
	mux.HandleFunc("GET /downloads", s.handleDownloads)
	mux.HandleFunc("GET /status", s.handleStatus)
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, "Home")
	titles, err := s.DB.ListTitles(r.Context())
	if err != nil {
		s.log.Error("list titles", "err", err)
	}
	type landing struct {
		baseData
		Titles []store.Title
	}
	s.render(w, http.StatusOK, "landing.html", landing{d, titles})
}

// handleFeatures and handleDownloads are static content pages: what the
// network does today, and how to get onto it. Both state availability
// honestly rather than advertising what has not shipped.
func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	s.render(w, http.StatusOK, "features.html", s.data(r, v, i18n.T("en", "features.title")))
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	s.render(w, http.StatusOK, "downloads.html", s.data(r, v, i18n.T("en", "downloads.title")))
}

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	if v != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	d := s.data(r, nil, "Create account")
	type reg struct {
		baseData
		Closed bool
	}
	s.render(w, http.StatusOK, "register.html", reg{d, !d.RegOpen})
}

func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.DB.GetSiteConfig(r.Context())
	if err == nil && !cfg.RegistrationsOpen {
		s.render(w, http.StatusForbidden, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, "Register"), i18n.T("en", "auth.register.closed")})
		return
	}
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	display := strings.TrimSpace(r.PostFormValue("display_name"))
	// Enumeration resistance is enforced by the core: taken-but-unverified
	// emails resend, and the response shape is identical.
	if err := s.Core.Register(r.Context(), email, password, display); err != nil {
		s.log.Info("register rejected", "err", err)
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, "Register"), i18n.T("en", "auth.register.sent")})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	err := s.Core.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	msg := i18n.T("en", "auth.verify.ok")
	if err != nil {
		msg = i18n.T("en", "auth.verify.bad")
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, "Verify email"), msg})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if v := s.viewerOf(w, r); v != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login.html", s.data(r, nil, "Sign in"))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	// WEB-01/WEB-02: log in through the core's HTTP route, bind the website
	// cookie to the core session id, and resolve the account id via the
	// Identity contract (interim whoami pending CR-4).
	coreSession, err := s.Core.Login(r.Context(), email, password)
	if err != nil {
		msg := i18n.T("en", "auth.login.failed")
		if errors.Is(err, core.ErrBanned) {
			msg = i18n.T("en", "auth.login.banned")
		}
		s.render(w, http.StatusUnauthorized, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, "Sign in"), msg})
		return
	}
	acct, err := s.Identity.ResolveAccount(r.Context(), email, password)
	if err != nil {
		// Login worked core-side but identity resolution failed; keep the
		// user signed out rather than serving a half session.
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("resolve account", "err", err)
		s.render(w, http.StatusInternalServerError, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, "Sign in"), i18n.T("en", "msg.error")})
		return
	}
	if err := s.DB.PutWebSession(r.Context(), store.WebSession{
		CoreSessionID: coreSession, AccountID: acct.ID, Email: email,
	}); err != nil {
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("put web session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Sessions.SetCookie(w, coreSession)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	if v != nil {
		if err := r.ParseForm(); err != nil || !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		if err := s.Core.Logout(r.Context(), v.CoreSessionID); err != nil {
			s.log.Info("logout", "err", err)
		}
		_ = s.DB.DeleteWebSession(r.Context(), v.CoreSessionID)
	}
	s.Sessions.ClearCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleForgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "forgot.html", s.data(r, nil, "Reset password"))
}

func (s *Server) handleForgotSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err == nil {
		// Core-side enumeration resistance: identical response either way.
		_ = s.Core.ForgotPassword(r.Context(), normalizeEmail(r.PostFormValue("email")))
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, "Reset password"), i18n.T("en", "auth.forgot.sent")})
}

func (s *Server) handleResetForm(w http.ResponseWriter, r *http.Request) {
	type reset struct {
		baseData
		Token string
	}
	s.render(w, http.StatusOK, "reset.html", reset{s.data(r, nil, "Choose a new password"), r.URL.Query().Get("token")})
}

func (s *Server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	err := s.Core.ResetPassword(r.Context(), r.PostFormValue("token"), r.PostFormValue("password"))
	if err != nil {
		s.render(w, http.StatusOK, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, "Reset password"), i18n.T("en", "auth.reset.bad")})
		return
	}
	// The core revoked every web session on reset; the website must forget
	// its index rows and bearer tokens for this account too. We do not know
	// the account id without the password, so expiry + core-side revocation
	// do the enforcement; local rows die on next use.
	redirectMsg(w, r, "/login", "pwreset")
}

func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// fmtFriendCode renders a link subject as SW- plus dash-separated groups of
// four when the adapter stored 12 or 16 hex digits (both are in the wild;
// the subject is adapter-owned, CR-3). Anything else is shown verbatim.
func fmtFriendCode(subject string) string {
	hex := make([]rune, 0, 16)
	for _, c := range subject {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			hex = append(hex, c)
		case c == '-':
		default:
			return subject
		}
	}
	if len(hex) != 12 && len(hex) != 16 {
		return subject
	}
	groups := make([]string, 0, 4)
	for i := 0; i < len(hex); i += 4 {
		groups = append(groups, string(hex[i:i+4]))
	}
	return "SW-" + strings.Join(groups, "-")
}

// AssetVersion is the fingerprint appended to static URLs; admin templates share the layout,
// so they need it too.
func AssetVersion() string { return assetVersion() }

// assetVersion hashes the files served from web/static, once per process. A new deploy has a
// new binary, so a new fingerprint, so browsers and caches fetch the new files.
var assetVersion = sync.OnceValue(func() string {
	h := sha256.New()
	filepath.WalkDir("web/static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil {
			h.Write([]byte(path))
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
})
