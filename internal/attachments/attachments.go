// Package attachments keeps uploaded files (help-ticket attachments, crash
// dumps) in Postgres (attachment_files). Uploads are size-limited by the
// callers (10 and 20 MiB), so a file is held in memory at most once.
package attachments

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTooLarge is a file over the caller's limit; nothing is kept.
var ErrTooLarge = errors.New("attachments: file too large")

// ErrNotFound is an unknown or malformed key.
var ErrNotFound = errors.New("attachments: not found")

var keyRE = regexp.MustCompile(`^(tickets|crashes)/[0-9a-f]{2}/[0-9a-f]{32}$`)

// backend holds the bytes: Postgres in production, memory in tests.
type backend interface {
	put(ctx context.Context, key string, data []byte) error
	get(ctx context.Context, key string) ([]byte, error)
	del(ctx context.Context, key string) error
}

// Store is the attachment store.
type Store struct{ b backend }

// NewPG keeps attachments in the attachment_files table (made by the store
// migrations). A nil pool disables attachments (nil Store).
func NewPG(pool *pgxpool.Pool) *Store {
	if pool == nil {
		return nil
	}
	return &Store{b: pgBackend{pool}}
}

// NewMemory is a non-durable Store for tests.
func NewMemory() *Store { return &Store{b: &memBackend{m: map[string][]byte{}}} }

// Saved describes a stored file.
type Saved struct {
	Key         string
	Size        int64
	ContentType string // sniffed from the first bytes, for display only
}

// Save reads r into a new file under kind ("tickets" or "crashes"). A body
// longer than max is refused and nothing is kept. At most max+1 bytes are
// ever held in memory (the callers' limits are 10 and 20 MiB).
func (s *Store) Save(kind string, r io.Reader, max int64) (Saved, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Saved{}, err
	}
	name := hex.EncodeToString(b)
	key := kind + "/" + name[:2] + "/" + name
	if !keyRE.MatchString(key) {
		return Saved{}, errors.New("attachments: unknown kind")
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return Saved{}, err
	}
	if int64(len(data)) > max {
		return Saved{}, ErrTooLarge
	}
	if err := s.b.put(context.Background(), key, data); err != nil {
		return Saved{}, err
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	return Saved{Key: key, Size: int64(len(data)), ContentType: http.DetectContentType(head)}, nil
}

// Open returns the file for a key.
func (s *Store) Open(key string) (io.ReadCloser, error) {
	data, err := s.read(key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *Store) read(key string) ([]byte, error) {
	if !keyRE.MatchString(key) {
		return nil, ErrNotFound
	}
	return s.b.get(context.Background(), key)
}

// Remove deletes a stored file; unknown keys are ignored.
func (s *Store) Remove(key string) { _ = s.Delete(key) }

// Delete removes a stored file and says when it could not: a file already
// gone, or a key that is not one of ours, is not an error (account erasure
// retries until every file it found is gone).
func (s *Store) Delete(key string) error {
	if !keyRE.MatchString(key) {
		return nil
	}
	return s.b.del(context.Background(), key)
}

type pgBackend struct{ pool *pgxpool.Pool }

func (p pgBackend) put(ctx context.Context, key string, data []byte) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO attachment_files(key, data) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, key, data)
	return err
}

func (p pgBackend) get(ctx context.Context, key string) ([]byte, error) {
	var b []byte
	err := p.pool.QueryRow(ctx, `SELECT data FROM attachment_files WHERE key=$1`, key).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (p pgBackend) del(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM attachment_files WHERE key=$1`, key)
	return err
}

type memBackend struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (m *memBackend) put(_ context.Context, k string, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[k] = d
	return nil
}

func (m *memBackend) get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.m[k]
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

func (m *memBackend) del(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, k)
	return nil
}

// ImportDir copies every file under dir (layout tickets/ab/<32hex>) whose key
// is not yet stored into Postgres. Files are never removed. A missing dir
// imports nothing; files that are not attachments are skipped.
func ImportDir(ctx context.Context, pool *pgxpool.Pool, dir string) (imported, skipped int, err error) {
	if _, serr := os.Stat(dir); serr != nil {
		return 0, 0, nil
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		key := filepath.ToSlash(rel)
		if !keyRE.MatchString(key) {
			skipped++
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		tag, rerr := pool.Exec(ctx, `INSERT INTO attachment_files(key, data) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, key, data)
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

// Serve writes a stored file as a download. It is always served as an opaque
// attachment, whatever it claims to be, so an uploaded HTML file can never
// run in the site's origin.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, key, name string) {
	if s == nil {
		http.NotFound(w, r)
		return
	}
	data, err := s.read(key)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": CleanName(name)}))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

// CleanName is a filename safe to show and to put in a header: no path, no
// control characters, at most 120 bytes.
func CleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, name)
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}
