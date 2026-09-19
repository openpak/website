package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestTitlesAdmin(t *testing.T) {
	_, db, mux := adminMux(t)
	ctx := context.Background()
	_ = db.SeedRequests(ctx, []store.Request{{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "beta"}})
	titles, _ := db.ListRequests(ctx, "title", "")
	id := strconv.FormatInt(titles[0].ID, 10)

	if rec := get(mux, "/admin/titles"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Splatoon 3") {
		t.Fatalf("titles page: %d", rec.Code)
	}
	if rec := post(mux, "/admin/titles", url.Values{"id": {id}, "status": {"live"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("save without csrf: %d", rec.Code)
	}
	rec := post(mux, "/admin/titles", url.Values{"csrf": {"tok-sess-admin"}, "id": {id}, "status": {"live"}, "notes": {"verified on hardware"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	titles, _ = db.ListRequests(ctx, "title", "")
	audit, _ := db.ListAudit(ctx, 10)
	if titles[0].Status != "live" || titles[0].Notes != "verified on hardware" || len(audit) != 1 || !strings.Contains(audit[0].Detail, "beta -> live") {
		t.Fatalf("saved %+v, audit %+v", titles[0], audit)
	}
}

func TestModsAdmin(t *testing.T) {
	var decisions []string
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mods-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/internal/queue":
			_, _ = w.Write([]byte(`{"queue":[{"id":"m1","name":"Blue Hat","version":"1.0","title_id":"0100000000010000","licence":"CC-BY-4.0","source_url":"https://example.org/blue-hat","status":"submitted","extra":1}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/internal/mods/gone/takedown":
			http.Error(w, `{"error":"no such mod"}`, http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/internal/mods/"):
			var body struct{ Reason string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			decisions = append(decisions, r.URL.Path+" "+body.Reason)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer svc.Close()

	s, db, mux := adminMux(t)
	if rec := get(mux, "/admin/mods"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("mods unset: %d", rec.Code)
	}
	s.Mods = NewModsAPI(svc.URL, "mods-key")
	if rec := get(mux, "/admin/mods"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Blue Hat") {
		t.Fatalf("queue: %d", rec.Code)
	}
	if rec := post(mux, "/admin/mods/m1/publish", url.Values{}); rec.Code != http.StatusForbidden || len(decisions) != 0 {
		t.Fatalf("publish without csrf: %d %v", rec.Code, decisions)
	}
	if rec := post(mux, "/admin/mods/m1/reject", url.Values{"csrf": {"tok-sess-admin"}, "reason": {"no licence file"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject: %d", rec.Code)
	}
	if len(decisions) != 1 || decisions[0] != "/internal/mods/m1/reject no licence file" {
		t.Fatalf("decisions: %v", decisions)
	}
	// A refusal from the service is shown with its words, and not audited.
	rec := post(mux, "/admin/mods/takedown", url.Values{"csrf": {"tok-sess-admin"}, "id": {"gone"}, "reason": {"x"}})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no such mod") {
		t.Fatalf("takedown of an unknown mod: %d", rec.Code)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "mods.reject" || !strings.Contains(audit[0].Detail, "no licence file") {
		t.Fatalf("audit: %+v", audit)
	}
}
