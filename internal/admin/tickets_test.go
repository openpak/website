package admin

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

func TestTicketsAdmin(t *testing.T) {
	s, db, mux := adminMux(t)
	ctx := context.Background()
	if rec := get(mux, "/admin/tickets"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("off: %d", rec.Code)
	}
	files, _ := attachments.New(t.TempDir())
	s.Support = &support.Service{DB: db, Files: files}
	tk, err := s.Support.Open(ctx, store.Ticket{AccountID: "acct-1", DisplayName: "Leia", Subject: "Friend list empty", Category: "switch"},
		"Nothing shows", &support.Upload{Name: "shot.png", Body: strings.NewReader("png")})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(mux, "/admin/tickets")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Friend list empty") {
		t.Fatalf("queue: %d", rec.Code)
	}
	if rec := get(mux, "/admin/tickets?status=closed"); strings.Contains(rec.Body.String(), "Friend list empty") {
		t.Fatal("open ticket in the closed view")
	}
	if rec := get(mux, "/admin/tickets/1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "shot.png") {
		t.Fatalf("detail: %d", rec.Code)
	}
	if rec := get(mux, "/admin/tickets/attachments/1"); rec.Code != http.StatusOK || rec.Body.String() != "png" {
		t.Fatalf("download: %d", rec.Code)
	}

	// An internal note (multipart, the way the form sends it) leaves the status alone.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", "tok-sess-admin")
	_ = mw.WriteField("message", "Checked the adapter logs")
	_ = mw.WriteField("internal", "1")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/tickets/1", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("note: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := db.GetTicket(ctx, tk.ID); got.Status != store.TicketOpen || got.Messages != 1 {
		t.Fatalf("after note: %+v", got)
	}
	// Without the token nothing is written.
	if rec := post(mux, "/admin/tickets/1", url.Values{"message": {"x"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("reply without csrf: %d", rec.Code)
	}
	// A public reply waits on the person; reply-and-close closes.
	post(mux, "/admin/tickets/1", url.Values{"csrf": {"tok-sess-admin"}, "message": {"Fixed in 0.3"}})
	if got, _ := db.GetTicket(ctx, tk.ID); got.Status != store.TicketWaiting || got.Messages != 2 {
		t.Fatalf("after reply: %+v", got)
	}
	post(mux, "/admin/tickets/1", url.Values{"csrf": {"tok-sess-admin"}, "message": {"Closing"}, "close": {"1"}})
	if got, _ := db.GetTicket(ctx, tk.ID); got.Status != store.TicketClosed {
		t.Fatalf("after reply+close: %+v", got)
	}
	if rec := post(mux, "/admin/tickets/1/reopen", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reopen: %d", rec.Code)
	}
	if rec := post(mux, "/admin/tickets/1/assign", url.Values{"csrf": {"tok-sess-admin"}, "assignee": {"acct-stranger"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("assign to a non-admin: %d", rec.Code)
	}
	post(mux, "/admin/tickets/1/assign", url.Values{"csrf": {"tok-sess-admin"}, "assignee": {"acct-admin"}})
	got, _ := db.GetTicket(ctx, tk.ID)
	if got.Status != store.TicketOpen || got.Assignee != "acct-admin" {
		t.Fatalf("after reopen+assign: %+v", got)
	}
	if rec := get(mux, "/admin/tickets?status=open&mine=1"); !strings.Contains(rec.Body.String(), "Friend list empty") {
		t.Fatal("mine view misses the assigned ticket")
	}
	// The person sees the replies and not the note.
	_, msgs, _ := s.Support.Own(ctx, tk.ID, "acct-1")
	for _, m := range msgs {
		if m.Internal {
			t.Fatal("note leaked into the public thread")
		}
	}
	audit, _ := db.ListAudit(ctx, 10)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
	}
	if strings.Join(actions, ",") != "ticket.assign,ticket.reopen,ticket.close,ticket.reply,ticket.reply,ticket.note" {
		t.Fatalf("audit: %v", actions)
	}
}
