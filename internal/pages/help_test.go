package pages

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

// multipartAuthed posts a multipart form with the session cookie and CSRF
// token of a login; file is sent as "attachment" when not empty.
func multipartAuthed(t *testing.T, s *Server, loginRec *httptest.ResponseRecorder, target string, fields map[string]string, file string) *httptest.ResponseRecorder {
	t.Helper()
	var cookie string
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "opak_session" {
			cookie = c.Value
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", s.Sessions.CSRFToken(cookie))
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != "" {
		fw, _ := mw.CreateFormFile("attachment", "log.txt")
		_, _ = fw.Write([]byte(file))
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "opak_session", Value: cookie})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// Open a request with a file, read the thread, answer it; nobody else can see
// it or its file, and staff notes never show.
func TestHelpTickets(t *testing.T) {
	s, fc, db := newTestServer(t)
	a, b := seedTwoAccounts(fc)

	// Without the service the page says so; signed out it asks to sign in.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/help", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("help off: %d", rec.Code)
	}
	files, _ := attachments.New(t.TempDir())
	s.Support = &support.Service{DB: db, Files: files}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/support", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/help" {
		t.Fatalf("/support: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/help", nil))
	if !strings.Contains(rec.Body.String(), "Sign in to open") {
		t.Fatalf("signed out: %s", rec.Body.String())
	}

	la := login(t, s, a, "password-A-1")
	rec = multipartAuthed(t, s, la, "/help", map[string]string{"subject": "", "category": "account", "message": "x"}, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Fill in a subject") {
		t.Fatalf("invalid: %d", rec.Code)
	}
	rec = multipartAuthed(t, s, la, "/help", map[string]string{"subject": "Cannot sign in on Switch", "category": "switch",
		"message": "Error 2124-4007"}, "the log")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/help/1?m=ticket-opened") {
		t.Fatalf("open: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	msgs, _ := db.TicketMessages(context.Background(), 1, true)
	if len(msgs) != 1 || msgs[0].Attachment.Name != "log.txt" || msgs[0].Attachment.Size != 7 {
		t.Fatalf("stored: %+v", msgs)
	}
	_, _ = db.AddTicketMessage(context.Background(), store.TicketMessage{TicketID: 1, AuthorID: "adm", Staff: true, Internal: true, Body: "secret staff note"}, "")
	_, _ = db.AddTicketMessage(context.Background(), store.TicketMessage{TicketID: 1, AuthorID: "adm", Staff: true, Body: "Try the new build"}, store.TicketWaiting)

	rec = authedRequest(s, la, http.MethodGet, "/help", nil)
	if !strings.Contains(rec.Body.String(), "Cannot sign in on Switch") || !strings.Contains(rec.Body.String(), "Answered") {
		t.Fatalf("list: %s", rec.Body.String())
	}
	rec = authedRequest(s, la, http.MethodGet, "/help/1", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Try the new build") || strings.Contains(body, "secret staff note") ||
		!strings.Contains(body, "/help/attachments/1") {
		t.Fatalf("thread: %d %s", rec.Code, body)
	}
	rec = authedRequest(s, la, http.MethodGet, "/help/attachments/1", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "the log" || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body.String())
	}
	// The internal note (message 2) has no file, and would be hidden anyway.
	if rec := authedRequest(s, la, http.MethodGet, "/help/attachments/2", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("note download: %d", rec.Code)
	}

	// Someone else: no thread, no file, no reply.
	lb := login(t, s, b, "password-B-1")
	if rec := authedRequest(s, lb, http.MethodGet, "/help/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other's thread: %d", rec.Code)
	}
	if rec := authedRequest(s, lb, http.MethodGet, "/help/attachments/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other's file: %d", rec.Code)
	}
	if rec := multipartAuthed(t, s, lb, "/help/1", map[string]string{"message": "me too"}, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("other's reply: %d", rec.Code)
	}

	// Replying puts it back in the queue.
	rec = multipartAuthed(t, s, la, "/help/1", map[string]string{"message": "Still broken"}, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
	}
	if tk, _ := db.GetTicket(context.Background(), 1); tk.Status != store.TicketOpen || tk.Messages != 3 {
		t.Fatalf("after reply: %+v", tk)
	}
	// Without the CSRF token nothing happens.
	rec = authedRequest(s, la, http.MethodGet, "/help/1", nil)
	req := httptest.NewRequest(http.MethodPost, "/help/1", strings.NewReader("message=forged"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(la.Result().Cookies()[0])
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged reply: %d", rec.Code)
	}
}
