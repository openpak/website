package netprofile

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PrivateKey, []PinnedKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, []PinnedKey{NewPinnedKey(pub)}
}

func testCeiling(version int, platforms map[string][]string) *Ceiling {
	return &Ceiling{Type: CeilingType, Version: version, Issued: "2026-09-24T10:30:00Z", Platforms: platforms}
}

func mustSign(t *testing.T, c *Ceiling, key ed25519.PrivateKey) []byte {
	t.Helper()
	b, err := SignCeiling(c, key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyCeiling(t *testing.T) {
	key, pinned := testKey(t)
	good := mustSign(t, testCeiling(3, map[string][]string{
		"switch": {".nintendo.net", ".demonware.net"},
		"wiiu":   {".nintendo.net"},
	}), key)

	c, err := VerifyCeiling(good, pinned)
	if err != nil {
		t.Fatalf("good envelope: %v", err)
	}
	if c.Version != 3 || !c.Covers("switch", "x.demonware.net") || !c.Covers("switch", "demonware.net") ||
		c.Covers("wiiu", "x.demonware.net") || c.Covers("switch", "notdemonware.net") {
		t.Fatalf("payload: %+v", c)
	}

	var env Envelope
	if err := json.Unmarshal(good, &env); err != nil {
		t.Fatal(err)
	}
	reenc := func(e Envelope) []byte { b, _ := json.Marshal(e); return b }

	// Tampered payload: a wider ceiling under the old signature.
	tampered := env
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	tampered.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte(`".demonware.net"`), []byte(`".example.net"`), 1))
	if _, err := VerifyCeiling(reenc(tampered), pinned); err == nil {
		t.Error("tampered payload verified")
	}

	// Unknown keyid, and a valid signature from a key nobody pinned.
	unknown := env
	unknown.Signatures = []Signature{{KeyID: strings.Repeat("ab", 32), Sig: env.Signatures[0].Sig}}
	if _, err := VerifyCeiling(reenc(unknown), pinned); err == nil {
		t.Error("unknown keyid verified")
	}
	other, _ := testKey(t)
	if _, err := VerifyCeiling(mustSign(t, testCeiling(4, map[string][]string{"switch": {".nintendo.net"}}), other), pinned); err == nil {
		t.Error("unpinned key verified")
	}

	// Bad base64 in the payload and in the signature.
	badPayload := env
	badPayload.Payload = "!!not base64!!"
	if _, err := VerifyCeiling(reenc(badPayload), pinned); err == nil {
		t.Error("bad payload base64 verified")
	}
	badSig := env
	badSig.Signatures = []Signature{{KeyID: env.Signatures[0].KeyID, Sig: "%%%"}}
	if _, err := VerifyCeiling(reenc(badSig), pinned); err == nil {
		t.Error("bad signature base64 verified")
	}
	if _, err := VerifyCeiling([]byte("not json"), pinned); err == nil {
		t.Error("garbage verified")
	}

	// A malformed entry rejects the whole file, even correctly signed. The
	// signer refuses to produce one, so sign the bytes by hand.
	for _, bad := range []string{
		`{"type":"openpak-ceiling","version":1,"issued":"2026-09-24T10:30:00Z","platforms":{"switch":[".com"]}}`,
		`{"type":"openpak-ceiling","version":1,"issued":"2026-09-24T10:30:00Z","platforms":{"switch":["nintendo.net"]}}`,
		`{"type":"openpak-ceiling","version":1,"issued":"2026-09-24T10:30:00Z","platforms":{"switch":[".Nintendo.net"]}}`,
		`{"type":"openpak-ceiling","version":1,"issued":"2026-09-24T10:30:00Z","platforms":{"xbox":[".xbox.com"]}}`,
		`{"type":"something-else","version":1,"issued":"2026-09-24T10:30:00Z","platforms":{"switch":[".nintendo.net"]}}`,
		`{"type":"openpak-ceiling","version":0,"issued":"2026-09-24T10:30:00Z","platforms":{"switch":[".nintendo.net"]}}`,
	} {
		p := []byte(bad)
		e := Envelope{Payload: base64.StdEncoding.EncodeToString(p), Signatures: []Signature{{
			KeyID: pinned[0].KeyID, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, p)),
		}}}
		if _, err := VerifyCeiling(reenc(e), pinned); err == nil {
			t.Errorf("malformed payload verified: %s", bad)
		}
	}

	// Unknown fields are ignored.
	p := []byte(`{"type":"openpak-ceiling","version":2,"issued":"2026-09-24T10:30:00Z","future":true,"platforms":{"ds":[".gamespy.com"]}}`)
	e := Envelope{Payload: base64.StdEncoding.EncodeToString(p), Signatures: []Signature{
		{KeyID: "00", Sig: "AA=="},
		{KeyID: pinned[0].KeyID, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, p))},
	}}
	if _, err := VerifyCeiling(reenc(e), pinned); err != nil {
		t.Errorf("unknown field or a second, bad signature rejected the file: %v", err)
	}
}

func TestPinnedKeyMatchesTheContract(t *testing.T) {
	if len(PinnedKeys) != 1 || PinnedKeys[0].KeyID != "36e8bcdd93c2c1a1d7e5d87bbba05a7a4f97882131cf6878f1c053a25a377fe4" {
		t.Fatalf("pinned keys: %+v", PinnedKeys)
	}
}

