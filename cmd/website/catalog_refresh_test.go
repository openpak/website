package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openpak/website/internal/store"
)

// A title added to the live catalog appears without a release; a status already set in the
// database is not overwritten by the catalog's word for it.
func TestRefreshCatalogReseeds(t *testing.T) {
	body := `[{"kind":"title","name":"Mario Kart 8","console":"Wii U","category":"Racing","backend":"NEX","status":"requestable","notes":""}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }))
	defer srv.Close()
	db := store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refreshCatalog(ctx, db, srv.URL, 30*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))

	waitStatus := func(want string) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			reqs, _ := db.ListRequests(ctx, "title", "")
			for _, r := range reqs {
				if r.Name == "Mario Kart 8" && r.Status == want {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Mario Kart 8 never became %q", want)
	}
	waitStatus("requestable")
	reqs, _ := db.ListRequests(ctx, "title", "")
	if err := db.SetRequestStatus(ctx, reqs[0].ID, "beta", "deployed"); err != nil {
		t.Fatal(err)
	}
	body = `[{"kind":"title","name":"Mario Kart 8","console":"Wii U","category":"Racing","backend":"NEX","status":"requestable","notes":""},` +
		`{"kind":"title","name":"Pikmin 3","console":"Wii U","category":"Strategy","backend":"NEX","status":"requestable","notes":""}]`
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reqs, _ = db.ListRequests(ctx, "title", "")
		if len(reqs) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(reqs) != 2 {
		t.Fatal("Pikmin 3 never appeared")
	}
	for _, r := range reqs {
		if r.Name == "Mario Kart 8" && r.Status != "beta" {
			t.Fatalf("catalog refresh overwrote the database status: %+v", r)
		}
	}
}
