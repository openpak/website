package pages

import (
	"context"
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
