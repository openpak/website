package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeNewsAdmin is the news service's /admin/v1, as much as the pages use.
type fakeNewsAdmin struct {
	mu        sync.Mutex
	requests  map[string]map[string]any // "METHOD path" -> last JSON body
	failNext  bool
	imageType string
}

func (f *fakeNewsAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Internal-Key") != "news-key" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var body map[string]any
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&body)
	} else if r.Method == http.MethodPut {
		f.imageType = r.Header.Get("Content-Type")
		_, _ = io.Copy(io.Discard, r.Body)
	}
	f.requests[r.Method+" "+r.URL.Path] = body
	channel := `{"topic_id":"openpak_news","name":"OpenPak News","publisher":"OpenPak","description":"News.","icon":{"motif":"mark","hue":0},"application_ids":["0100abcd12340000"],"article_count":2,"active_article_count":1}`
	article := `{"topic_id":"openpak_news","news_id":2609230001,"title":"Welcome","body":"One paragraph.","language":"en-GB","supported_languages":["en-GB","en-US"],"lifetime_days":14,"published_at":"2026-09-23T12:00:00Z","expire_at":"2026-10-07T12:00:00Z","pickup_days":null,"priority":100,"essential_pickup":null,"art":{"motif":"burst","hue":0},"more":{"type":"browser","url":"https://openpak.org/guide"},"ever_published":true}`
	switch p := strings.TrimPrefix(r.URL.Path, "/admin/v1"); {
	case p == "/status":
		_, _ = w.Write([]byte(`{"serving":{"build":7,"since":"2026-09-23T12:00:05Z"},"revision":12,"dirty":true,"signing_configured":true,"push_configured":false,
			"last_publish":{"build":7,"at":"2026-09-23T12:00:05Z","changed_topics":["openpak_news"],"note":"first"},
			"last_error":{"at":"2026-09-23T13:00:00Z","op":"push","error":"nx-baas refused"},
			"builds":[{"build":7,"created_at":"2026-09-23T12:00:05Z","containers":117,"channels":4,"articles":33},{"build":6,"created_at":"2026-09-22T12:00:00Z"}]}`))
	case p == "/motifs":
		_, _ = w.Write([]byte(`{"icon":["mark","mark-pad"],"scene":["beacon","burst"]}`))
	case p == "/channels" && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"channels":[` + channel + `]}`))
	case p == "/channels" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(channel))
	case p == "/channels/openpak_news" || p == "/channels/openpak_news/icon":
		_, _ = w.Write([]byte(channel))
	case p == "/channels/openpak_news/articles" && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"articles":[` + article + `]}`))
	case p == "/channels/openpak_news/articles" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(article))
	case p == "/channels/openpak_news/articles/2609230001" && r.Method == http.MethodDelete:
		_, _ = w.Write([]byte(`{"retired":true,"article":` + article + `}`))
	case strings.HasPrefix(p, "/channels/openpak_news/articles/2609230001"):
		_, _ = w.Write([]byte(article))
	case p == "/publish" || p == "/rollback":
		if f.failNext {
			f.failNext = false
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"channel openpak_news: body of 2609230001 is longer than 1200 bytes"}`))
			return
		}
		_, _ = w.Write([]byte(`{"build":8,"revision":13,"containers":118,"channels":4,"articles":34,"changed_topics":["openpak_news"],"push":"queued"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such route"}`))
	}
}

