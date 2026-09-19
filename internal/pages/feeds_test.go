package pages

import (
	"context"
	"encoding/xml"
	"net/http/httptest"
	"testing"
)

func TestFeeds(t *testing.T) {
	s, _, db := newTestServer(t)
	ctx := context.Background()
	db.AddAudit(ctx, "admin-1", "title.status", `7 Mario Kart 8: planned -> working notes="secret"`)
	db.AddAudit(ctx, "admin-1", "user.suspend", "acct-9")

	get := func(path string) atomFeed {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/atom+xml; charset=utf-8" {
			t.Fatalf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
		var f atomFeed
		if err := xml.Unmarshal(w.Body.Bytes(), &f); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, w.Body)
		}
		return f
	}
	f := get("/feed/titles.xml")
	if len(f.Entries) != 1 || f.Entries[0].Title != "Mario Kart 8: planned -> working" {
		t.Errorf("titles feed entries: %+v", f.Entries)
	}
	if f := get("/feed/news.xml"); len(f.Entries) != 0 {
		t.Errorf("news feed without a news service: %+v", f.Entries)
	}
}
