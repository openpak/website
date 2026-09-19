package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/openpak/website/internal/store"
)

// catalogDo sends a catalog write as the key holder (auth "key"), a signed-in admin with the
// CSRF header ("admin"), or with whatever Authorization header is given.
func catalogDo(t *testing.T, s *Server, method, target, auth string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	switch auth {
	case "admin", "admin-no-csrf", "player":
		sid := "sess-" + auth
		acct := map[string]string{"admin": "acct-admin", "admin-no-csrf": "acct-admin", "player": "acct-player"}[auth]
		_ = s.DB.PutWebSession(context.Background(), store.WebSession{CoreSessionID: sid, AccountID: acct})
		req.AddCookie(&http.Cookie{Name: "opak_session", Value: sid})
		if auth != "admin-no-csrf" {
			req.Header.Set("X-CSRF-Token", s.Sessions.CSRFToken(sid))
		}
	case "":
	default:
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type entry struct {
	ID          int64  `json:"id"`
	Status      string `json:"status"`
	Notes       string `json:"notes"`
	Sort        int    `json:"sort"`
	DateAdded   string `json:"date_added"`
	LastUpdated string `json:"last_updated"`
}

func TestCatalogWrites(t *testing.T) {
	t.Setenv("WEBSITE_CATALOG_KEY", "k3y")
	s, _, db := newTestAPI(t)
	s.AdminIDs = []string{"acct-admin"}
	const key = "Bearer k3y"

	// Create through the key.
	rec := catalogDo(t, s, "POST", "/api/v1/admin/titles", key, map[string]any{
		"kind": "title", "name": "Kirby's Dream Buffet", "console": "Switch", "status": "beta"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var e entry
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.DateAdded == "" || e.DateAdded != e.LastUpdated {
		t.Fatalf("create dates: %+v", e)
	}
	id := strconv.FormatInt(e.ID, 10)
	if ch := db.RequestChanges(e.ID); len(ch) != 1 || ch[0].Action != "create" || ch[0].Actor != "api-key" ||
		ch[0].Changes["status"].New != "beta" || ch[0].Changes["status"].Old != nil {
		t.Fatalf("create log: %+v", ch)
	}
	// The same (kind, name, console) again is a conflict.
	if rec := catalogDo(t, s, "POST", "/api/v1/admin/titles", key, map[string]any{
		"kind": "title", "name": "Kirby's Dream Buffet", "console": "Switch", "status": "live"}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create: %d", rec.Code)
	}

	// Update through an admin session: status and notes change, the date moves, the log says
	// what each field was.
	time.Sleep(10 * time.Millisecond)
	before, _ := db.ListRequests(context.Background(), "", "")
	rec = catalogDo(t, s, "PATCH", "/api/v1/admin/titles/"+id, "admin", map[string]any{"status": "live", "notes": "played online"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	after, _ := db.ListRequests(context.Background(), "", "")
	if !after[0].LastUpdated.After(before[0].LastUpdated) || !after[0].DateAdded.Equal(before[0].DateAdded) {
		t.Fatalf("last_updated must move and date_added stay: %v -> %v", before[0], after[0])
	}
	ch := db.RequestChanges(e.ID)
	if len(ch) != 2 || ch[1].Actor != "acct-admin" || ch[1].Changes["status"] != (store.FieldChange{Old: "beta", New: "live"}) ||
		len(ch[1].Changes) != 2 {
		t.Fatalf("update log: %+v", ch)
	}
	// A patch that changes nothing logs nothing.
	catalogDo(t, s, "PATCH", "/api/v1/admin/titles/"+id, key, map[string]any{"status": "live"})
	if n := len(db.RequestChanges(e.ID)); n != 2 {
		t.Fatalf("no-op patch logged: %d rows", n)
	}

	// Validation: an unknown status, a misspelt field, kind changes.
	for _, body := range []map[string]any{{"status": "shipped"}, {"stauts": "live"}, {"kind": "feature"}} {
		if rec := catalogDo(t, s, "PATCH", "/api/v1/admin/titles/"+id, key, body); rec.Code != http.StatusBadRequest {
			t.Errorf("patch %v: %d, want 400", body, rec.Code)
		}
	}
	if rec := catalogDo(t, s, "POST", "/api/v1/admin/titles", key, map[string]any{
		"kind": "title", "name": "X", "status": "done"}); rec.Code != http.StatusBadRequest {
		t.Errorf("create with a bad status: %d", rec.Code)
	}

	// A title with votes cannot be deleted; one without can, and the delete is logged.
	voted, _ := db.CreateRequest(context.Background(), store.Request{Kind: "title", Name: "Tetris 99", Status: "requestable"}, "t")
	if _, err := db.ToggleVote(context.Background(), voted.ID, "acct-a"); err != nil {
		t.Fatal(err)
	}
	if rec := catalogDo(t, s, "DELETE", "/api/v1/admin/titles/"+strconv.FormatInt(voted.ID, 10), key, nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete voted: %d", rec.Code)
	}
	if rec := catalogDo(t, s, "DELETE", "/api/v1/admin/titles/"+id, key, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if ch := db.RequestChanges(e.ID); ch[len(ch)-1].Action != "delete" || ch[len(ch)-1].Changes["name"].New != nil {
		t.Fatalf("delete log: %+v", ch)
	}
	if rec := catalogDo(t, s, "DELETE", "/api/v1/admin/titles/"+id, key, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestCatalogWriteAuth(t *testing.T) {
	s, _, db := newTestAPI(t)
	s.AdminIDs = []string{"acct-admin"}
	r, _ := db.CreateRequest(context.Background(), store.Request{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "beta"}, "t")
	path := "/api/v1/admin/titles/" + strconv.FormatInt(r.ID, 10)
	patch := map[string]any{"status": "live"}

	// Without WEBSITE_CATALOG_KEY there is no bearer path, not even for an empty key.
	for _, auth := range []string{"Bearer ", "Bearer anything"} {
		if rec := catalogDo(t, s, "PATCH", path, auth, patch); rec.Code != http.StatusUnauthorized {
			t.Errorf("no key set, %q: %d", auth, rec.Code)
		}
	}
	t.Setenv("WEBSITE_CATALOG_KEY", "k3y")
	for auth, want := range map[string]int{
		"":              http.StatusUnauthorized, // nobody
		"Bearer wrong":  http.StatusUnauthorized,
		"player":        http.StatusForbidden, // signed in, not an admin
		"admin-no-csrf": http.StatusForbidden, // an admin's cookie sent by another site
	} {
		if rec := catalogDo(t, s, "PATCH", path, auth, patch); rec.Code != want {
			t.Errorf("%q: %d, want %d", auth, rec.Code, want)
		}
	}
	if n := len(db.RequestChanges(r.ID)); n != 1 { // the create above
		t.Fatalf("refused writes were logged: %d", n)
	}
	if rec := catalogDo(t, s, "PATCH", path, "admin", patch); rec.Code != http.StatusOK {
		t.Fatalf("admin session: %d %s", rec.Code, rec.Body)
	}
	if rec := catalogDo(t, s, "PATCH", path, "Bearer k3y", map[string]any{"status": "beta"}); rec.Code != http.StatusOK {
		t.Fatalf("key: %d %s", rec.Code, rec.Body)
	}
}

// The export is the old catalog.json: a bare array without ids, plus the two dates.
func TestCatalogExport(t *testing.T) {
	s, _, db := newTestAPI(t)
	_, _ = db.CreateRequest(context.Background(), store.Request{Kind: "feature", Name: "Cloud saves", Status: "working", Sort: -1}, "t")
	rec := do(t, s, "GET", "/api/v1/catalog.json", "", nil)
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got) != 1 {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	for _, k := range []string{"kind", "name", "console", "category", "backend", "status", "notes", "date_added", "last_updated"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("export lacks %q", k)
		}
	}
	if _, ok := got[0]["id"]; ok {
		t.Error("export carries ids; catalog.json never had them")
	}
}
