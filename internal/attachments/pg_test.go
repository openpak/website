package attachments

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openpak/website/internal/store"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("WEBSITE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEBSITE_TEST_DATABASE_URL not set; Postgres tests skipped")
	}
	db, err := store.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Pool().Exec(context.Background(), `TRUNCATE attachment_files`); err != nil {
		t.Fatal(err)
	}
	return db.Pool()
}

func TestPGRoundTripAndRestart(t *testing.T) {
	pool := testPool(t)
	s := NewPG(pool)
	got, err := s.Save("crashes", strings.NewReader("crash dump"), 100)
	if err != nil || got.Size != 10 {
		t.Fatalf("save: %+v %v", got, err)
	}
	if _, err := s.Save("tickets", strings.NewReader(strings.Repeat("x", 101)), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	// A new Store over the same database (a restart) sees the file.
	f, err := NewPG(pool).Open(got.Key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	if string(b) != "crash dump" {
		t.Fatalf("after restart %q", b)
	}
	if err := s.Delete(got.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(got.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestImportDirIdempotent(t *testing.T) {
	pool := testPool(t)
	dir := t.TempDir()
	key := "tickets/ab/" + strings.Repeat("ab", 16)
	if err := os.MkdirAll(filepath.Join(dir, "tickets", "ab"), 0o750); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.WriteFile(p, []byte("old file"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, _, err := ImportDir(ctx, pool, dir); err != nil || n != 1 {
		t.Fatalf("first import: %d %v", n, err)
	}
	if n, k, err := ImportDir(ctx, pool, dir); err != nil || n != 0 || k != 2 {
		t.Fatalf("second import: %d %d %v", n, k, err)
	}
	f, err := NewPG(pool).Open(key)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(f); string(b) != "old file" {
		t.Fatalf("imported %q", b)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("the old file must stay")
	}
	if n, k, err := ImportDir(ctx, pool, filepath.Join(dir, "missing")); n+k != 0 || err != nil {
		t.Fatalf("missing dir: %d %d %v", n, k, err)
	}
}
