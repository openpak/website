package pages

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

// Help tickets on the site: open one, read the thread, answer it. The same
// flows back /api/v1/me/tickets for the app (internal/support).

type helpView struct {
	baseData
	Tickets    []store.Ticket
	Categories []string
	Subject    string
	Category   string
	Message    string
	Error      string
	Files      bool
	Off        bool
}

type helpTicketView struct {
	baseData
	Ticket   store.Ticket
	Messages []store.TicketMessage
	Error    string
	Files    bool
}

func (s *Server) helpOn() bool { return s.Support != nil }

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := helpView{baseData: s.data(r, v, i18n.T(i18n.FromRequest(r), "help.title")), Categories: store.TicketCategories,
		Off: !s.helpOn()}
	if v != nil && s.helpOn() {
		d.Files = s.Support.Files != nil
		ts, err := s.DB.ListTickets(r.Context(), store.TicketFilter{AccountID: v.AccountID}, 100)
		if err != nil {
			s.log.Error("help: list", "err", err)
		}
		d.Tickets = ts
	}
	s.render(w, http.StatusOK, "help.html", d)
}

// helpUpload reads the optional file of a help form; nil when none was chosen.
func helpUpload(r *http.Request) (*support.Upload, func(), error) {
	f, h, err := r.FormFile("attachment")
	if errors.Is(err, http.ErrMissingFile) || (err == nil && h.Filename == "" && h.Size == 0) {
		if f != nil {
			f.Close()
		}
		return nil, func() {}, nil
	}
	if err != nil {
		return nil, func() {}, err
	}
	return &support.Upload{Name: h.Filename, Body: f}, func() { f.Close() }, nil
}

// helpError is what the form says when the service refused.
func helpError(lang string, err error) string {
	switch {
	case errors.Is(err, store.ErrInvalid):
		return i18n.T(lang, "help.invalid")
	case errors.Is(err, attachments.ErrTooLarge):
		return i18n.T(lang, "help.toolarge")
	case errors.Is(err, support.ErrNoAttachments):
		return i18n.T(lang, "help.nofiles")
	default:
		return i18n.T(lang, "msg.error")
	}
}

// limitHelpBody caps a help form: the file plus a generous margin for the
// text fields, so a huge body is refused before any of it is parsed.
func limitHelpBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, support.MaxAttachment+1<<20)
}

func (s *Server) handleHelpOpen(w http.ResponseWriter, r *http.Request) {
	limitHelpBody(w, r)
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !s.helpOn() {
		http.Error(w, "help tickets are not available", http.StatusServiceUnavailable)
		return
	}
	lang := i18n.FromRequest(r)
	up, done, err := helpUpload(r)
	defer done()
	d := helpView{Categories: store.TicketCategories, Subject: strings.TrimSpace(r.PostFormValue("subject")),
		Category: r.PostFormValue("category"), Message: strings.ReplaceAll(r.PostFormValue("message"), "\r\n", "\n"),
		Files: s.Support.Files != nil}
	var t store.Ticket
	if err == nil {
		t, err = s.Support.Open(r.Context(), store.Ticket{AccountID: v.AccountID, DisplayName: v.DisplayName, Email: v.Email,
			Subject: d.Subject, Category: d.Category}, d.Message, up)
	}
	if err != nil {
		if !errors.Is(err, store.ErrInvalid) {
			s.log.Error("help: open", "err", err)
		}
		d.baseData, d.Error = s.data(r, v, i18n.T(lang, "help.title")), helpError(lang, err)
		d.Tickets, _ = s.DB.ListTickets(r.Context(), store.TicketFilter{AccountID: v.AccountID}, 100)
		s.render(w, http.StatusBadRequest, "help.html", d)
		return
	}
	redirectMsg(w, r, "/help/"+strconv.FormatInt(t.ID, 10), "ticket-opened")
}

func (s *Server) helpTicket(w http.ResponseWriter, r *http.Request, v *viewer, status int, errMsg string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	t, msgs, err := s.Support.Own(r.Context(), id, v.AccountID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := helpTicketView{baseData: s.data(r, v, t.Subject), Ticket: t, Messages: msgs, Error: errMsg, Files: s.Support.Files != nil}
	d.Path = "/help"
	s.render(w, status, "help_ticket.html", d)
}

func (s *Server) handleHelpTicket(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	if !s.helpOn() {
		http.NotFound(w, r)
		return
	}
	s.helpTicket(w, r, v, http.StatusOK, "")
}

func (s *Server) handleHelpReply(w http.ResponseWriter, r *http.Request) {
	limitHelpBody(w, r)
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !s.helpOn() {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	up, done, err := helpUpload(r)
	defer done()
	if err == nil {
		_, err = s.Support.Reply(r.Context(), id, v.AccountID, strings.ReplaceAll(r.PostFormValue("message"), "\r\n", "\n"), up)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.helpTicket(w, r, v, http.StatusBadRequest, helpError(i18n.FromRequest(r), err))
	default:
		redirectMsg(w, r, "/help/"+strconv.FormatInt(id, 10), "ticket-replied")
	}
}

func (s *Server) handleHelpAttachment(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil || !s.helpOn() {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("msg"), 10, 64)
	att, err := s.Support.Attachment(r.Context(), id, v.AccountID, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Support.Files.Serve(w, r, att.Key, att.Name)
}
