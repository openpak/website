// Package news connects the website to the news service: the operator gRPC
// the admin pages author through, and the public read API the landing page
// shows the latest articles from. The service owns the articles; the website
// holds none of them.
package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	newsv1 "github.com/openpak/website/internal/newspb"
)

// DialOperator connects to the authoring gRPC (news.v1.News). Every call
// carries the operator key; the service refuses anything without it.
func DialOperator(addr, key string) (newsv1.NewsClient, io.Closer, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key), method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("news: grpc dial: %w", err)
	}
	return newsv1.NewNewsClient(conn), conn, nil
}

// Article is one published article as the landing page shows it: one
// language, already picked.
type Article struct {
	Slug, Title, Body string
	Published         time.Time
}

// Summary is the first paragraph: bodies are plain text with blank lines
// between paragraphs, and the landing page has room for one.
func (a Article) Summary() string {
	p, _, _ := strings.Cut(a.Body, "\n\n")
	return strings.TrimSpace(p)
}

// Feed reads the public read API (GET /v1/articles) and keeps the answer for
// a short while: the landing page is the busiest page on the site, and news
// changes a few times a week. A nil Feed, or one whose fetch fails, has
// nothing to show, and the landing page simply has no news section.
type Feed struct {
	URL  string // base, e.g. http://openpak-news:20040
	HTTP *http.Client
	TTL  time.Duration

	mu    sync.Mutex
	at    time.Time
	items []Article
}

// NewFeed returns nil when baseURL is empty.
func NewFeed(baseURL string) *Feed {
	if baseURL == "" {
		return nil
	}
	return &Feed{URL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 2 * time.Second}, TTL: 2 * time.Minute}
}

// Latest returns up to n articles, newest first. A failed fetch is cached like
// a good one, so a news outage costs one slow request per TTL rather than one
// per visitor.
func (f *Feed) Latest(ctx context.Context, n int) []Article {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.at.IsZero() || time.Since(f.at) > f.TTL {
		f.items, _ = f.fetch(ctx)
		f.at = time.Now()
	}
	if len(f.items) > n {
		return f.items[:n]
	}
	return f.items
}

func (f *Feed) fetch(ctx context.Context) ([]Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL+"/v1/articles?"+url.Values{"platform": {"web"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news: %s", resp.Status)
	}
	var wire struct {
		Articles []struct {
			Slug        string `json:"slug"`
			PublishedAt string `json:"published_at"`
			Locales     []struct {
				Language string `json:"language"`
				Title    string `json:"title"`
				Body     string `json:"body"`
			} `json:"locales"`
		} `json:"articles"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&wire); err != nil {
		return nil, err
	}
	var out []Article
	for _, a := range wire.Articles {
		published, err := time.Parse(time.RFC3339, a.PublishedAt)
		if err != nil || len(a.Locales) == 0 {
			continue // a draft or an empty article has nothing to show
		}
		// English is the site's only language today; otherwise the first
		// locale, so an article written only in another language still shows.
		l := a.Locales[0]
		for _, c := range a.Locales {
			if strings.HasPrefix(strings.ToLower(c.Language), "en") {
				l = c
				break
			}
		}
		out = append(out, Article{Slug: a.Slug, Title: l.Title, Body: l.Body, Published: published})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	return out, nil
}
