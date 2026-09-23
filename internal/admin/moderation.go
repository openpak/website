package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
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
		Accounts []userRow
		Searched bool
		// Banned is every account banned through this site, newest first,
		// shown when nothing is searched: who did it and why at a glance.
		Banned []store.Ban
	}
	v := usersView{base: b, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	v.Flash = moderationFlash(r.URL.Query().Get("m"))
	if v.Query != "" {
		accts, err := s.Moderation.SearchAccounts(r.Context(), v.Query, 50)
		if err != nil {
			s.log.Error("admin: search accounts", "err", err)
			v.Flash = i18n.T("en", "admin.users.failed")
		}
		for _, a := range accts {
			row := userRow{ModeratedAccount: a}
			if a.Status == "banned" {
				if ban, err := s.DB.GetBan(r.Context(), a.ID); err == nil && ban.Banned {
					row.Ban = &ban
				}
			}
			v.Accounts = append(v.Accounts, row)
		}
		v.Searched = true
	} else {
		bans, err := s.DB.ListBans(r.Context(), 200)
		if err != nil {
			s.log.Error("admin: list bans", "err", err)
		}
		v.Banned = bans
	}
	s.render(w, http.StatusOK, "admin_users.html", v)
}

// userRow is a search hit plus the website's record of its ban, if any.
type userRow struct {
	core.ModeratedAccount
	Ban *store.Ban
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
	// The core holds the ban; this row is who did it and why, for the admin
	// and the internal ban lookup (docs/ban-lookup.md).
	if err := s.DB.RecordBan(r.Context(), store.Ban{AccountID: id, Banned: banned, Reason: reason, Actor: ws.AccountID}); err != nil {
		s.log.Error("admin: record ban", "account", id, "err", err)
	}
	if banned {
		// The core revoked the account's sessions; drop the website's own index
		// rows and bearers now too, so nothing here (admin pages included)
		// waits for the next core check to notice.
		_ = s.DB.DeleteWebSessionsForAccount(r.Context(), id)
		_ = s.DB.RevokeAPITokensForAccount(r.Context(), id)
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
