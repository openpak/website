package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc"

	newsv1 "github.com/openpak/website/internal/newspb"
)

// fakeNews implements the few news.v1 calls the admin pages make; the
// embedded interface panics on anything else, which is the point.
type fakeNews struct {
	newsv1.NewsClient
	created   *newsv1.CreateArticleRequest
	published []string
}

func (f *fakeNews) CreateArticle(_ context.Context, in *newsv1.CreateArticleRequest, _ ...grpc.CallOption) (*newsv1.Article, error) {
	f.created = in
	return &newsv1.Article{Id: "a1", Slug: in.GetSlug(), Locales: in.GetLocales()}, nil
}

func (f *fakeNews) ListArticles(context.Context, *newsv1.ListArticlesRequest, ...grpc.CallOption) (*newsv1.ListArticlesResponse, error) {
	return &newsv1.ListArticlesResponse{Articles: []*newsv1.Article{
		{Id: "a1", Slug: "hello", Locales: []*newsv1.ArticleLocale{{Language: "en-US", Title: "Hello from OpenPak"}}},
	}}, nil
}

func (f *fakeNews) PublishArticle(_ context.Context, in *newsv1.PublishArticleRequest, _ ...grpc.CallOption) (*newsv1.Article, error) {
	f.published = append(f.published, in.GetId())
	return &newsv1.Article{Id: in.GetId(), Slug: "hello", PublishedAt: "2026-09-19T10:00:00Z"}, nil
}

func post(mux *http.ServeMux, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestNewsAdmin(t *testing.T) {
	s, db, mux := adminMux(t)

	// Unset: the page says so, it does not fail.
	if rec := get(mux, "/admin/news"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("news unset: %d", rec.Code)
	}

	fake := &fakeNews{}
	s.News = fake
	if rec := get(mux, "/admin/news"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hello from OpenPak") {
		t.Fatalf("news list: %d", rec.Code)
	}

	rec := post(mux, "/admin/news", url.Values{"csrf": {"tok-sess-admin"}, "slug": {"hello"}, "platforms": {"web, android"},
		"language": {"en-US"}, "title": {"Hello"}, "body": {"One.\r\n\r\nTwo."}})
	if rec.Code != http.StatusSeeOther || fake.created == nil {
		t.Fatalf("create: %d", rec.Code)
	}
	if p := fake.created.GetAudience().GetPlatforms(); len(p) != 2 || p[1] != "android" || fake.created.GetLocales()[0].GetBody() != "One.\n\nTwo." {
		t.Fatalf("create request: %+v", fake.created)
	}

	// A publish without the CSRF token is refused before the news service hears of it.
	if rec := post(mux, "/admin/news/a1/publish", url.Values{}); rec.Code != http.StatusForbidden || len(fake.published) != 0 {
		t.Fatalf("publish without csrf: %d %v", rec.Code, fake.published)
	}
	if rec := post(mux, "/admin/news/a1/publish", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusSeeOther || len(fake.published) != 1 {
		t.Fatalf("publish: %d", rec.Code)
	}

	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 2 || audit[0].Action != "news.publish" || audit[1].Action != "news.create" {
		t.Fatalf("audit: %+v", audit)
	}
}
