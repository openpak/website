package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestPGStorage(t *testing.T) {
	dsn := os.Getenv("WEBSITE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEBSITE_TEST_DATABASE_URL not set; Postgres tests skipped")
	}
	ctx := context.Background()
	db, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Pool().Exec(ctx, `TRUNCATE media_objects`); err != nil {
		t.Fatal(err)
	}
	s := NewPGStorage(db.Pool())
	if err := New(s).Healthy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "avatars/a1/console.jpg", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "avatars/a1/console.jpg", []byte("two")); err != nil {
		t.Fatal(err)
	}
	_ = s.Put(ctx, "avatars/a10/console.jpg", []byte("neighbour"))
	// Restart: a new storage over the same database.
	if b, err := NewPGStorage(db.Pool()).Get(ctx, "avatars/a1/console.jpg"); err != nil || string(b) != "two" {
		t.Fatalf("after restart: %q %v", b, err)
	}
	if err := s.RemoveAll(ctx, "avatars/a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "avatars/a1/console.jpg"); err == nil {
		t.Fatal("not erased")
	}
	if b, err := s.Get(ctx, "avatars/a10/console.jpg"); err != nil || string(b) != "neighbour" {
		t.Fatalf("neighbour: %q %v", b, err)
	}

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "avatars", "b2"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "avatars", "b2", "console.jpg"), []byte("file"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "avatars", "b2", "x.png.tmp"), []byte("half"), 0o644)
	if n, _, err := s.ImportDir(ctx, dir); err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	if n, k, err := s.ImportDir(ctx, dir); err != nil || n != 0 || k != 1 {
		t.Fatalf("again: %d %d %v", n, k, err)
	}
	if b, _ := s.Get(ctx, "avatars/b2/console.jpg"); string(b) != "file" {
		t.Fatalf("imported %q", b)
	}
}
