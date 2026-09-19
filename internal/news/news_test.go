package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFeedLatest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/articles" || r.URL.Query().Get("platform") != "web" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"articles":[
			{"slug":"old","published_at":"2026-01-01T00:00:00Z","locales":[{"language":"en-US","title":"Old","body":"a"}]},
			{"slug":"new","published_at":"2026-09-01T00:00:00Z","locales":[{"language":"fr-FR","title":"Nouveau","body":"x"},{"language":"en-GB","title":"New","body":"First.\n\nSecond."}]},
			{"slug":"mid","published_at":"2026-05-01T00:00:00Z","locales":[{"language":"de","title":"Mitte","body":"m"}]},
			{"slug":"draft","locales":[{"language":"en","title":"Draft","body":"d"}]},
			{"slug":"older","published_at":"2025-01-01T00:00:00Z","locales":[{"language":"en","title":"Older","body":"o"}]}]}`))
	}))
	defer srv.Close()

	f := NewFeed(srv.URL)
	got := f.Latest(context.Background(), 3)
	if len(got) != 3 || got[0].Title != "New" || got[1].Title != "Mitte" || got[2].Title != "Old" {
		t.Fatalf("latest: %+v", got)
	}
	if got[0].Summary() != "First." {
		t.Fatalf("summary: %q", got[0].Summary())
	}
	f.Latest(context.Background(), 3)
	if hits != 1 {
		t.Fatalf("the feed must be cached, got %d fetches", hits)
	}

	// Unset or failing: nothing, silently.
	if (*Feed)(nil).Latest(context.Background(), 3) != nil {
		t.Fatal("nil feed must show nothing")
	}
	srv.Close()
	if got := NewFeed(srv.URL).Latest(context.Background(), 3); len(got) != 0 {
		t.Fatalf("failing feed must show nothing: %+v", got)
	}
}
