package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

// Help tickets for the app: the same threads the website's /help shows
// (internal/support). JSON bodies, or multipart when a file comes along.
func (s *Server) registerTickets(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	g("/me/tickets", s.auth(s.handleTickets))
	h("/me/tickets", s.auth(s.handleOpenTicket))
	g("/me/tickets/{id}", s.auth(s.handleTicket))
	h("/me/tickets/{id}/messages", s.auth(s.handleTicketReply))
	g("/me/tickets/{id}/attachments/{message}", s.auth(s.handleTicketAttachment))
}

func ticketJSON(t store.Ticket) map[string]any {
	return map[string]any{"id": t.ID, "subject": t.Subject, "category": t.Category, "status": t.Status,
		"messages": t.Messages, "created_at": t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": t.UpdatedAt.UTC().Format(time.RFC3339)}
}

func ticketMessageJSON(m store.TicketMessage) map[string]any {
	from := "you"
	if m.Staff {
		from = "staff"
	}
	out := map[string]any{"id": m.ID, "from": from, "body": m.Body,
		"created_at": m.CreatedAt.UTC().Format(time.RFC3339), "attachment": nil}
	if m.Attachment.Key != "" {
		out["attachment"] = map[string]any{"name": m.Attachment.Name, "size": m.Attachment.Size,
			"url": "/api/v1/me/tickets/" + strconv.FormatInt(m.TicketID, 10) + "/attachments/" + strconv.FormatInt(m.ID, 10)}
	}
	return out
}

func (s *Server) ticketsOff(w http.ResponseWriter) bool {
	if s.Support == nil {
		writeError(w, http.StatusServiceUnavailable, "help tickets are not available on this deployment")
		return true
	}
	return false
}

// ticketInput reads subject, category, message and an optional file from a
// JSON or multipart body. The body is capped at the attachment limit plus a
// margin for the text.
func ticketInput(w http.ResponseWriter, r *http.Request) (subject, category, message string, up *support.Upload, done func(), ok bool) {
	done = func() {}
	r.Body = http.MaxBytesReader(w, r.Body, support.MaxAttachment+1<<20)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than 10 MB")
			return
		}
		if f, h, err := r.FormFile("attachment"); err == nil {
			up, done = &support.Upload{Name: h.Filename, Body: f}, func() { f.Close() }
		}
		return r.FormValue("subject"), r.FormValue("category"), r.FormValue("message"), up, done, true
	}
	var req struct{ Subject, Category, Message string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	return req.Subject, req.Category, req.Message, nil, done, true
}

// ticketFailed answers a refused open or reply.
func (s *Server) ticketFailed(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such ticket")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid request: "))
	case errors.Is(err, attachments.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than 10 MB")
	case errors.Is(err, support.ErrNoAttachments):
		writeError(w, http.StatusBadRequest, "attachments are not available on this deployment")
	default:
		s.log.Error("tickets", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.ticketsOff(w) {
		return
	}
	ts, err := s.DB.ListTickets(r.Context(), store.TicketFilter{AccountID: tok.AccountID}, 100)
	if err != nil {
		s.ticketFailed(w, err)
		return
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, ticketJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": out, "categories": store.TicketCategories,
		"attachments": s.Support.Files != nil})
}

func (s *Server) handleOpenTicket(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.ticketsOff(w) {
		return
	}
	subject, category, message, up, done, ok := ticketInput(w, r)
	defer done()
	if !ok {
		return
	}
	t := store.Ticket{AccountID: tok.AccountID, Subject: subject, Category: category}
	if acct, err := s.Identity.GetAccount(r.Context(), tok.AccountID); err == nil {
		t.DisplayName = acct.DisplayName
	}
	if me, err := s.Core.Whoami(r.Context(), tok.CoreSessionID); err == nil {
		t.Email = me.Email
	}
	t, err := s.Support.Open(r.Context(), t, message, up)
	if err != nil {
		s.ticketFailed(w, err)
		return
	}
	s.writeThread(w, r, http.StatusCreated, t.ID, tok.AccountID)
}

func (s *Server) writeThread(w http.ResponseWriter, r *http.Request, code int, id int64, accountID string) {
	t, msgs, err := s.Support.Own(r.Context(), id, accountID)
	if err != nil {
		s.ticketFailed(w, err)
		return
	}
	out := ticketJSON(t)
	thread := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		thread = append(thread, ticketMessageJSON(m))
	}
	out["thread"] = thread
	writeJSON(w, code, out)
}

func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.ticketsOff(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.writeThread(w, r, http.StatusOK, id, tok.AccountID)
}

func (s *Server) handleTicketReply(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.ticketsOff(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_, _, message, up, done, ok := ticketInput(w, r)
	defer done()
	if !ok {
		return
	}
	m, err := s.Support.Reply(r.Context(), id, tok.AccountID, message, up)
	if err != nil {
		s.ticketFailed(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ticketMessageJSON(m))
}

func (s *Server) handleTicketAttachment(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.ticketsOff(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("message"), 10, 64)
	att, err := s.Support.Attachment(r.Context(), id, tok.AccountID, false)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such attachment")
		return
	}
	s.Support.Files.Serve(w, r, att.Key, att.Name)
}
