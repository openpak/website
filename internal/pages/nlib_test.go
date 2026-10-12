package pages

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSwitchLocalIcon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0100A7C01B792000.jpg"), []byte("published game icon"), 0o644); err != nil {
		t.Fatal(err)
	}
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	oldDir, oldBase := switchIconDir, nlibBase
	switchIconDir, nlibBase = dir, upstream.URL+"/"
	defer func() { switchIconDir, nlibBase = oldDir, oldBase }()
	for _, id := range []string{"0100a7c01b792000", "0100A7C01B792800"} {
		req := httptest.NewRequest(http.MethodGet, "/titles/icon/"+id, nil)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		(&Server{}).handleSwitchIcon(w, req)
		if w.Code != http.StatusOK || w.Body.String() != "published game icon" || w.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("local icon for %s: status %d, headers %v, body %q", id, w.Code, w.Header(), w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "public, max-age=2592000" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing icon cache/security headers: %v", w.Header())
		}
	}
	if hits != 0 {
		t.Fatalf("local icon fetched upstream %d times", hits)
	}
	req := httptest.NewRequest(http.MethodGet, "/titles/icon/0100000000000000", nil)
	req.SetPathValue("id", "0100000000000000")
	w := httptest.NewRecorder()
	(&Server{}).handleSwitchIcon(w, req)
	if w.Code != http.StatusNotFound || hits != 1 {
		t.Fatalf("uncached icon: status %d, upstream hits %d", w.Code, hits)
	}
}

func TestSwitchTitleName(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/01000CA004DCA000" {
			w.Write([]byte(`{"name":"Human: Fall Flat"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := nlibBase
	nlibBase = srv.URL + "/"
	defer func() { nlibBase = old }()
	ctx := context.Background()
	if n := switchTitleName(ctx, "01000ca004dca000"); n != "Human: Fall Flat" {
		t.Fatalf("name = %q", n)
	}
	switchTitleName(ctx, "01000CA004DCA000")
	if n := switchTitleName(ctx, "0100000000000001"); n != "" {
		t.Fatalf("miss = %q", n)
	}
	switchTitleName(ctx, "0100000000000001")
	if n := switchTitleName(ctx, "../../etc"); n != "" || hits != 2 {
		t.Fatalf("bad id = %q, hits = %d (want 2: both cached, bad id never sent)", n, hits)
	}
}

func TestSwitchUpdateMetadata(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/0100AAA00ACBE000":
			w.Write([]byte(`{"name":"Risk of Rain"}`))
		case "/0100AAA00ACBE000/icon/256/256":
			w.Header().Set("Content-Type", "image/jpeg")
			io.WriteString(w, "game icon")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := nlibBase
	nlibBase = srv.URL + "/"
	defer func() {
		nlibBase = old
		nlibNames.Delete("0100AAA00ACBE000")
	}()
	nlibNames.Delete("0100AAA00ACBE000")
	for _, id := range []string{"0100aaa00acbe800", "0100AAA00ACBE000"} {
		if name := switchTitleName(context.Background(), id); name != "Risk of Rain" {
			t.Fatalf("name for %s = %q", id, name)
		}
		req := httptest.NewRequest(http.MethodGet, "/titles/icon/"+id, nil)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		(&Server{}).handleSwitchIcon(w, req)
		if w.Code != http.StatusOK || w.Body.String() != "game icon" || w.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("icon for %s: status %d, headers %v, body %q", id, w.Code, w.Header(), w.Body.String())
		}
	}
	if len(paths) != 3 {
		t.Fatalf("requests = %v, want one cached name lookup and two icons", paths)
	}
}