func TestConsoleNewsAdmin(t *testing.T) {
	fake := &fakeNewsAdmin{requests: map[string]map[string]any{}}
	svc := httptest.NewServer(fake)
	defer svc.Close()
	s, db, mux := adminMux(t)

	if body := get(mux, "/admin").Body.String(); strings.Contains(body, "/admin/console-news") {
		t.Fatal("console news in the nav while unset")
	}
	if rec := get(mux, "/admin/console-news"); rec.Code != http.StatusNotFound {
		t.Fatalf("unset: %d", rec.Code)
	}
	s.ConsoleNews = NewConsoleNewsAPI(svc.URL, "news-key")

	rec := get(mux, "/admin/console-news")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Serving build 7") || !strings.Contains(body, "unpublished edits") ||
		!strings.Contains(body, "nx-baas refused") || !strings.Contains(body, "OpenPak News") || !strings.Contains(body, `<span class="badge ok">serving</span>`) {
		t.Fatalf("dashboard: %d %s", rec.Code, body)
	}

	// Channels: create sends the topic; an edit sends every writable field.
	rec = post(mux, "/admin/console-news/channels", url.Values{"csrf": {"tok-sess-admin"}, "topic_id": {"openpak_news"}, "name": {"OpenPak News"},
		"publisher": {"OpenPak"}, "description": {"News."}, "icon_motif": {"mark"}, "application_ids": {""}})
	if rec.Code != http.StatusSeeOther || fake.requests["POST /admin/v1/channels"]["topic_id"] != "openpak_news" {
		t.Fatalf("create channel: %d %v", rec.Code, fake.requests["POST /admin/v1/channels"])
	}
	if _, sent := fake.requests["POST /admin/v1/channels"]["application_ids"]; sent {
		t.Fatal("create sent an empty application_ids")
	}
	post(mux, "/admin/console-news/channels/openpak_news", url.Values{"csrf": {"tok-sess-admin"}, "name": {"OpenPak News"}, "publisher": {"OpenPak"},
		"description": {"News."}, "icon_motif": {"mark-pad"}, "application_ids": {"0100ABCD12340000"}, "important": {"1"}})
	patch := fake.requests["PATCH /admin/v1/channels/openpak_news"]
	if patch["important"] != true || patch["icon"].(map[string]any)["motif"] != "mark-pad" || patch["application_ids"].([]any)[0] != "0100abcd12340000" {
		t.Fatalf("patch channel: %v", patch)
	}
	if body := get(mux, "/admin/console-news/channels/openpak_news").Body.String(); !strings.Contains(body, "Welcome") || !strings.Contains(body, "shipped") {
		t.Fatalf("channel page: %s", body)
	}

	// Articles: a new one sends only what was filled; newlines become spaces.
	rec = post(mux, "/admin/console-news/channels/openpak_news/articles", url.Values{"csrf": {"tok-sess-admin"}, "title": {"Welcome"},
		"body": {"One\r\nparagraph."}, "language": {"en-GB"}, "supported_languages": {"en-GB, en-US"}, "lifetime_days": {"14"},
		"priority": {"100"}, "art_motif": {"burst"}, "more_type": {"browser"}, "more_url": {"https://openpak.org/guide"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/console-news/channels/openpak_news/articles/2609230001?m=saved" {
		t.Fatalf("create article: %d %s", rec.Code, rec.Body.String())
	}
	created := fake.requests["POST /admin/v1/channels/openpak_news/articles"]
	if created["body"] != "One paragraph." || created["more"].(map[string]any)["url"] != "https://openpak.org/guide" {
		t.Fatalf("article body: %v", created)
	}
	for _, k := range []string{"news_id", "pickup_days", "published_at", "essential_pickup"} {
		if _, sent := created[k]; sent {
			t.Fatalf("create sent empty %s: %v", k, created)
		}
	}
	// An edit sends nulls for the emptied optional fields, so they reset.
	post(mux, "/admin/console-news/channels/openpak_news/articles/2609230001", url.Values{"csrf": {"tok-sess-admin"}, "title": {"Welcome"},
		"body": {"x"}, "language": {"en-GB"}, "supported_languages": {"en-GB"}, "lifetime_days": {"14"}, "priority": {"1000"},
		"published_at": {"2026-09-24T08:30"}, "essential_days": {"2"}, "art_motif": {"beacon"}, "more_type": {"game"}, "more_ids": {"0100ABCD12340000"}})
	edit := fake.requests["PATCH /admin/v1/channels/openpak_news/articles/2609230001"]
	if v, ok := edit["pickup_days"]; !ok || v != nil {
		t.Fatalf("pickup_days must be null: %v", edit)
	}
	if edit["published_at"] != "2026-09-24T08:30:00Z" || edit["essential_pickup"].(map[string]any)["pickup_days"] != float64(2) ||
		edit["more"].(map[string]any)["type"] != "game" || edit["priority"] != float64(1000) {
		t.Fatalf("edit: %v", edit)
	}
	if rec := post(mux, "/admin/console-news/channels/openpak_news/articles/2609230001", url.Values{"csrf": {"tok-sess-admin"}, "lifetime_days": {"two"}}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "lifetime days must be a whole number") {
		t.Fatalf("bad number: %d", rec.Code)
	}

	// An image goes up as the raw PUT body with its type.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", "tok-sess-admin")
	fw, _ := mw.CreateFormFile("image", "banner.jpg")
	_, _ = fw.Write([]byte("\xff\xd8\xff\xe0 jpeg"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/console-news/channels/openpak_news/articles/2609230001/image", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || fake.imageType != "image/jpeg" {
		t.Fatalf("image: %d %q", rec.Code, fake.imageType)
	}

	// Deleting a shipped article retires it; the page says so.
	rec = post(mux, "/admin/console-news/channels/openpak_news/articles/2609230001/delete", url.Values{"csrf": {"tok-sess-admin"}})
	if !strings.HasSuffix(rec.Header().Get("Location"), "m=retired-instead") {
		t.Fatalf("delete: %s", rec.Header().Get("Location"))
	}

	// Publish: a refusal shows why and that nothing switched; a success shows the build.
	fake.failNext = true
	rec = post(mux, "/admin/console-news/publish", url.Values{"csrf": {"tok-sess-admin"}, "note": {"typo"}})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "nothing was switched") || !strings.Contains(rec.Body.String(), "longer than 1200 bytes") {
		t.Fatalf("failed publish: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(mux, "/admin/console-news/publish", url.Values{"csrf": {"tok-sess-admin"}, "note": {"typo"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Now serving build 8") || fake.requests["POST /admin/v1/publish"]["note"] != "typo" {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(mux, "/admin/console-news/rollback", url.Values{"csrf": {"tok-sess-admin"}, "build": {"6"}}); rec.Code != http.StatusOK ||
		fake.requests["POST /admin/v1/rollback"]["build"] != float64(6) {
		t.Fatalf("rollback: %d", rec.Code)
	}
	if rec := post(mux, "/admin/console-news/publish", url.Values{}); rec.Code != http.StatusForbidden {
		t.Fatalf("publish without csrf: %d", rec.Code)
	}

	audit, _ := db.ListAudit(context.Background(), 20)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
	}
	want := "cnews.rollback,cnews.publish,cnews.article.delete,cnews.image.upload,cnews.article.update,cnews.article.create,cnews.channel.update,cnews.channel.create"
	if strings.Join(actions, ",") != want {
		t.Fatalf("audit: %v", actions)
	}
}
