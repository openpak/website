package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

// mintFor signs an account in and returns its bearer.
func mintFor(t *testing.T, s *Server, email, password string) string {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: email, Password: password})
	var minted struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)
	if minted.Token == "" {
		t.Fatalf("mint %s: %d %s", email, rec.Code, rec.Body.String())
	}
	return minted.Token
}

func TestTicketsAPI(t *testing.T) {
	s, fc, db := newTestAPI(t)
	fc.accounts["b@x.test"], fc.verified["b@x.test"] = "password-B-1", true
	tokA := mintFor(t, s, "a@x.test", "password-A-1")
	tokB := mintFor(t, s, "b@x.test", "password-B-1")

	if rec := do(t, s, http.MethodGet, "/api/v1/me/tickets", tokA, nil); rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), "tickets_unavailable") {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	files, _ := attachments.New(t.TempDir())
	s.Support = &support.Service{DB: db, Files: files}

	if rec := do(t, s, http.MethodGet, "/api/v1/me/tickets", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/tickets", tokA, map[string]string{"subject": "x", "category": "bogus", "message": "m"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad category: %d", rec.Code)
	}
	rec := do(t, s, http.MethodPost, "/api/v1/me/tickets", tokA, map[string]string{"subject": "Saves vanished", "category": "saves", "message": "All gone"})
	var created struct {
		ID     int64
		Status string
		Thread []struct{ From, Body string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != http.StatusCreated || created.Status != "open" || len(created.Thread) != 1 || created.Thread[0].From != "you" {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	if tk, _ := db.GetTicket(context.Background(), created.ID); tk.Email != "a@x.test" {
		t.Fatalf("stored ticket: %+v", tk)
	}

	// A reply with a file, as multipart.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("message", "Here is the log")
	fw, _ := mw.CreateFormFile("attachment", "app.log")
	_, _ = fw.Write([]byte("log line"))
	_ = mw.Close()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/tickets/1/messages", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tokA)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var msg struct {
		ID         int64
		Attachment struct{ Name, URL string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &msg)
	if rec.Code != http.StatusCreated || msg.Attachment.Name != "app.log" || msg.Attachment.URL != "/api/v1/me/tickets/1/attachments/2" {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodGet, msg.Attachment.URL, tokA, nil); rec.Code != http.StatusOK || rec.Body.String() != "log line" {
		t.Fatalf("download: %d", rec.Code)
	}

	// Staff answer (and a note the app must never see).
	sup := s.Support
	_, _ = sup.StaffReply(context.Background(), 1, "adm", "internal only", true, nil, "")
	_, _ = sup.StaffReply(context.Background(), 1, "adm", "Restored from backup", false, nil, "")
	rec = do(t, s, http.MethodGet, "/api/v1/me/tickets/1", tokA, nil)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `"status":"waiting"`) ||
		!strings.Contains(body, "Restored from backup") || strings.Contains(body, "internal only") || !strings.Contains(body, `"from":"staff"`) {
		t.Fatalf("thread: %d %s", rec.Code, body)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/tickets", tokA, nil)
	if !strings.Contains(rec.Body.String(), `"categories"`) || !strings.Contains(rec.Body.String(), "Saves vanished") {
		t.Fatalf("list: %s", rec.Body.String())
	}

	// Another account sees nothing of it.
	for _, path := range []string{"/api/v1/me/tickets/1", "/api/v1/me/tickets/1/attachments/2"} {
		if rec := do(t, s, http.MethodGet, path, tokB, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s as B: %d", path, rec.Code)
		}
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/tickets/1/messages", tokB, map[string]string{"message": "hi"}); rec.Code != http.StatusNotFound {
		t.Fatalf("reply as B: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/tickets", tokB, nil); strings.Contains(rec.Body.String(), "Saves vanished") {
		t.Fatal("B lists A's ticket")
	}
	// A banned account's sessions are gone, so its bearer stops working.
	delete(fc.sessions, "sess-a@x.test")
	if rec := do(t, s, http.MethodGet, "/api/v1/me/tickets", tokA, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after ban: %d", rec.Code)
	}
	if tk, _ := db.GetTicket(context.Background(), 1); tk.Status != store.TicketWaiting {
		t.Fatalf("status: %s", tk.Status)
	}
}
