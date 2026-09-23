package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// fakeStore is nx-baas's store admin API, as much of it as the page uses.
type fakeStore struct {
	mu     sync.Mutex
	titles map[string]map[string]any
	calls  []string
	upload struct {
		meta  string
		parts []string
		sum   [32]byte
		size  int
	}
	iconCT string
}

func (f *fakeStore) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Internal-Key") != "store-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","message":"wrong key"}`))
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		p := strings.TrimPrefix(r.URL.Path, "/internal/store/")
		switch {
		case r.Method == http.MethodGet && p == "titles":
			var list []map[string]any
			for _, t := range f.titles {
				list = append(list, t)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(list), "titles": list})
		case r.Method == http.MethodGet && p == "stats":
			_, _ = w.Write([]byte(`{"titles":[{"title_id":"0100abcd12340000","downloads":12,"content_requests":80,"bytes_served":1234,"entitlements":5,"tasks":{"completed":4}}]}`))
		case r.Method == http.MethodPost && p == "titles":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			id, _ := body["title_id"].(string)
			if _, dup := f.titles[id]; dup {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"already_exists","message":"title exists"}`))
				return
			}
			body["published"] = false
			f.titles[id] = body
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(body)
		default:
			parts := strings.Split(strings.TrimPrefix(p, "titles/"), "/")
			t, ok := f.titles[parts[0]]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not_found","message":"no such title"}`))
				return
			}
			switch {
			case len(parts) == 1 && r.Method == http.MethodGet:
				_ = json.NewEncoder(w).Encode(t)
			case len(parts) == 1 && r.Method == http.MethodPatch:
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				for k, v := range body {
					t[k] = v
				}
				_ = json.NewEncoder(w).Encode(t)
			case len(parts) == 1 && r.Method == http.MethodDelete:
				delete(f.titles, parts[0])
				w.WriteHeader(http.StatusNoContent)
			case parts[1] == "stats":
				_, _ = w.Write([]byte(`{"title_id":"0100abcd12340000","downloads":12,"content_requests":80,"bytes_served":1234,"entitlements":5,"tasks":{"completed":4}}`))
			case parts[1] == "publish":
				t["published"] = true
				_ = json.NewEncoder(w).Encode(t)
			case parts[1] == "icon":
				f.iconCT = r.Header.Get("Content-Type")
				_ = json.NewEncoder(w).Encode(t)
			case parts[1] == "versions":
				mr, err := r.MultipartReader()
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					f.upload.parts = append(f.upload.parts, part.FormName())
					if part.FormName() == "metadata" {
						b, _ := io.ReadAll(part)
						f.upload.meta = string(b)
					} else {
						h := sha256.New()
						n, _ := io.Copy(h, part)
						copy(f.upload.sum[:], h.Sum(nil))
						f.upload.size = int(n)
					}
				}
				t["application"] = map[string]any{"version": 0, "version_label": "1.0.0", "content": []any{}}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(t)
			}
		}
	}
}

