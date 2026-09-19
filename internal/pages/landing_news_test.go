package pages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/news"
)

// The landing page shows the latest published articles when the news feed
// answers, and nothing at all when it does not.
func TestLandingNews(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"articles":[{"slug":"hello","published_at":"2026-09-01T00:00:00Z",
			"locales":[{"language":"en-US","title":"Hello from OpenPak","body":"News is live.\n\nMore later."}]}]}`))
	}))
	defer feed.Close()
	s, _, _ := newTestServer(t)
	s.News = news.NewFeed(feed.URL)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Hello from OpenPak") || !strings.Contains(body, "News is live.") || strings.Contains(body, "More later.") {
		t.Fatalf("landing with news: %d", rec.Code)
	}

	feed.Close()
	s.News = news.NewFeed(feed.URL)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `id="news-h"`) {
		t.Fatalf("landing with a failing feed: %d", rec.Code)
	}
}
