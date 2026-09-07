// Package admin serves role-gated moderation and operator pages (WEB-06,
// WEB-07). Authorization is fail-closed: only account ids listed in
// WEBSITE_ADMIN_ACCOUNT_IDS may open any /admin route. Ban/unban and the
// reports queue require admin contracts in the account core (CR-5, CR-6)
// and render explicit pending notices until those land — the website never
// touches core storage and never forks the core.
package admin

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// Server hosts the admin surface.
type Server struct {
	Core     core.Events
	DB       store.Store
	Sessions sessionView
	AdminIDs []string
	tmpl     *template.Template
	log      *slog.Logger
}

// sessionView is the minimal session interface admin needs (avoids an
// import cycle with pages).
type sessionView interface {
	CoreSessionID(r *http.Request) string
}

// New builds the admin server.
func New(tpl *template.Template, coreEvents core.Events, db store.Store, sessions sessionView, adminIDs []string, log *slog.Logger) *Server {
	return &Server{Core: coreEvents, DB: db, Sessions: sessions, AdminIDs: adminIDs, tmpl: tpl, log: log}
}

type base struct {
	Lang   string
	Title  string
	User   adminUser
	Flash  string
	Banner string
	CSRF   string
	Path   string
}

// adminUser matches the field shape the shared templates expect.
type adminUser struct{ DisplayName string }

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
		}{base{Lang: "en", Title: "Admin", User: adminUser{ws.DisplayName}}, i18n.T("en", "admin.denied")})
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
	mux.HandleFunc("GET /admin/config", s.handleConfig)
	mux.HandleFunc("POST /admin/config", s.handleConfigSave)
	mux.HandleFunc("GET /admin/events", s.handleEvents)
	mux.HandleFunc("GET /admin/users", s.handleUsers)
	mux.HandleFunc("GET /admin/reports", s.handleReports)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "admin.html", base{Lang: "en", Title: i18n.T("en", "admin.title"), User: adminUser{ws.DisplayName}})
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
	s.render(w, http.StatusOK, "admin_config.html", cfgView{base{Lang: "en", Title: i18n.T("en", "admin.config"), User: adminUser{ws.DisplayName}}, cfg})
}

func (s *Server) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
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
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "site_config.update",
		"registrations_open="+strconv.FormatBool(cfg.RegistrationsOpen))
	http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	// Core invalidation events are the durable audit log (WEB-06). Poll
	// from the beginning each view; the stream is small and append-only.
	coreEvents, _, err := s.Core.Poll(r.Context(), 0, 200)
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
		base{Lang: "en", Title: i18n.T("en", "admin.events"), User: adminUser{ws.DisplayName}},
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
	}{base{Lang: "en", Title: i18n.T("en", "admin.users"), User: adminUser{ws.DisplayName}}, i18n.T("en", "admin.pending.cr5")})
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
	}{base{Lang: "en", Title: "Reports", User: adminUser{ws.DisplayName}}, i18n.T("en", "admin.pending.cr6")})
}
