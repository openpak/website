package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Blobs is the private artifact storage: content-addressed, immutable
// (POL-003). Digest knowledge grants no access — authorization happens in
// the API layer; this interface only moves bytes.
type Blobs interface {
	// Put stores bytes under their SHA-256 and rejects a digest that does
	// not match, or an existing entry whose bytes differ (a corruption or
	// hash-collision guard; the bytes are never overwritten).
	Put(ctx context.Context, data []byte) (string, error)
	Get(ctx context.Context, sha256Hex string) ([]byte, error)
	Has(ctx context.Context, sha256Hex string) (bool, error)
}

// DirBlobs keeps blobs under a filesystem root, sharded by the first two
// digest bytes. Writes go to a temp file and rename, so a crash leaves
// either the old blob or the complete new one — never a partial artifact
// (POL-008: a partially uploaded artifact cannot be promoted).
type DirBlobs struct {
	root string
}

// NewDirBlobs creates the root if needed.
func NewDirBlobs(root string) (*DirBlobs, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("registry: blob root: %w", err)
	}
	return &DirBlobs{root: root}, nil
}

func blobPath(root, sha256Hex string) (string, error) {
	if !isSHA256Hex(sha256Hex) {
		return "", fmt.Errorf("registry: %q is not a sha-256 hex digest", sha256Hex)
	}
	return filepath.Join(root, sha256Hex[:2], sha256Hex), nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (d *DirBlobs) Put(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hex_ := hex.EncodeToString(sum[:])
	path, err := blobPath(d.root, hex_)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		// Immutable: an existing entry must already be these bytes. Verify
		// rather than trust — a truncated write from a past crash must not
		// silently pass as the content it claims.
		old, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !bytesEqual(old, data) {
			return "", fmt.Errorf("registry: blob %s exists with different bytes", hex_)
		}
		return hex_, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".blob-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return hex_, nil
}

func (d *DirBlobs) Get(_ context.Context, sha256Hex string) ([]byte, error) {
	path, err := blobPath(d.root, sha256Hex)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sha256Hex {
		return nil, fmt.Errorf("registry: blob %s holds bytes that hash to %s", sha256Hex, got)
	}
	return data, nil
}

func (d *DirBlobs) Has(_ context.Context, sha256Hex string) (bool, error) {
	path, err := blobPath(d.root, sha256Hex)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }
