// Package admin serves role-gated moderation and operator pages (WEB-06,
// WEB-07). Authorization is fail-closed: only account ids listed in
// WEBSITE_ADMIN_ACCOUNT_IDS may open any /admin route. Ban/unban and the
// reports queue require admin contracts in the account core (CR-5, CR-6)
// and render explicit pending notices until those land — the website never
// touches core storage and never forks the core.
package admin

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	newsv1 "github.com/openpak/website/internal/newspb"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/store"
)

// Server hosts the admin surface.
type Server struct {
	// Federation is set by main; nil hides the region page's actions.
	Federation core.Federation
	Core       core.Events
	DB         store.Store
	Sessions   sessionView
	AdminIDs   []string

	// What the monitor reads. Every one is optional: nil (or an empty URL)
	// renders as "not configured", never as an error page.
	CoreHTTP healthChecker
	Media    healthChecker
	Players  interface {
		PlayerCounts(ctx context.Context) ([]core.TitlePlayers, map[string]int, int, error)
	}
	Saves *saves.Client
	Chat  *chat.Client
	// PushOn says the notifier runs, so its cursor is expected to move.
	PushOn bool
	// StatusAPI is the availability page's JSON (STATUS_API_URL).
	StatusAPI string
	// News is the news service's operator gRPC; nil says "not configured".
	News newsv1.NewsClient
	// Mods is the mods service's curation API; nil says "not configured".
	Mods *ModsAPI

	tmpl       *template.Template
	log        *slog.Logger
}

// sessionView is the minimal session interface admin needs (avoids an
// import cycle with pages).
type sessionView interface {
	CoreSessionID(r *http.Request) string
	CSRFToken(coreSessionID string) string
	CheckCSRF(coreSessionID, token string) bool
}

// New builds the admin server.
func New(tpl *template.Template, coreEvents core.Events, db store.Store, sessions sessionView, adminIDs []string, log *slog.Logger) *Server {
	return &Server{Core: coreEvents, DB: db, Sessions: sessions, AdminIDs: adminIDs, tmpl: tpl, log: log}
}

type base struct {
	// Same fingerprint the public pages use: _layout.html is shared, and without this field
	// every admin page fails to render.
	Asset  string
	Lang   string
	Title  string
	User   adminUser
	Flash  string
	Banner string
	CSRF   string
	Path   string
	Admin  bool
	Nav    []navLink
}

// navLink is one entry of the admin sub-nav every admin page carries.
type navLink struct {
	Href, Label string
	Current     bool
}

// adminNav is the sub-nav, in reading order. One list, so a new page is one
// line here rather than an edit to every template.
var adminNav = []struct{ href, key string }{
	{"/admin", "admin.nav.overview"},
	{"/admin/monitor", "admin.monitor"},
	{"/admin/news", "admin.news"},
	{"/admin/titles", "admin.titles"},
	{"/admin/mods", "admin.mods"},
	{"/admin/nodes", "admin.nodes"},
	{"/admin/config", "admin.nav.config"},
	{"/admin/events", "admin.nav.events"},
	{"/admin/users", "admin.nav.users"},
	{"/admin/reports", "admin.reports"},
}

func navFor(path string) []navLink {
	out := make([]navLink, len(adminNav))
	for i, n := range adminNav {
		// A section owns its sub-pages (/admin/news/new is News), except the
		// overview, which would otherwise own everything.
		cur := path == n.href || (n.href != "/admin" && strings.HasPrefix(path, n.href+"/"))
		out[i] = navLink{n.href, i18n.T("en", n.key), cur}
	}
	return out
}

// adminUser matches the field shape the shared templates expect.
type adminUser struct{ DisplayName, Email string }

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) (store.WebSession, bool) {
	id := s.Sessions.CoreSessionID(r)
	if id == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return store.WebSession{}, false
	}
	ws, err := s.DB.GetWebSession(r.Context(), id)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return store.WebSession{}, false
	}
	if !slices.Contains(s.AdminIDs, ws.AccountID) {
		s.render(w, http.StatusForbidden, "message.html", struct {
			base
			Message string
		}{s.page(r, ws, "Admin"), i18n.T("en", "admin.denied")})
		return ws, false
	}
	return ws, true
}

// page is the shared view base. CSRF is filled for every page so the admin
// forms and the layout's sign-out form both carry a valid token.
func (s *Server) page(r *http.Request, ws store.WebSession, title string) base {
	return base{Asset: pages.AssetVersion(), Admin: true, Path: r.URL.Path, Lang: "en", Title: title,
		User: adminUser{ws.DisplayName, ws.Email}, CSRF: s.Sessions.CSRFToken(ws.CoreSessionID),
		Nav: navFor(r.URL.Path)}
}