func TestStoreAdmin(t *testing.T) {
	fs := &fakeStore{titles: map[string]map[string]any{}}
	svc := httptest.NewServer(fs.handler())
	defer svc.Close()
	s, db, mux := adminMux(t)

	// Unset: no nav entry, and the pages do not exist.
	if body := get(mux, "/admin").Body.String(); strings.Contains(body, `href="/admin/store"`) {
		t.Fatal("store in the nav while unset")
	}
	if rec := get(mux, "/admin/store"); rec.Code != http.StatusNotFound {
		t.Fatalf("unset store page: %d", rec.Code)
	}
	s.Store = NewStoreAPI(svc.URL, "store-key")
	if body := get(mux, "/admin").Body.String(); !strings.Contains(body, `href="/admin/store"`) {
		t.Fatal("store missing from the nav")
	}

	// Create, then a duplicate shows the service's words.
	form := url.Values{"csrf": {"tok-sess-admin"}, "title_id": {"0100ABCD12340000"}, "lang": {"en-US"}, "name": {"Tetris Clone"},
		"description": {"Blocks."}, "publisher": {"Someone"}, "languages": {"en-US, de-DE"}, "compatibility": {"handheld"},
		"screenshots": {"https://x.test/1.png\nhttps://x.test/2.png"}, "source": {"https://github.com/x/y"}, "permission": {"GPL-3.0"}}
	if rec := post(mux, "/admin/store", form); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/store/0100abcd12340000?m=created" {
		t.Fatalf("create: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	created := fs.titles["0100abcd12340000"]
	if created["publisher"] != "Someone" || len(created["screenshots"].([]any)) != 2 {
		t.Fatalf("created body: %+v", created)
	}
	if rec := post(mux, "/admin/store", form); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "title exists (already_exists)") {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	// Editing one language keeps the others.
	fs.titles["0100abcd12340000"]["localized"] = map[string]any{"en-US": map[string]any{"name": "Tetris Clone"}, "de-DE": map[string]any{"name": "Tetris-Klon"}}
	form.Set("name", "Tetris Clone 2")
	if rec := post(mux, "/admin/store/0100abcd12340000", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	loc := fs.titles["0100abcd12340000"]["localized"].(map[string]any)
	if loc["de-DE"].(map[string]any)["name"] != "Tetris-Klon" || loc["en-US"].(map[string]any)["name"] != "Tetris Clone 2" {
		t.Fatalf("localized: %+v", loc)
	}

	// List and detail show the stats.
	if body := get(mux, "/admin/store").Body.String(); !strings.Contains(body, "Tetris Clone 2") || !strings.Contains(body, "12 downloads") {
		t.Fatalf("list: %s", body)
	}
	if body := get(mux, "/admin/store/0100abcd12340000").Body.String(); !strings.Contains(body, "80 content requests") {
		t.Fatalf("detail: %s", body)
	}

	// A version upload is relayed metadata first, the file byte for byte.
	nsp := bytes.Repeat([]byte("PFS0-and-more"), 200000) // ~2.6 MB
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, kv := range [][2]string{{"csrf", "tok-sess-admin"}, {"kind", "base"}, {"version", "0"}, {"version_label", "1.0.0"},
		{"contents", strings.Repeat("a", 32) + " meta 3584\n" + strings.Repeat("b", 32) + " program 4096000 " + strings.Repeat("c", 64)}} {
		_ = mw.WriteField(kv[0], kv[1])
	}
	fw, _ := mw.CreateFormFile("nsp", "tetris.nsp")
	_, _ = fw.Write(nsp)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/store/0100abcd12340000/versions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Join(fs.upload.parts, ",") != "metadata,nsp" || fs.upload.sum != sha256.Sum256(nsp) || fs.upload.size != len(nsp) {
		t.Fatalf("relayed: parts %v size %d", fs.upload.parts, fs.upload.size)
	}
	var meta struct {
		Kind     string
		Contents []storeContent
	}
	_ = json.Unmarshal([]byte(fs.upload.meta), &meta)
	if meta.Kind != "base" || len(meta.Contents) != 2 || meta.Contents[1].Size != 4096000 || meta.Contents[1].SHA256 != strings.Repeat("c", 64) {
		t.Fatalf("metadata: %s", fs.upload.meta)
	}

	// A file ahead of the token is refused before anything is relayed.
	calls := len(fs.calls)
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("nsp", "x.nsp")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("csrf", "tok-sess-admin")
	_ = mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/admin/store/0100abcd12340000/versions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(fs.calls) != calls {
		t.Fatalf("file before csrf: %d, calls %v", rec.Code, fs.calls[calls:])
	}

	// Icon, publish, and a delete that needs its tick.
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", "tok-sess-admin")
	fw, _ = mw.CreateFormFile("icon", "icon.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\n rest"))
	_ = mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/admin/store/0100abcd12340000/icon", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || fs.iconCT != "image/png" {
		t.Fatalf("icon: %d %q", rec.Code, fs.iconCT)
	}
	if rec := post(mux, "/admin/store/0100abcd12340000/publish", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusSeeOther || fs.titles["0100abcd12340000"]["published"] != true {
		t.Fatalf("publish: %d", rec.Code)
	}
	if rec := post(mux, "/admin/store/0100abcd12340000/delete", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusBadRequest || fs.titles["0100abcd12340000"] == nil {
		t.Fatalf("delete without the tick: %d", rec.Code)
	}
	if rec := post(mux, "/admin/store/0100abcd12340000/delete", url.Values{"csrf": {"tok-sess-admin"}, "confirm": {"1"}}); rec.Code != http.StatusSeeOther || fs.titles["0100abcd12340000"] != nil {
		t.Fatalf("delete: %d", rec.Code)
	}
	audit, _ := db.ListAudit(context.Background(), 20)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
	}
	if strings.Join(actions, ",") != "store.delete,store.publish,store.icon,store.upload,store.update,store.create" {
		t.Fatalf("audit: %v", actions)
	}
}

func TestParseContents(t *testing.T) {
	id := strings.Repeat("A", 32)
	got, err := parseContents(id + " META 10\n\n" + strings.Repeat("b", 32) + ", control")
	if err != nil || len(got) != 2 || got[0].ContentID != strings.Repeat("a", 32) || got[0].ContentType != "meta" || got[0].Size != 10 {
		t.Fatalf("lines: %+v %v", got, err)
	}
	if got, err := parseContents(`[{"content_id":"` + id + `","content_type":"meta"}]`); err != nil || len(got) != 1 {
		t.Fatalf("json: %+v %v", got, err)
	}
	for _, bad := range []string{"", "nothex meta", id + " meta nonsense"} {
		if _, err := parseContents(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
