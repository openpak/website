package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
)

// Users and reports (WEB-06) over the core's Moderation service (CR-5, CR-6).
// The core audits every ban and resolution under the actor it is given, the
// admin's account id; the website's own audit log records the same act, so
// both trails agree on who did what.

// handleUsers searches accounts and offers ban/unban on each hit.
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	b := s.page(r, ws, i18n.T("en", "admin.users"))
	if s.Moderation == nil {
		s.renderPending(w, b, "admin.users.off")
		return
	}
	type usersView struct {
		base
		Query    string
		Accounts []core.ModeratedAccount
		Searched bool
	}
	v := usersView{base: b, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	v.Flash = moderationFlash(r.URL.Query().Get("m"))
	if v.Query != "" {
		accts, err := s.Moderation.SearchAccounts(r.Context(), v.Query, 50)
		if err != nil {
			s.log.Error("admin: search accounts", "err", err)
			v.Flash = i18n.T("en", "admin.users.failed")
		}
		v.Accounts, v.Searched = accts, true
	}
	s.render(w, http.StatusOK, "admin_users.html", v)
}

// handleBan bans or unbans one account. A reason is required either way: the
// core refuses without one, and so does the audit trail.
func (s *Server) handleBan(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Moderation == nil {
		http.Error(w, "moderation is not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	banned := r.PostFormValue("banned") == "1"
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	back := "/admin/users?" + url.Values{"q": {r.PostFormValue("q")}}.Encode()
	if reason == "" {
		http.Redirect(w, r, back+"&m=reason", http.StatusSeeOther)
		return
	}
	err := s.Moderation.SetBanned(r.Context(), id, banned, reason, ws.AccountID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		http.Redirect(w, r, back+"&m=gone", http.StatusSeeOther)
		return
	case err != nil:
		s.log.Error("admin: set banned", "account", id, "err", err)
		http.Error(w, "the account service refused the change", http.StatusBadGateway)
		return
	}
	action, flash := "account.ban", "banned"
	if !banned {
		action, flash = "account.unban", "unbanned"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("account=%s reason=%q", id, reason))
	http.Redirect(w, r, back+"&m="+flash, http.StatusSeeOther)
}

// reportFilters are the queue's views, in the order the page offers them.
var reportFilters = []string{"open", "resolved", "all"}

// handleReports is the queue: open reports oldest first, the rest newest first.
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	b := s.page(r, ws, i18n.T("en", "admin.reports"))
	if s.Moderation == nil {
		s.renderPending(w, b, "admin.reports.off")
		return
	}
	status := r.URL.Query().Get("status")
	if status != "resolved" && status != "all" {
		status = "open"
	}
	query := status
	if status == "all" {
		query = ""
	}
	type reportsView struct {
		base
		Status  string
		Filters []string
		Reports []core.Report
	}
	v := reportsView{base: b, Status: status, Filters: reportFilters}
	v.Flash = moderationFlash(r.URL.Query().Get("m"))
	reports, err := s.Moderation.ListReports(r.Context(), query, 200)
	if err != nil {
		s.log.Error("admin: list reports", "err", err)
		v.Flash = i18n.T("en", "admin.reports.failed")
	}
	v.Reports = reports
	s.render(w, http.StatusOK, "admin_reports.html", v)
}

// handleResolveReport closes one open report with what was done about it.
func (s *Server) handleResolveReport(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Moderation == nil {
		http.Error(w, "moderation is not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	resolution := strings.TrimSpace(r.PostFormValue("resolution"))
	if resolution == "" {
		http.Redirect(w, r, "/admin/reports?m=resolution", http.StatusSeeOther)
		return
	}
	err := s.Moderation.ResolveReport(r.Context(), id, resolution, ws.AccountID)
	switch {
	case errors.Is(err, core.ErrConflict), errors.Is(err, core.ErrNotFound):
		// Someone else got there first.
		http.Redirect(w, r, "/admin/reports?m=already", http.StatusSeeOther)
		return
	case err != nil:
		s.log.Error("admin: resolve report", "report", id, "err", err)
		http.Error(w, "the account service refused the change", http.StatusBadGateway)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "report.resolve", fmt.Sprintf("report=%s resolution=%q", id, resolution))
	http.Redirect(w, r, "/admin/reports?m=resolved", http.StatusSeeOther)
}

func (s *Server) renderPending(w http.ResponseWriter, b base, key string) {
	s.render(w, http.StatusOK, "admin_pending.html", struct {
		base
		Notice string
	}{b, i18n.T("en", key)})
}

func moderationFlash(code string) string {
	switch code {
	case "banned", "unbanned", "gone", "reason", "resolved", "resolution", "already":
		return i18n.T("en", "admin.flash."+code)
	default:
		return ""
	}
}
