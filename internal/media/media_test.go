package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestStoreAvatarResizes(t *testing.T) {
	st, err := NewFSStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	ctx := context.Background()

	if err := svc.StoreAvatar(ctx, "acct-1", bytes.NewReader(pngBytes(1024))); err != nil {
		t.Fatalf("store: %v", err)
	}
	for _, size := range []int{SizeSmall, SizeLarge} {
		data, err := svc.Avatar(ctx, "acct-1", size)
		if err != nil {
			t.Fatalf("avatar %d: %v", size, err)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("size %d: got %dx%d", size, b.Dx(), b.Dy())
		}
	}
}

func TestStoreAvatarRejectsGarbage(t *testing.T) {
	st, _ := NewFSStorage(t.TempDir())
	svc := New(st)
	if err := svc.StoreAvatar(context.Background(), "acct-1", strings.NewReader("not an image")); err == nil {
		t.Fatal("garbage upload must fail")
	}
	// Oversize body: a 5 MiB random blob is not an image AND exceeds cap.
	if err := svc.StoreAvatar(context.Background(), "acct-1", bytes.NewReader(make([]byte, MaxUploadBytes+2))); err == nil {
		t.Fatal("oversize upload must fail")
	}
	// Path traversal in account id.
	if err := svc.StoreAvatar(context.Background(), "../evil", bytes.NewReader(pngBytes(64))); err == nil {
		t.Fatal("traversal account id must fail")
	}
}

// TestPresetsProduceConsoleJPEG covers the path the console actually reads:
// picking a built-in avatar must leave a decodable JPEG behind, because the
// adapter serves those bytes at /1/<id> and /2/<id> (CR-2).
func TestPresetsProduceConsoleJPEG(t *testing.T) {
	st, err := NewFSStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	for _, id := range PresetIDs {
		if err := svc.StorePreset(context.Background(), "acct-1", id); err != nil {
			t.Fatalf("preset %s: %v", id, err)
		}
		raw, err := svc.ConsoleAvatar(context.Background(), "acct-1")
		if err != nil {
			t.Fatalf("console avatar after %s: %v", id, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "jpeg" {
			t.Fatalf("preset %s: format=%q err=%v", id, format, err)
		}
		if cfg.Width != SizeConsole || cfg.Height != SizeConsole {
			t.Fatalf("preset %s: %dx%d, want %d square", id, cfg.Width, cfg.Height, SizeConsole)
		}
	}
	if err := svc.StorePreset(context.Background(), "acct-1", "not-a-preset"); err == nil {
		t.Fatal("unknown preset must be rejected")
	}
}

type fakePublisher struct {
	account      string
	small, large []byte
	err          error
}

func (f *fakePublisher) PutAvatar(_ context.Context, accountID string, png256, png512 []byte) error {
	f.account, f.small, f.large = accountID, png256, png512
	return f.err
}

// Every stored avatar goes to the core too (CR-2), with the sizes the core
// checks; a core that refuses it does not fail the upload.
func TestStoreAvatarPublishesToCore(t *testing.T) {
	st, _ := NewFSStorage(t.TempDir())
	svc := New(st)
	pub := &fakePublisher{}
	svc.Publisher = pub
	ctx := context.Background()
	if err := svc.StoreAvatar(ctx, "acct-1", bytes.NewReader(pngBytes(600))); err != nil {
		t.Fatal(err)
	}
	if pub.account != "acct-1" {
		t.Fatalf("published for %q", pub.account)
	}
	for size, b := range map[int][]byte{SizeSmall: pub.small, SizeLarge: pub.large} {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("published %d: err=%v", size, err)
		}
	}
	pub.err = context.DeadlineExceeded
	if err := svc.StorePreset(ctx, "acct-1", PresetIDs[0]); err != nil {
		t.Fatalf("a core failure must not fail the store: %v", err)
	}
}
