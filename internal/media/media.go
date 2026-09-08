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
	"image/jpeg"
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
	// SizeConsole is the JPEG the Switch adapter serves at /1/<id> and
	// /2/<id> (CR-2). Both paths take the same bytes, which the console
	// accepts; the exact dimensions it prefers are still to be measured, so
	// this tracks SizeSmall until a device measurement says otherwise.
	SizeConsole = SizeSmall
	// ConsoleJPEGQuality trades a little fidelity for a small body: the
	// console refetches this whenever the version changes.
	ConsoleJPEGQuality = 85
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

// ConsoleKey is the storage key for the JPEG the console path consumes.
func ConsoleKey(accountID string) string {
	return fmt.Sprintf("avatars/%s/console.jpg", accountID)
}

// putDerivatives writes every derivative of one decoded image: the two PNGs the
// website serves and the JPEG the console path needs.
func (s *Service) putDerivatives(ctx context.Context, accountID string, img image.Image) error {
	for _, size := range []int{SizeLarge, SizeSmall} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, square(img, size)); err != nil {
			return err
		}
		if err := s.store.Put(ctx, AvatarKey(accountID, size), buf.Bytes()); err != nil {
			return err
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, square(img, SizeConsole), &jpeg.Options{Quality: ConsoleJPEGQuality}); err != nil {
		return err
	}
	return s.store.Put(ctx, ConsoleKey(accountID), jb.Bytes())
}

// StorePreset stores one of the built-in avatars for an account.
func (s *Service) StorePreset(ctx context.Context, accountID, presetID string) error {
	if !validAccountID(accountID) || !IsPreset(presetID) {
		return ErrInvalid
	}
	return s.putDerivatives(ctx, accountID, drawPreset(presetID, SizeLarge))
}

// PresetImage renders a preset for the picker; no account needed.
func PresetImage(presetID string, size int) ([]byte, error) {
	if !IsPreset(presetID) {
		return nil, ErrInvalid
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, drawPreset(presetID, size)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ConsoleAvatar returns the JPEG bytes for the console path.
func (s *Service) ConsoleAvatar(ctx context.Context, accountID string) ([]byte, error) {
	if b, err := s.store.Get(ctx, ConsoleKey(accountID)); err == nil {
		return b, nil
	}
	// No console upload: give back whatever the account does have, as JPEG.
	// A Switch asks for one image format and the site keeps another, and that
	// difference is not the console's problem.
	png, err := s.Avatar(ctx, accountID, SizeSmall)
	if err != nil {
		return jpegOf(drawPreset(PresetForAccount(accountID), SizeSmall))
	}
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return nil, ErrInvalid
	}
	return jpegOf(img)
}

func jpegOf(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func validAccountID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "/\\") && !strings.Contains(id, "..")
}

// StoreAvatar validates, resizes to 256 and 512 px and stores both PNGs.
func (s *Service) StoreAvatar(ctx context.Context, accountID string, r io.Reader) error {
	if !validAccountID(accountID) {
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
	return s.putDerivatives(ctx, accountID, img)
}

// Avatar returns stored avatar bytes.
func (s *Service) Avatar(ctx context.Context, accountID string, size int) ([]byte, error) {
	if size != SizeSmall && size != SizeLarge {
		return nil, ErrInvalid
	}
	return s.store.Get(ctx, AvatarKey(accountID, size))
}