// authorizePost is authorize plus the CSRF check every state-changing admin
// form needs; the SameSite cookie alone is not the defence.
func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) (store.WebSession, bool) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return ws, false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return ws, false
	}
	if !s.Sessions.CheckCSRF(ws.CoreSessionID, r.PostFormValue("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return ws, false
	}
	return ws, true
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("admin template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// RegisterRoutes mounts /admin routes. Authorization runs per request.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.handleDashboard)
	mux.HandleFunc("GET /admin/monitor", s.handleMonitor)
	mux.HandleFunc("GET /admin/news", s.handleNews)
	mux.HandleFunc("GET /admin/news/new", s.handleNewsNew)
	mux.HandleFunc("POST /admin/news", s.handleNewsSave)
	mux.HandleFunc("GET /admin/news/{id}", s.handleNewsEdit)
	mux.HandleFunc("POST /admin/news/{id}", s.handleNewsSave)
	mux.HandleFunc("POST /admin/news/{id}/publish", s.handleNewsPublish)
	mux.HandleFunc("POST /admin/news/{id}/unpublish", s.handleNewsPublish)
	mux.HandleFunc("GET /admin/titles", s.handleTitles)
	mux.HandleFunc("POST /admin/titles", s.handleTitleSave)
	mux.HandleFunc("GET /admin/mods", s.handleMods)
	mux.HandleFunc("POST /admin/mods/{action}", s.handleModDecision)
	mux.HandleFunc("POST /admin/mods/{id}/{action}", s.handleModDecision)
	mux.HandleFunc("GET /admin/config", s.handleConfig)
	mux.HandleFunc("POST /admin/config", s.handleConfigSave)
	mux.HandleFunc("GET /admin/events", s.handleEvents)
	mux.HandleFunc("GET /admin/users", s.handleUsers)
	mux.HandleFunc("GET /admin/reports", s.handleReports)
	mux.HandleFunc("GET /admin/nodes", s.handleNodes)
	mux.HandleFunc("POST /admin/nodes", s.handleNodeCreate)
	mux.HandleFunc("POST /admin/nodes/{id}/approve", s.handleNodeApprove)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "admin.html", s.page(r, ws, i18n.T("en", "admin.title")))
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	cfg, err := s.DB.GetSiteConfig(r.Context())
	if err != nil {
		s.log.Error("get config", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	type cfgView struct {
		base
		store.SiteConfig
	}
	s.render(w, http.StatusOK, "admin_config.html", cfgView{s.page(r, ws, i18n.T("en", "admin.config")), cfg})
}

func (s *Server) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	cfg := store.SiteConfig{
		RegistrationsOpen: r.PostFormValue("registrations_open") == "on",
		BannerMessage:     r.PostFormValue("banner"),
	}
	if err := s.DB.SetSiteConfig(r.Context(), cfg); err != nil {
		s.log.Error("set config", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The banner is shown to every visitor, so what it said is part of the record.
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "site_config.update",
		fmt.Sprintf("registrations_open=%t banner=%q", cfg.RegistrationsOpen, cfg.BannerMessage))
	http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	// Core invalidation events are the durable audit log (WEB-06). The push
	// cursor trails the head by seconds when the notifier runs, so it is where
	// the walk starts; without it the walk starts at the beginning.
	cursor, _ := s.DB.PushCursor(r.Context())
	coreEvents, _, err := newestEvents(r.Context(), s.Core, cursor, 200)
	if err != nil {
		s.log.Error("poll events", "err", err)
	}
	siteAudit, err := s.DB.ListAudit(r.Context(), 100)
	if err != nil {
		s.log.Error("list audit", "err", err)
	}
	type eventsView struct {
		base
		CoreEvents []core.Event
		SiteAudit  []store.AuditEntry
	}
	s.render(w, http.StatusOK, "admin_events.html", eventsView{
		s.page(r, ws, i18n.T("en", "admin.events")),
		coreEvents, siteAudit,
	})
}

// handleUsers: user search and ban/unban are fail-closed pending CR-5.
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "admin_pending.html", struct {
		base
		Notice string
	}{s.page(r, ws, i18n.T("en", "admin.users")), i18n.T("en", "admin.pending.cr5")})
}

// handleReports: the reports queue is fail-closed pending CR-6.
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "admin_pending.html", struct {
		base
		Notice string
	}{s.page(r, ws, i18n.T("en", "admin.reports")), i18n.T("en", "admin.pending.cr6")})
}
