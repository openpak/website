package admin

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

// The help-ticket queue (/admin/tickets): what people asked on /help and in
// the app. Replies reach the person on the site and in the app (and as a push
// when FCM is configured); the website has no mail path, so nothing is
// emailed. Internal notes are for staff only.

var ticketFilters = []string{"open", "waiting", "closed", "all"}

func (s *Server) ticketsOff(w http.ResponseWriter, r *http.Request, ws store.WebSession) bool {
	if s.Support != nil {
		return false
	}
	s.renderPending(w, s.page(r, ws, i18n.T("en", "admin.tickets")), "admin.tickets.off")
	return true
}

func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.ticketsOff(w, r, ws) {
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if !slices.Contains(ticketFilters, status) {
		status = "open"
	}
	f := store.TicketFilter{Status: status}
	if status == "all" {
		f.Status = ""
	}
	mine := q.Get("mine") == "1"
	if mine {
		f.Assignee = ws.AccountID
	}
	ts, err := s.DB.ListTickets(r.Context(), f, 200)
	v := struct {
		base
		Status  string
		Filters []string
		Mine    bool
		Tickets []store.Ticket
		Error   string
	}{base: s.page(r, ws, i18n.T("en", "admin.tickets")), Status: status, Filters: ticketFilters, Mine: mine, Tickets: ts}
	if err != nil {
		s.log.Error("admin: list tickets", "err", err)
		v.Error = err.Error()
	}
	s.render(w, http.StatusOK, "admin_tickets.html", v)
}

func (s *Server) renderTicket(w http.ResponseWriter, r *http.Request, ws store.WebSession, id int64, status int, errMsg string) {
	t, err := s.DB.GetTicket(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msgs, err := s.DB.TicketMessages(r.Context(), id, true)
	if err != nil {
		s.log.Error("admin: ticket thread", "err", err)
	}
	b := s.page(r, ws, fmt.Sprintf("#%d %s", t.ID, t.Subject))
	b.Path = "/admin/tickets/" + strconv.FormatInt(id, 10)
	b.Flash = ticketFlash(r.URL.Query().Get("m"))
	s.render(w, status, "admin_ticket.html", struct {
		base
		Ticket   store.Ticket
		Messages []store.TicketMessage
		Admins   []string
		Statuses []string
		Files    bool
		Error    string
	}{b, t, msgs, s.AdminIDs, store.TicketStatuses, s.Support.Files != nil, errMsg})
}

func ticketFlash(code string) string {
	switch code {
	case "replied", "noted", "assigned", "closed", "reopened":
		return i18n.T("en", "admin.tickets.flash."+code)
	}
	return ""
}

func (s *Server) handleTicketView(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.ticketsOff(w, r, ws) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.renderTicket(w, r, ws, id, http.StatusOK, "")
}

// handleTicketReply is a reply to the person, or an internal note; a reply
// can close the ticket in the same step.
func (s *Server) handleTicketReply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, support.MaxAttachment+1<<20)
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Support == nil {
		http.Error(w, i18n.T("en", "admin.tickets.off"), http.StatusServiceUnavailable)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	internal := r.PostFormValue("internal") == "1"
	status := ""
	if r.PostFormValue("close") == "1" && !internal {
		status = store.TicketClosed
	}
	var up *support.Upload
	if f, h, err := r.FormFile("attachment"); err == nil {
		defer f.Close()
		if h.Filename != "" || h.Size > 0 {
			up = &support.Upload{Name: h.Filename, Body: f}
		}
	}
	body := strings.ReplaceAll(r.PostFormValue("message"), "\r\n", "\n")
	_, err := s.Support.StaffReply(r.Context(), id, ws.AccountID, body, internal, up, status)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.renderTicket(w, r, ws, id, http.StatusBadRequest, err.Error())
		return
	}
	action, flash := "ticket.reply", "replied"
	if internal {
		action, flash = "ticket.note", "noted"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("ticket=%d", id))
	if status == store.TicketClosed {
		_ = s.DB.AddAudit(r.Context(), ws.AccountID, "ticket.close", fmt.Sprintf("ticket=%d", id))
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/tickets/%d?m=%s", id, flash), http.StatusSeeOther)
}

// handleTicketUpdate assigns, closes or reopens.
func (s *Server) handleTicketUpdate(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Support == nil {
		http.Error(w, i18n.T("en", "admin.tickets.off"), http.StatusServiceUnavailable)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var status, assignee *string
	var action, detail, flash string
	switch r.PathValue("action") {
	case "assign":
		a := strings.TrimSpace(r.PostFormValue("assignee"))
		if a != "" && !slices.Contains(s.AdminIDs, a) {
			s.renderTicket(w, r, ws, id, http.StatusBadRequest, "assign to an admin account id, or nobody")
			return
		}
		assignee, action, detail, flash = &a, "ticket.assign", fmt.Sprintf("ticket=%d assignee=%q", id, a), "assigned"
	case "close":
		c := store.TicketClosed
		status, action, detail, flash = &c, "ticket.close", fmt.Sprintf("ticket=%d", id), "closed"
	case "reopen":
		o := store.TicketOpen
		status, action, detail, flash = &o, "ticket.reopen", fmt.Sprintf("ticket=%d", id), "reopened"
	default:
		http.NotFound(w, r)
		return
	}
	if _, err := s.DB.UpdateTicket(r.Context(), id, status, assignee); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("admin: update ticket", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, detail)
	http.Redirect(w, r, fmt.Sprintf("/admin/tickets/%d?m=%s", id, flash), http.StatusSeeOther)
}

func (s *Server) handleTicketAttachment(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.ticketsOff(w, r, ws) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("msg"), 10, 64)
	att, err := s.Support.Attachment(r.Context(), id, ws.AccountID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Support.Files.Serve(w, r, att.Key, att.Name)
}
