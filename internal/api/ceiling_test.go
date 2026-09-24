package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

// The ceiling endpoint serves the owner-signed file byte for byte, with an
// ETag, and never serves a file that does not verify (docs/signed-ceiling.md).
func TestNetworkCeiling(t *testing.T) {
	s, _, _ := newTestAPI(t)
	get := func(inm string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		s.RegisterRoutes(mux)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/network/ceiling", nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := get(""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured: %d", rec.Code)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "netprofile-ceiling.json")
	s.Ceiling = netprofile.NewCeilingStore(path, []netprofile.PinnedKey{netprofile.NewPinnedKey(pub)}, slog.New(slog.DiscardHandler))

	rec := get("")
	if rec.Code != http.StatusServiceUnavailable || !bytes.Contains(rec.Body.Bytes(), []byte("error")) {
		t.Fatalf("missing file: %d %s", rec.Code, rec.Body.String())
	}

	env, err := netprofile.SignCeiling(&netprofile.Ceiling{
		Type: netprofile.CeilingType, Version: 1, Issued: "2026-09-24T10:30:00Z",
		Platforms: map[string][]string{"switch": {".nintendo.net"}},
	}, priv)
	if err != nil {
		t.Fatal(err)
	}
	// Written by hand with odd spacing: served as the bytes on disk, not a
	// re-serialisation.
	env = append([]byte("  "), env...)
	if err := os.WriteFile(path, env, 0o644); err != nil {
		t.Fatal(err)
	}
	rec = get("")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), env) {
		t.Fatalf("serve: %d %q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Header().Get("Cache-Control") != "public, max-age=300" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers: %v", rec.Header())
	}
	if rec := get(etag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d", rec.Code)
	}

	// Replaced by an invalid file: refused, 503, without a restart.
	bad := bytes.Replace(env, []byte(`"sig": "`), []byte(`"sig": "AAAA`), 1)
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(path, later, later)
	if rec := get(""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid file served: %d", rec.Code)
	}
	if rec := get(etag); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid file answered 304: %d", rec.Code)
	}
}
