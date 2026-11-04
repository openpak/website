package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openpak/website/internal/store"
)

// fakePakka is just enough of Pakka's publisher admin API to drive the page.
type fakePakka struct {
	mu       sync.Mutex
	killed   bool
	drafts   map[string]*pakkaDraft
	calls    []string
	approver string
	edit     pakkaEdit
	reason   string
	down     bool
}

func newFakePakka() *fakePakka {
	at := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	f := &fakePakka{drafts: map[string]*pakkaDraft{}}
	post := pakkaAction{Tool: "openverse.post", Category: "highlights", Body: "Best of OpenVerse\nA <b>great</b> week for Splatoon drawings."}
	post.Community.Name = "Announcements"
	f.drafts["a1b2c3d4"] = &pakkaDraft{ID: "a1b2c3d4", Action: post, Preview: "post to Announcements: Best of OpenVerse A great week", CreatedBy: "model", CreatedAt: at, Status: "pending"}
	news := pakkaAction{Tool: "news.publish", Category: "news", Title: "Maintenance window", Body: "Servers restart on Friday."}
	f.drafts["e5f6a7b8"] = &pakkaDraft{ID: "e5f6a7b8", Action: news, Preview: `News "Maintenance window"`, CreatedBy: "model", CreatedAt: at.Add(time.Hour), Status: "pending"}
	tmpl := pakkaAction{Tool: "openverse.post", Category: "splatfest", Template: "result"}
	f.drafts["99887766"] = &pakkaDraft{ID: "99887766", Action: tmpl, Preview: "Team Pizza won the Pizza vs Pasta Splatfest!", CreatedBy: "system:events", CreatedAt: at.Add(2 * time.Hour), Status: "pending"}
	return f
}

func (f *fakePakka) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, `{"error":"down"}`, http.StatusBadGateway)
		return
	}
	if r.Header.Get("X-Admin-Key") != "pk-admin" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	f.approver = r.Header.Get("X-Approver")
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	enc := json.NewEncoder(w)
	switch {
	case r.URL.Path == "/v1/publisher/status":
		_ = enc.Encode(pakkaStatus{Killed: f.killed, Configured: true, CategoriesOn: []string{"highlights", "news", "splatfest"},
			AutoCategory: []string{"news_mirror", "splatfest"}, Queue: map[string]int{"pending": 3}})
	case r.URL.Path == "/v1/publisher/drafts" && r.Method == http.MethodGet:
		var out []*pakkaDraft
		for _, id := range []string{"a1b2c3d4", "e5f6a7b8", "99887766"} {
			if d := f.drafts[id]; d.Status == "pending" || r.URL.Query().Get("status") == "all" {
				out = append(out, d)
			}
		}
		_ = enc.Encode(map[string]any{"drafts": out})
	case r.URL.Path == "/v1/publisher/kill":
		f.killed = true
		_ = enc.Encode(map[string]bool{"killed": true})
	case strings.HasPrefix(r.URL.Path, "/v1/publisher/drafts/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/publisher/drafts/"), "/")
		d := f.drafts[parts[0]]
		if d == nil {
			http.Error(w, `{"error":"no such draft"}`, http.StatusNotFound)
			return
		}
		switch {
		case len(parts) == 1:
			_ = enc.Encode(map[string]any{"draft": d, "history": []pakkaAudit{
				{Time: d.CreatedAt, Actor: d.CreatedBy, Event: "queued", DraftID: d.ID, Detail: d.Preview}}})
		case parts[1] == "approve":
			if f.killed {
				http.Error(w, `{"error":"publishing is switched off (kill switch)"}`, http.StatusServiceUnavailable)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&f.edit)
			if f.edit.Body != "" {
				orig := d.Action
				d.Original, d.Action.Body, d.EditedBy = &orig, f.edit.Body, f.approver
			}
			d.Status, d.Reviewer = "published", f.approver
			_ = enc.Encode(d)
		case parts[1] == "reject":
			var req struct{ Reason string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.reason = req.Reason
			d.Status, d.Reviewer, d.RejectReason = "rejected", f.approver, req.Reason
			_ = enc.Encode(d)
		}
	default:
		http.NotFound(w, r)
	}
}

func pakkaAdmin(t *testing.T) (*Server, *http.ServeMux, *fakePakka, *store.Memory) {
	t.Helper()
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root"})
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return s, mux, newFakePakka(), db
}

func pakkaPost(mux *http.ServeMux, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAdminPakkaQueue(t *testing.T) {
	s, mux, fp, db := pakkaAdmin(t)

	// Without Pakka configured the section is not in the nav and the page is not there.
	if rec := get(mux, "/admin/pakka"); rec.Code != http.StatusNotFound {
		t.Fatalf("no pakka: %d", rec.Code)
	}
	if strings.Contains(get(mux, "/admin/users").Body.String(), "/admin/pakka") {
		t.Error("the nav shows a section with no backend")
	}

	srv := httptest.NewServer(fp)
	defer srv.Close()
	s.Pakka = NewPakkaAPI(srv.URL, "pk-admin")

	rec := get(mux, "/admin/pakka")
	body := rec.Body.String()
	for _, want := range []string{"settings-nav", `href="/admin/pakka"`, "Maintenance window", "Best of OpenVerse", "Announcements", "Waiting (3)", "Switch publishing off"} {
		if rec.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("queue lacks %q (%d)", want, rec.Code)
		}
	}
	if strings.Contains(body, "<b>great</b>") {
		t.Error("model text was not escaped")
	}

	// Edit then approve: the page sends only what changed, under the staff member's name.
	rec = get(mux, "/admin/pakka/a1b2c3d4")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="body"`) || !strings.Contains(rec.Body.String(), "Reject") {
		t.Fatalf("review page: %d", rec.Code)
	}
	if rec := pakkaPost(mux, "/admin/pakka/a1b2c3d4/approve", url.Values{"body": {"Best of OpenVerse\nA calm week."}}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	rec = pakkaPost(mux, "/admin/pakka/a1b2c3d4/approve", url.Values{"csrf": {"tok-sess-admin"}, "body": {"Best of OpenVerse\nA calm week."}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/pakka/a1b2c3d4?m=approved" {
		t.Fatalf("approve: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if fp.edit.Body != "Best of OpenVerse\nA calm week." || fp.approver != "Root" {
		t.Errorf("edit=%+v approver=%q", fp.edit, fp.approver)
	}

	// Approving as written sends no edit.
	fp.edit = pakkaEdit{}
	rec = pakkaPost(mux, "/admin/pakka/e5f6a7b8/approve", url.Values{"csrf": {"tok-sess-admin"}, "title": {"Maintenance window"}, "body": {"Servers restart on Friday."}})
	if rec.Code != http.StatusSeeOther || fp.edit != (pakkaEdit{}) {
		t.Errorf("unchanged approve: %d edit=%+v", rec.Code, fp.edit)
	}

	// A templated post cannot be edited through the form either.
	fp.edit = pakkaEdit{}
	pakkaPost(mux, "/admin/pakka/99887766/approve", url.Values{"csrf": {"tok-sess-admin"}, "body": {"smuggled"}})
	if fp.edit.Body != "" {
		t.Error("a templated draft was edited")
	}

	// Both decisions are in the website's own audit log.
	audit, _ := db.ListAudit(context.Background(), 20)
	var kinds []string
	for _, a := range audit {
		kinds = append(kinds, a.Action)
	}
	if !strings.Contains(strings.Join(kinds, " "), "pakka.approve") {
		t.Errorf("audit lacks the approval: %v", kinds)
	}
}

func TestAdminPakkaRejectKillAndFailure(t *testing.T) {
	s, mux, fp, _ := pakkaAdmin(t)
	srv := httptest.NewServer(fp)
	defer srv.Close()
	s.Pakka = NewPakkaAPI(srv.URL, "pk-admin")

	// A rejection needs a reason.
	if rec := pakkaPost(mux, "/admin/pakka/a1b2c3d4/reject", url.Values{"csrf": {"tok-sess-admin"}, "reason": {"  "}}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty reason: %d", rec.Code)
	}
	rec := pakkaPost(mux, "/admin/pakka/a1b2c3d4/reject", url.Values{"csrf": {"tok-sess-admin"}, "reason": {"Not accurate"}})
	if rec.Code != http.StatusSeeOther || fp.reason != "Not accurate" {
		t.Fatalf("reject: %d %q", rec.Code, fp.reason)
	}
	if body := get(mux, "/admin/pakka/a1b2c3d4?m=rejected").Body.String(); !strings.Contains(body, "Not accurate") || !strings.Contains(body, "Rejected. Nothing was published.") {
		t.Error("the rejection and its reason are not shown")
	}

	// The kill switch needs the confirmation box; once on, the page says so and approvals are refused with Pakka's words.
	if rec := pakkaPost(mux, "/admin/pakka/kill", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusBadRequest || fp.killed {
		t.Errorf("kill without confirmation: %d", rec.Code)
	}
	if rec := pakkaPost(mux, "/admin/pakka/kill", url.Values{"csrf": {"tok-sess-admin"}, "confirm": {"1"}}); rec.Code != http.StatusSeeOther || !fp.killed {
		t.Fatalf("kill: %d", rec.Code)
	}
	if body := get(mux, "/admin/pakka").Body.String(); !strings.Contains(body, "Publishing is switched off") || strings.Contains(body, "Switch publishing off") {
		t.Error("the page does not show the switch as on")
	}
	rec = pakkaPost(mux, "/admin/pakka/e5f6a7b8/approve", url.Values{"csrf": {"tok-sess-admin"}})
	if rec.Code == http.StatusSeeOther || !strings.Contains(rec.Body.String(), "kill switch") {
		t.Errorf("approve while killed: %d %s", rec.Code, rec.Body)
	}

	// Pakka down says so inside the shell; an unknown draft is a 404.
	if rec := get(mux, "/admin/pakka/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown draft: %d", rec.Code)
	}
	fp.down = true
	rec = get(mux, "/admin/pakka")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Pakka did not answer") {
		t.Errorf("down: %d", rec.Code)
	}
}
