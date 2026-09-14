package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"
)

// TestPGStoreParity runs the Postgres store through the rules the memory
// store enforces, plus the one thing only a real database can prove: the
// sequence counter surviving a full close and reopen. CI has no database, so
// this runs only when REGISTRY_TEST_DATABASE_URL is set.
func TestPGStoreParity(t *testing.T) {
	dsn := os.Getenv("REGISTRY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REGISTRY_TEST_DATABASE_URL not set; Postgres parity skipped")
	}
	ctx := context.Background()
	s1, err := OpenPG(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s1.Close()
	for _, table := range []string{"registry_build_sessions", "registry_revisions", "registry_channels", "registry_sequences", "registry_clients"} {
		if _, err := s1.pool.Exec(ctx, `DELETE FROM `+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}

	// Sequence allocation: monotonic, distinct, counter in its own row.
	a1, err := s1.NextSequence(ctx, "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s1.NextSequence(ctx, "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if a2 != a1+1 {
		t.Fatalf("allocations %d then %d", a1, a2)
	}
	b1, err := s1.NextSequence(ctx, "wiiu", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if b1 != 1 {
		t.Fatalf("wiiu allocation %d, want 1", b1)
	}

	// A revision records and looks up.
	digest := sha256.Sum256([]byte("bundle-bytes"))
	bundleSHA := hex.EncodeToString(digest[:])
	rev := Revision{
		PolicyID:       "openpak-production",
		Platform:       "switch",
		Channel:        "stable",
		Sequence:       a1,
		BundleSHA256:   bundleSHA,
		ManifestSHA256: hex.EncodeToString([]byte("0000000000000000000000000000000000000000000000000000000000000000")),
		Metadata:       map[string]string{"root.json": "cafedead"},
		IssuedAt:       time.Now(),
	}
	got, err := s1.InsertRevision(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}
	if got != a1 {
		t.Fatalf("insert returned sequence %d, want %d", got, a1)
	}
	byDigest, err := s1.RevisionByBundleDigest(ctx, "switch", "stable", bundleSHA)
	if err != nil || byDigest.Metadata["root.json"] != "cafedead" {
		t.Fatalf("digest lookup: %+v err=%v", byDigest, err)
	}
	if err := s1.Promote(ctx, "switch", "stable", bundleSHA); err != nil {
		t.Fatal(err)
	}
	cur, err := s1.Channel(ctx, "switch", "stable")
	if err != nil || cur.BundleSHA256 != bundleSHA {
		t.Fatalf("channel: %+v err=%v", cur, err)
	}
	// Duplicate sequence: the primary key refuses, mapped to ErrConflict.
	rev.ManifestSHA256 = "duplicate"
	if _, err := s1.InsertRevision(ctx, rev); err != ErrConflict {
		t.Fatalf("duplicate sequence: %v", err)
	}

	// The client and session round trip.
	client := Client{
		ID: "client-1", ClientHash: hex.EncodeToString([]byte("hash-1")),
		Repository: "openpak/citron", Status: ClientProductionApproved,
		Environment: EnvProduction, Platforms: []string{"switch"}, Channels: []string{"stable"},
	}
	if err := s1.PutClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetClientStatus(ctx, "client-1", ClientSuspended); err != nil {
		t.Fatal(err)
	}
	c, err := s1.ClientByHash(ctx, client.ClientHash)
	if err != nil || c.Status != ClientSuspended || c.RevocationEpoch != 1 {
		t.Fatalf("client after suspension: %+v err=%v", c, err)
	}
	sess := BuildSession{
		ID: "sess-1", Digest: "digest-1", ClientID: "client-1",
		ClientHash: client.ClientHash, Platform: "switch", Channel: "stable",
		ClientEpochAtIssue: 0, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := s1.CreateBuildSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := s1.MarkSessionResolved(ctx, "sess-1", "manifest-1"); err != nil {
		t.Fatal(err)
	}
	if res, err := s1.SessionResolution(ctx, "sess-1"); err != nil || res != "manifest-1" {
		t.Fatalf("session resolution: %q err=%v", res, err)
	}

	// The restart proof: a fresh pool must continue the counter exactly.
	s1.Close()
	s2, err := OpenPG(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	a3, err := s2.NextSequence(ctx, "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if a3 != a2+1 {
		t.Fatalf("sequence after restart %d, want %d", a3, a2+1)
	}
	revAfter, err := s2.Revision(ctx, "switch", "stable", a1)
	if err != nil || revAfter.BundleSHA256 != bundleSHA {
		t.Fatalf("revision after restart: %+v err=%v", revAfter, err)
	}
}
