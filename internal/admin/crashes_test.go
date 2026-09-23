package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/store"
)

func TestCrashesAdmin(t *testing.T) {
	s, db, mux := adminMux(t)
	ctx := context.Background()
	files, _ := attachments.New(t.TempDir())
	s.CrashFiles = files
	saved, _ := files.Save("crashes", strings.NewReader("dump"), 100)
	_ = db.SeedRequests(ctx, []store.Request{{Kind: "title", Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "live", TitleID: "0100152000022000"}})
	for i, msg := range []string{"null deref at 0x10", "null deref at 0x20", "gpu hang"} {
		c := store.CrashReport{Source: "ryujinx", Version: "1.3." + string(rune('0'+i)), TitleID: "0100152000022000",
			ErrorCode: "2002-0001", Message: msg, Fields: json.RawMessage(`{"gpu":"RADV"}`)}
		if i == 0 {
			c.Attachment = store.Attachment{Key: saved.Key, Name: "crash.dmp", Size: saved.Size}
		}
		if _, err := db.CreateCrash(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = db.CreateCrash(ctx, store.CrashReport{Source: "console", ErrorCode: "2110-3127", Firmware: "22.5.0", DeviceID: "dev-9"})

	rec := get(mux, "/admin/crashes")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "2 × ryujinx 2002-0001") || !strings.Contains(body, "Mario Kart 8 Deluxe") ||
		!strings.Contains(body, "1.3.0, 1.3.1") || !strings.Contains(body, "1 × console") {
		t.Fatalf("groups: %d %s", rec.Code, body)
	}
	// Filters: source, error code, version, dates.
	if body := get(mux, "/admin/crashes?source=console").Body.String(); strings.Contains(body, "ryujinx 2002") || !strings.Contains(body, "console") {
		t.Fatal("source filter")
	}
	if body := get(mux, "/admin/crashes?view=list&version=1.3.2").Body.String(); !strings.Contains(body, "gpu hang") || strings.Contains(body, "null deref") {
		t.Fatal("version filter")
	}
	if body := get(mux, "/admin/crashes?to=2000-01-01").Body.String(); !strings.Contains(body, "No crash reports match") {
		t.Fatal("date filter")
	}
	if body := get(mux, "/admin/crashes?error_code=2110-3127&view=list").Body.String(); !strings.Contains(body, "#4") || strings.Contains(body, "#1<") {
		t.Fatal("error code filter")
	}

	// Detail with fields and the file.
	rec = get(mux, "/admin/crashes/1")
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "RADV") || !strings.Contains(body, "crash.dmp") ||
		!strings.Contains(body, "2 reports with this signature") {
		t.Fatalf("detail: %d %s", rec.Code, body)
	}
	if rec := get(mux, "/admin/crashes/1/attachment"); rec.Code != http.StatusOK || rec.Body.String() != "dump" {
		t.Fatalf("file: %d", rec.Code)
	}
	if rec := get(mux, "/admin/crashes/2/attachment"); rec.Code != http.StatusNotFound {
		t.Fatalf("no file: %d", rec.Code)
	}

	// Resolve a group (CSRF required), then one report back open.
	c1, _ := db.GetCrash(ctx, 1)
	if rec := post(mux, "/admin/crashes/group/"+c1.Signature+"/resolve", url.Values{}); rec.Code != http.StatusForbidden {
		t.Fatalf("resolve without csrf: %d", rec.Code)
	}
	rec = post(mux, "/admin/crashes/group/"+c1.Signature+"/resolve", url.Values{"csrf": {"tok-sess-admin"}, "back": {"/admin/crashes?source=ryujinx"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/crashes?source=ryujinx&m=resolved" {
		t.Fatalf("resolve group: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if open, _ := db.ListCrashes(ctx, store.CrashFilter{Status: "open", Source: "ryujinx"}, 10); len(open) != 1 {
		t.Fatalf("open after group resolve: %d", len(open))
	}
	post(mux, "/admin/crashes/2/resolve", url.Values{"csrf": {"tok-sess-admin"}, "resolved": {"0"}, "back": {"https://evil.test/"}})
	if c, _ := db.GetCrash(ctx, 2); c.Resolved {
		t.Fatal("reopen one report")
	}

	// Link to a ticket; an unknown ticket says so.
	tk, _ := db.CreateTicket(ctx, store.Ticket{AccountID: "a", Subject: "crash", Category: "emulator"}, store.TicketMessage{Body: "see crash"})
	if rec := post(mux, "/admin/crashes/1/ticket", url.Values{"csrf": {"tok-sess-admin"}, "ticket": {"#999"}}); !strings.Contains(rec.Header().Get("Location"), "m=noticket") {
		t.Fatalf("unknown ticket: %s", rec.Header().Get("Location"))
	}
	post(mux, "/admin/crashes/1/ticket", url.Values{"csrf": {"tok-sess-admin"}, "ticket": {"1"}})
	if c, _ := db.GetCrash(ctx, 1); c.TicketID != tk.ID {
		t.Fatalf("linked: %+v", c)
	}
	if body := get(mux, "/admin/crashes/1").Body.String(); !strings.Contains(body, `href="/admin/tickets/1"`) {
		t.Fatal("detail misses the ticket link")
	}
	audit, _ := db.ListAudit(ctx, 10)
	if len(audit) != 3 || audit[0].Action != "crash.ticket" || audit[2].Action != "crash.resolve" {
		t.Fatalf("audit: %+v", audit)
	}
}
