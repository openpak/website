package pages

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
