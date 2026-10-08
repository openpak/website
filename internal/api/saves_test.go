package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/saves"
)

// The app's saves list carries the game's name and, for a title with one, the icon path.
func TestSavesListNamesTheGame(t *testing.T) {
	s, _, _ := newTestAPI(t)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"saves":[{"platform":"citron","title_id":"0100ABCD00001000","versions":[]},{"platform":"wii","title_id":"RMCP01","versions":[]}],"usage":{}}`))
	}))
	defer stub.Close()
	s.Saves = saves.New(stub.URL, "k")
	s.SaveTitle = func(_ context.Context, platform, id string) (string, bool) {
		if platform == "citron" {
			return "Mario Kart 8 Deluxe", true
		}
		return id, false
	}
	rec := do(t, s, http.MethodGet, "/api/v1/me/saves", mintTokenForTest(t, s), nil)
	body := rec.Body.String()
	for _, want := range []string{`"name":"Mario Kart 8 Deluxe"`, `"icon":"/titles/icon/0100ABCD00001000"`, `"name":"RMCP01"`, `"title_id":"RMCP01"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("%d: list lacks %s: %s", rec.Code, want, body)
		}
	}
	if strings.Count(body, `"icon"`) != 1 {
		t.Fatalf("an icon on a game without one: %s", body)
	}
}

// Restore, delete and download reach the service for the token's own account.
func TestSaveVersionActions(t *testing.T) {
	s, _, _ := newTestAPI(t)
	var got []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			w.Write([]byte("save-bytes"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stub.Close()
	s.Saves = saves.New(stub.URL, "k")
	tok := mintTokenForTest(t, s)
	if rec := do(t, s, http.MethodPost, "/api/v1/me/saves/versions/7/restore", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/saves/versions/7", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/saves/versions/7", tok, nil); rec.Code != 200 || rec.Body.String() != "save-bytes" {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/saves/versions/x", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	for _, g := range got {
		if !strings.Contains(g, "/versions/7") || !strings.Contains(g, "/internal/accounts/") {
			t.Fatalf("went to %q", g)
		}
	}
	if len(got) != 3 {
		t.Fatalf("calls: %v", got)
	}
}

// The emulator save routes are retired and a whole title's backups can be deleted for the
// token's own account.
func TestSaveRetiredAndDeleteTitle(t *testing.T) {
	s, _, _ := newTestAPI(t)
	var got []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		w.Write([]byte(`{"deleted":3}`))
	}))
	defer stub.Close()
	s.Saves = saves.New(stub.URL, "k")
	tok := mintTokenForTest(t, s)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		rec := do(t, s, m, "/api/v1/me/saves/switch/0100abcd", tok, nil)
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "retired") {
			t.Fatalf("%s: %d %s", m, rec.Code, rec.Body)
		}
	}
	if len(got) != 0 {
		t.Fatalf("a retired route reached the service: %v", got)
	}
	rec := do(t, s, http.MethodDelete, "/api/v1/me/saves/switch/0100abcd", tok, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":3`) {
		t.Fatalf("delete title: %d %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "DELETE /internal/accounts/") || !strings.HasSuffix(got[0], "/saves/switch/0100abcd") {
		t.Fatalf("calls: %v", got)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/saves/switch/0100abcd", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete: %d", rec.Code)
	}
}
