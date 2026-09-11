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

// A status change in the live catalog reaches the title list without a release.
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
	body = `[{"kind":"title","name":"Mario Kart 8","console":"Wii U","category":"Racing","backend":"NEX","status":"beta","notes":"deployed"}]`
	waitStatus("beta")
}