func TestValidFamily(t *testing.T) {
	for _, ok := range []string{".ea.com", ".nintendo.net", ".srv.nintendo.net", ".among.us"} {
		if err := ValidFamily(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{".com", "ea.com", ".EA.com", "..com", ".ea..com", ".*.ea.com", ".ea.com.", ""} {
		if ValidFamily(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func writeFile(t *testing.T, path string, b []byte, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// The store serves the file byte for byte, re-reads on change, refuses an
// invalid file, and does not do the clients' rollback check.
func TestCeilingStore(t *testing.T) {
	key, pinned := testKey(t)
	path := filepath.Join(t.TempDir(), "ceiling.json")
	s := NewCeilingStore(path, pinned, slog.New(slog.DiscardHandler))

	if _, _, _, err := s.Current(); !errors.Is(err, ErrNoCeiling) {
		t.Fatalf("missing file: %v", err)
	}
	if s.Effective().Version != 0 || !s.Effective().Covers("switch", ".epicgames.dev") {
		t.Fatal("missing file does not fall back to Universe")
	}

	v2 := mustSign(t, testCeiling(2, map[string][]string{"switch": {".nintendo.net"}}), key)
	base := time.Now().Add(-time.Hour)
	writeFile(t, path, v2, base)
	body, etag, c, err := s.Current()
	if err != nil || !bytes.Equal(body, v2) || c.Version != 2 || etag == "" {
		t.Fatalf("v2: %v %d", err, c.Version)
	}

	// Replaced in place with a lower, validly signed version: served. Rollback
	// protection is each client's, against the highest version it accepted.
	v1 := mustSign(t, testCeiling(1, map[string][]string{"switch": {".nintendo.net", ".nintendo.com"}}), key)
	writeFile(t, path, v1, base.Add(time.Minute))
	body, etag2, c, err := s.Current()
	if err != nil || !bytes.Equal(body, v1) || c.Version != 1 || etag2 == etag {
		t.Fatalf("v1 after v2: %v", err)
	}

	// Invalid: refused, and the generator falls back.
	writeFile(t, path, bytes.Replace(v1, []byte(`"payload": "`), []byte(`"payload": "x`), 1), base.Add(2*time.Minute))
	if _, _, _, err := s.Current(); !errors.Is(err, ErrNoCeiling) {
		t.Fatalf("invalid file served: %v", err)
	}
	if s.Effective().Version != 0 {
		t.Fatal("invalid file used for generation")
	}
}

// Generation obeys the signed ceiling: an out-of-ceiling family, override or
// service is dropped on its own, the rest of the platform stays; and a family
// outside the compiled Universe is served once the signed ceiling has it.
func TestGeneratorObeysTheSignedCeiling(t *testing.T) {
	key, pinned := testKey(t)
	dir := t.TempDir()
	mapPath := filepath.Join(dir, "platforms.yml")
	if err := os.WriteFile(mapPath, []byte(`
public_base_url: https://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch:
    families: [".nintendo.net", ".demonware.net", ".example.net"]
    exact: ["api.other.org"]
    overrides: {"probe.demonware.net": "10.0.0.2", "nncs2.nintendo.net": "10.0.0.3"}
  wiiu:
    families: [".nintendo.net", ".openpak.org"]
    services: {act: "https://account.openpak.org", nus: "https://nus.nintendo.net/x"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ceilPath := filepath.Join(dir, "ceiling.json")
	writeFile(t, ceilPath, mustSign(t, testCeiling(1, map[string][]string{
		"switch": {".nintendo.net", ".example.net"},
		"wiiu":   {".nintendo.net"},
	}), key), time.Now())

	var logs bytes.Buffer
	s, err := New(Options{
		TraefikFile: "testdata/traefik.yml",
		PlatformMap: mapPath,
		Ceiling:     NewCeilingStore(ceilPath, pinned, quietLogger()),
		Log:         slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("a map partly outside the ceiling must still load: %v", err)
	}

	sw := mustProfile(t, s, "switch")
	if !contains(sw.Redirect.Suffixes, ".nintendo.net") || !contains(sw.Redirect.Suffixes, ".example.net") {
		t.Fatalf("switch lost in-ceiling families: %v", sw.Redirect.Suffixes)
	}
	if contains(sw.Redirect.Suffixes, ".demonware.net") || contains(sw.Redirect.Exact, "api.other.org") {
		t.Fatalf("switch kept out-of-ceiling names: %+v", sw.Redirect)
	}
	if _, ok := sw.Redirect.Overrides["probe.demonware.net"]; ok || sw.Redirect.Overrides["nncs2.nintendo.net"] != "10.0.0.3" {
		t.Fatalf("overrides: %v", sw.Redirect.Overrides)
	}
	wu := mustProfile(t, s, "wiiu")
	if len(wu.Services) != 1 || wu.Services[0].ID != "nus" || contains(wu.Redirect.Suffixes, ".openpak.org") {
		t.Fatalf("wiiu: %+v %+v", wu.Redirect, wu.Services)
	}
	all := mustProfile(t, s, PlatformAll)
	if contains(all.Redirect.Suffixes, ".demonware.net") || !contains(all.Redirect.Suffixes, ".example.net") {
		t.Fatalf("union: %v", all.Redirect.Suffixes)
	}
	for _, want := range []string{"family .demonware.net", "exact api.other.org", "override probe.demonware.net", "family .openpak.org", "service act"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("drop of %q not logged:\n%s", want, logs.String())
		}
	}

	// Without a verified ceiling the compiled Universe bounds the map again:
	// .example.net is not in it.
	s2, err := New(Options{TraefikFile: "testdata/traefik.yml", PlatformMap: mapPath, Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if sw := mustProfile(t, s2, "switch"); contains(sw.Redirect.Suffixes, ".example.net") || !contains(sw.Redirect.Suffixes, ".demonware.net") {
		t.Fatalf("fallback: %v", sw.Redirect.Suffixes)
	}
}
