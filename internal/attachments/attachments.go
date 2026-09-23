// Package attachments keeps uploaded files (help-ticket attachments, crash
// dumps) on disk under WEBSITE_ATTACHMENTS_DIR. The database holds only the
// key; files are streamed in and out, never held in memory whole.
package attachments

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrTooLarge is a file over the caller's limit; nothing is kept.
var ErrTooLarge = errors.New("attachments: file too large")

// ErrNotFound is an unknown or malformed key.
var ErrNotFound = errors.New("attachments: not found")

var keyRE = regexp.MustCompile(`^(tickets|crashes)/[0-9a-f]{2}/[0-9a-f]{32}$`)

// Store is a directory of attachments.
type Store struct{ dir string }

// New creates the directory if needed. An empty dir disables attachments (nil).
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Saved describes a stored file.
type Saved struct {
	Key         string
	Size        int64
	ContentType string // sniffed from the first bytes, for display only
}

// Save streams r to a new file under kind ("tickets" or "crashes"). A body
// longer than max is refused and removed.
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
	path := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Saved{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return Saved{}, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)
	head = head[:n]
	written, err := io.Copy(f, io.LimitReader(io.MultiReader(strings.NewReader(string(head)), r), max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && written > max {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(path)
		return Saved{}, err
	}
	return Saved{Key: key, Size: written, ContentType: http.DetectContentType(head)}, nil
}

// Open returns the file for a key.
func (s *Store) Open(key string) (*os.File, error) {
	if !keyRE.MatchString(key) {
		return nil, ErrNotFound
	}
	f, err := os.Open(filepath.Join(s.dir, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Remove deletes a stored file; unknown keys are ignored.
func (s *Store) Remove(key string) {
	if keyRE.MatchString(key) {
		_ = os.Remove(filepath.Join(s.dir, filepath.FromSlash(key)))
	}
}

// Serve writes a stored file as a download. It is always served as an opaque
// attachment, whatever it claims to be, so an uploaded HTML file can never
// run in the site's origin.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, key, name string) {
	if s == nil {
		http.NotFound(w, r)
		return
	}
	f, err := s.Open(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": CleanName(name)}))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, f)
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
