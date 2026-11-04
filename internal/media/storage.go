package media

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is an unknown key.
var ErrNotFound = errors.New("media: object not found")

// PGStorage keeps objects in the media_objects table.
type PGStorage struct{ pool *pgxpool.Pool }

// NewPGStorage stores objects through pool (table made by the store migrations).
func NewPGStorage(pool *pgxpool.Pool) *PGStorage { return &PGStorage{pool: pool} }

// Put writes (or replaces) an object.
func (s *PGStorage) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO media_objects(key, data) VALUES($1,$2)
		ON CONFLICT (key) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, key, data)
	return err
}

// Get reads an object; a missing key is ErrNotFound.
func (s *PGStorage) Get(ctx context.Context, key string) ([]byte, error) {
	var b []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM media_objects WHERE key=$1`, key).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return b, err
}

// RemoveAll deletes every object under a key prefix (a path prefix: "a/b"
// matches "a/b/..." and "a/b" itself).
func (s *PGStorage) RemoveAll(ctx context.Context, prefix string) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return errors.New("media: refusing to remove the storage root")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM media_objects WHERE key=$1 OR left(key, $3)=$2`, prefix, prefix+"/", len(prefix)+1)
	return err
}

// ImportDir copies every file under dir into the table whose key is not yet
// there (key = slash path relative to dir). Files are never touched. A missing
// dir imports nothing. It returns how many were new and how many were present.
func (s *PGStorage) ImportDir(ctx context.Context, dir string) (imported, skipped int, err error) {
	if _, serr := os.Stat(dir); serr != nil {
		return 0, 0, nil
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || strings.HasSuffix(p, ".tmp") {
			return werr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		tag, rerr := s.pool.Exec(ctx, `INSERT INTO media_objects(key, data) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, filepath.ToSlash(rel), data)
		if rerr != nil {
			return rerr
		}
		if tag.RowsAffected() == 1 {
			imported++
		} else {
			skipped++
		}
		return nil
	})
	return imported, skipped, err
}

// MemStorage is an in-memory Storage for tests; it holds nothing durable.
type MemStorage struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemStorage returns an empty MemStorage.
func NewMemStorage() *MemStorage { return &MemStorage{m: map[string][]byte{}} }

// Put stores a copy.
func (s *MemStorage) Put(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = append([]byte(nil), data...)
	return nil
}

// Get reads an object.
func (s *MemStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return b, nil
}

// RemoveAll deletes every object under a prefix.
func (s *MemStorage) RemoveAll(_ context.Context, prefix string) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return errors.New("media: refusing to remove the storage root")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if k == prefix || strings.HasPrefix(k, prefix+"/") {
			delete(s.m, k)
		}
	}
	return nil
}
