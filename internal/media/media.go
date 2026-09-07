// Package media implements the avatar pipeline (WEB-03): validate PNG/JPEG
// uploads, resize server-side to 256 and 512 px, store bytes behind a
// filesystem-first object-storage interface, and record metadata in the
// website database. Publishing avatars to the account core (so adapters can
// serve them to consoles) is pending contract request CR-2; until then the
// website serves avatars itself from /media/.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

// Max sizes and pixel constraints.
const (
	MaxUploadBytes = 4 << 20 // 4 MiB
	SizeLarge      = 512
	SizeSmall      = 256
)

// ErrInvalid is returned for rejected uploads.
var ErrInvalid = errors.New("media: not a valid PNG or JPEG image")

// Storage is the object-storage interface (filesystem first; an
// S3-compatible implementation can slot in without touching handlers).
type Storage interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// FSStorage stores objects under a root directory.
type FSStorage struct{ root string }

// NewFSStorage creates the root if needed.
func NewFSStorage(root string) (*FSStorage, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FSStorage{root: root}, nil
}

func (s *FSStorage) path(key string) (string, error) {
	clean := filepath.Clean("/" + key) // force under root
	return filepath.Join(s.root, clean), nil
}

// Put writes an object atomically.
func (s *FSStorage) Put(_ context.Context, key string, data []byte) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Get reads an object.
func (s *FSStorage) Get(_ context.Context, key string) ([]byte, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Service processes and stores avatars.
type Service struct {
	store Storage
}

// New builds the service over a Storage.
func New(s Storage) *Service { return &Service{store: s} }

// Healthy reports whether the storage backend accepts writes.
func (s *Service) Healthy(ctx context.Context) error {
	probe := "health/probe"
	if err := s.store.Put(ctx, probe, []byte("ok")); err != nil {
		return err
	}
	_, err := s.store.Get(ctx, probe)
	return err
}

// square crops to the center square and scales to size.
func square(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	side := w
	if h < w {
		side = h
	}
	// Center the crop.
	minX := b.Min.X + (w-side)/2
	minY := b.Min.Y + (h-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(),
		img, image.Rect(minX, minY, minX+side, minY+side), draw.Over, nil)
	return dst
}

// AvatarKey is the storage key for one avatar size.
func AvatarKey(accountID string, size int) string {
	return fmt.Sprintf("avatars/%s/avatar_%d.png", accountID, size)
}

// StoreAvatar validates, resizes to 256 and 512 px and stores both PNGs.
func (s *Service) StoreAvatar(ctx context.Context, accountID string, r io.Reader) error {
	if strings.ContainsAny(accountID, "/\\") || accountID == "" ||
		strings.Contains(accountID, "..") {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return ErrInvalid
	}
	if len(raw) > MaxUploadBytes {
		return ErrInvalid
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return ErrInvalid
	}
	for _, size := range []int{SizeLarge, SizeSmall} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, square(img, size)); err != nil {
			return err
		}
		if err := s.store.Put(ctx, AvatarKey(accountID, size), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// Avatar returns stored avatar bytes.
func (s *Service) Avatar(ctx context.Context, accountID string, size int) ([]byte, error) {
	if size != SizeSmall && size != SizeLarge {
		return nil, ErrInvalid
	}
	return s.store.Get(ctx, AvatarKey(accountID, size))
}
