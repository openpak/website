package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

func writeTestSigningKey(t *testing.T, dir string) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pub
}

func TestCeilingSignAddRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath, pub := writeTestSigningKey(t, dir)
	oldKeys, oldNow := ceilingKeys, ceilingNow
	ceilingKeys = []netprofile.PinnedKey{netprofile.NewPinnedKey(pub)}
	ceilingNow = func() time.Time { return time.Date(2026, 9, 24, 10, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { ceilingKeys, ceilingNow = oldKeys, oldNow })

	ceil := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := cmdCeiling(args, &out)
		return out.String(), err
	}
	verify := func(path string) *netprofile.Ceiling {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		c, err := netprofile.VerifyCeiling(b, ceilingKeys)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return c
	}

	payload := filepath.Join(dir, "payload.json")
	os.WriteFile(payload, []byte(`{"platforms":{"switch":[".nintendo.net",".nintendo.com"],"wiiu":[".nintendo.net"]}}`), 0o644)
	v1 := filepath.Join(dir, "v1.json")
	out, err := ceil("sign", "--in", payload, "--no-current", "--key", keyPath, "--out", v1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "scp "+v1+" <prod-host>:~/openpak/deploy/netprofile-ceiling.json") {
		t.Fatalf("no scp line:\n%s", out)
	}
	if c := verify(v1); c.Version != 1 || c.Issued != "2026-09-24T10:30:00Z" || c.Type != netprofile.CeilingType {
		t.Fatalf("v1: %+v", c)
	}

	// Positionals before the flags.
	v2 := filepath.Join(dir, "v2.json")
	if _, err := ceil("add", "switch", ".demonware.net", "--file", v1, "--key", keyPath, "--out", v2); err != nil {
		t.Fatal(err)
	}
	c2 := verify(v2)
	if c2.Version != 2 || !slices.Contains(c2.Platforms["switch"], ".demonware.net") || c2.Covers("wiiu", "x.demonware.net") {
		t.Fatalf("v2: %+v", c2)
	}

	// Flags before the positionals.
	v3 := filepath.Join(dir, "v3.json")
	if _, err := ceil("remove", "--file", v2, "--key", keyPath, "--out", v3, "switch", ".nintendo.com"); err != nil {
		t.Fatal(err)
	}
	if c3 := verify(v3); c3.Version != 3 || slices.Contains(c3.Platforms["switch"], ".nintendo.com") || !slices.Contains(c3.Platforms["switch"], ".demonware.net") {
		t.Fatalf("v3: %+v", c3)
	}

	// sign on top of a current envelope bumps past it.
	v4 := filepath.Join(dir, "v4.json")
	if _, err := ceil("sign", "--in", payload, "--file", v3, "--key", keyPath, "--out", v4); err != nil {
		t.Fatal(err)
	}
	if c4 := verify(v4); c4.Version != 4 {
		t.Fatalf("v4: %+v", c4)
	}

	if out, err := ceil("show", "--file", v4); err != nil || !strings.Contains(out, "version 4") || !strings.Contains(out, ".nintendo.com") {
		t.Fatalf("show: %v\n%s", err, out)
	}

	no := filepath.Join(dir, "never.json")
	for name, args := range map[string][]string{
		"duplicate":        {"add", "switch", ".nintendo.net", "--file", v1},
		"one label":        {"add", "switch", ".com", "--file", v1},
		"no leading dot":   {"add", "switch", "ea.com", "--file", v1},
		"upper case":       {"add", "switch", ".EA.com", "--file", v1},
		"unknown platform": {"add", "xbox", ".xboxlive.com", "--file", v1},
		"absent remove":    {"remove", "wiiu", ".among.us", "--file", v1},
		"sign lower":       {"sign", "--in", payload, "--file", v1},
	} {
		if name == "sign lower" {
			os.WriteFile(payload, []byte(`{"platforms":{"switch":[".com"]}}`), 0o644)
		}
		if _, err := ceil(append(args, "--key", keyPath, "--out", no)...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(no); err == nil {
		t.Fatal("a refused edit wrote a file")
	}

	// A key clients do not pin is refused, and so is a tampered current file.
	otherKey, _ := writeTestSigningKey(t, t.TempDir())
	if _, err := ceil("add", "ds", ".gamespy.com", "--file", v1, "--key", otherKey, "--out", no); err == nil {
		t.Error("unpinned signing key accepted")
	}
	b, _ := os.ReadFile(v1)
	tampered := filepath.Join(dir, "tampered.json")
	os.WriteFile(tampered, bytes.Replace(b, []byte(`"sig": "`), []byte(`"sig": "AAAA`), 1), 0o644)
	if _, err := ceil("add", "ds", ".gamespy.com", "--file", tampered, "--key", keyPath, "--out", no); err == nil {
		t.Error("edit on top of an unverified envelope accepted")
	}
}
