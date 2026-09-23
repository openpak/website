package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/policy"
)

// A fixture map with the drift cases; the same shape the policy package's
// tests use, because the registry publishes what that package generates.
const fixtureMapYAML = `
public_base_url: https://openpak.org
server:
  address: 145.241.199.19
  https_port: 443
  http_port: 80
recheck_after: 21600
platforms:
  switch:
    families: [".nintendo.net", ".nintendo.com", ".battle.net"]
    exact:
      - "matchmaker.among.us"
      - "ns.photonengine.io"
    overrides:
      "nncs2-lp1.n.n.srv.nintendo.net": "145.241.228.207"
    never: ["conntest.nintendowifi.net"]
    services: {}
  wiiu:
    families: [".nintendo.net", ".nintendowifi.net", ".openpak.org"]
    never: ["conntest.nintendowifi.net"]
    services:
      act: "https://account.openpak.org"
      ccs: "http://ccs.cdn.wup.shop.openpak.org/ccs/download"
`

type fixtureClock struct{ t time.Time }

func (c *fixtureClock) Now() time.Time { return c.t }
func (c *fixtureClock) Advance(d time.Duration) {
	c.t = c.t.Add(d)
}

type fixture struct {
	svc       *Service
	store     *MemStore
	blobs     *DirBlobs
	clock     *fixtureClock
	canonical *policy.Canonical
	pem       []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := NewMemStore(nil)
	blobs, err := NewDirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootKey, err := GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	onlineKey, err := GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	clock := &fixtureClock{t: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
	publisher, err := NewPublisher(rootKey, onlineKey, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, err := netprofile.LoadMap([]byte(fixtureMapYAML))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := policy.Build(policy.Inputs{
		PolicyID:         "openpak-production",
		Map:              m,
		CA:               &policy.CA{ID: "openpak-ca-2", PEM: []byte("fixture pem"), NotAfter: clock.Now().Add(365 * 24 * time.Hour)},
		CeilingRevisions: map[string]string{"switch": "unified-1", "wiiu": "unified-1"},
		TransportMeta: map[string]map[string]policy.TransportSpec{
			"switch": {
				"nncs2-lp1.n.n.srv.nintendo.net": {Transport: "udp", Ports: []int{10025, 10125}},
				"ns.photonengine.io":             {Transport: "udp", Ports: []int{5055}},
			},
		},
	}, policy.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		svc: &Service{
			Store:     store,
			Blobs:     blobs,
			Publisher: publisher,
			PolicyID:  "openpak-production",
			Now:       clock.Now,
		},
		store:     store,
		blobs:     blobs,
		clock:     clock,
		canonical: canonical,
		pem:       []byte("fixture pem"),
	}
}

func (f *fixture) artifacts() []policy.PayloadInput {
	return []policy.PayloadInput{{
		Path:      policy.TrustArtifactPath,
		MediaType: policy.MediaTypePEM,
		Bytes:     f.pem,
	}}
}

// The happy path: publish with promotion, then verify the whole TUF chain
// the way the fetch tool will (POL-M2's signed publication verifies).
func TestPublishPromoteAndVerifyChain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical,
		Platform:  "switch",
		Channel:   "stable",
		Artifacts: f.artifacts(),
		Promote:   true,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.Sequence != 1 || !res.Promoted {
		t.Fatalf("first publication: sequence %d promoted=%v", res.Sequence, res.Promoted)
	}
	cur, err := f.store.Channel(ctx, "switch", "stable")
	if err != nil || cur.BundleSHA256 != res.Revision.BundleSHA256 {
		t.Fatalf("channel pointer: %+v err=%v", cur, err)
	}

	// Fetch every metadata file the publication recorded and run the
	// verification chain against the manifest.
	rootBytes, err := f.blobs.Get(ctx, res.Metadata["root.json"])
	if err != nil {
		t.Fatal(err)
	}
	targetsBytes, err := f.blobs.Get(ctx, res.Metadata["targets.json"])
	if err != nil {
		t.Fatal(err)
	}
	snapshotBytes, err := f.blobs.Get(ctx, res.Metadata["snapshot.json"])
	if err != nil {
		t.Fatal(err)
	}
	timestampBytes, err := f.blobs.Get(ctx, res.Metadata["timestamp.json"])
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, res.Manifest, f.clock.Now()); err != nil {
		t.Fatalf("verification chain failed on a fresh publication: %v", err)
	}

	// Tampering anywhere in the chain must fail: a flipped byte in the
	// manifest no longer matches the signed target.
	bad := append([]byte(nil), res.Manifest...)
	bad[0] ^= 0xff
	if err := VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, bad, f.clock.Now()); err == nil {
		t.Fatal("verification accepted a tampered manifest")
	}
	// And a root whose signed content was altered is not the pin's root:
	// roll its expiry backward and the chain must refuse it. (Flipping an
	// arbitrary byte proves nothing — it may land outside the signed
	// content, which the pin, not a self-signature, is what authenticates.)
	var doc map[string]any
	if err := json.Unmarshal(rootBytes, &doc); err != nil {
		t.Fatal(err)
	}
	doc["signed"].(map[string]any)["expires"] = "2020-01-01T00:00:00Z"
	badRoot, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPublication(badRoot, targetsBytes, snapshotBytes, timestampBytes, res.Manifest, f.clock.Now()); err == nil {
		t.Fatal("verification accepted an altered root")
	}
	// Expired metadata is not trusted either.
	if err := VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, res.Manifest, f.clock.Now().Add(timestampTTL*2)); err == nil {
		t.Fatal("verification accepted expired timestamp metadata")
	}
}

// Sequences move forward and are never reused. Re-projecting one canonical
// bakes a new sequence and issue time into the bundle bytes, so the bundle
// digest legitimately moves; the canonical digest is the semantic identity
// that persists (POL-003: a timestamp or re-issue never changes it).
func TestSequencesAreMonotonicAndCanonicalIdentityPersists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r1, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)
	r2, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Sequence != r1.Sequence+1 {
		t.Fatalf("second publication sequence %d, want %d", r2.Sequence, r1.Sequence+1)
	}
	if r1.Revision.CanonicalPolicySHA256 != r2.Revision.CanonicalPolicySHA256 {
		t.Fatal("identical canonical produced a different canonical digest")
	}
	if r1.Revision.BundleSHA256 == r2.Revision.BundleSHA256 {
		t.Fatal("bundles issued at different times have identical bytes; issued_at is not riding along")
	}
	rev, err := f.store.RevisionByBundleDigest(ctx, "switch", "stable", r2.Revision.BundleSHA256)
	if err != nil || rev.Sequence != r2.Sequence {
		t.Fatalf("digest lookup returned sequence %d err=%v, want %d", rev.Sequence, err, r2.Sequence)
	}
	// A different platform counts its own sequences.
	r3, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "wiiu", Channel: "stable",
		Artifacts: f.artifacts(), Promote: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r3.Sequence != 1 {
		t.Fatalf("wiiu sequence %d, want 1", r3.Sequence)
	}
}

// A rollback reissues the last good semantic content at a higher sequence.
// Bundle, manifest and signed metadata must all carry that new publication
// identity, while the canonical policy identity stays unchanged (POL-007).
func TestRollbackPublishesHigherSequenceOfTheSameContent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(2 * time.Hour)
	rb, err := f.svc.Rollback(ctx, "switch", "stable")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Sequence != first.Sequence+1 {
		t.Fatalf("rollback sequence %d, want %d", rb.Sequence, first.Sequence+1)
	}
	if rb.Revision.BundleSHA256 == first.Revision.BundleSHA256 {
		t.Fatal("rollback reused a bundle carrying the old publication identity")
	}
	if rb.Revision.CanonicalPolicySHA256 != first.Revision.CanonicalPolicySHA256 {
		t.Fatal("rollback changed the canonical policy identity")
	}
	bundleBytes, err := f.blobs.Get(ctx, rb.Revision.BundleSHA256)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := policy.DecodeBundle(bundleBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := policy.DecodeManifest(rb.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Sequence != rb.Sequence || manifest.Sequence != rb.Sequence {
		t.Fatalf("rollback identity: bundle=%d manifest=%d revision=%d", bundle.Sequence, manifest.Sequence, rb.Sequence)
	}
	if err := policy.ValidateBundleCoherence(bundle, manifest); err != nil {
		t.Fatalf("rollback bundle and manifest disagree: %v", err)
	}
	cur, err := f.store.Channel(ctx, "switch", "stable")
	if err != nil || cur.BundleSHA256 != rb.Revision.BundleSHA256 || cur.Sequence != rb.Sequence {
		t.Fatalf("channel after rollback: %+v err=%v", cur, err)
	}
}

// Withdrawal: the withdrawn digest is dead — promotion refused, pinned
// resolution refused — but its bytes stay retrievable for audit (AUTH-006:
// already distributed bytes cannot be recalled). The same policy content
// returning under a higher sequence is the sanctioned recovery path, so a
// re-projection with a fresh sequence succeeds while the old bytes stay dead.
func TestWithdrawBlocksReuseButKeepsBytes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Withdraw(ctx, res.Revision.BundleSHA256); err != nil {
		t.Fatal(err)
	}
	// The dead digest stays dead.
	rev, err := f.store.RevisionByBundleDigest(ctx, "switch", "stable", res.Revision.BundleSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if rev.WithdrawnAt == nil {
		t.Fatal("withdrawal did not stick")
	}
	if err := f.store.Promote(ctx, "switch", "stable", res.Revision.BundleSHA256); err == nil {
		t.Fatal("a withdrawn bundle was promoted")
	}
	if _, err := f.svc.ResolveChannel(ctx, "switch", "stable"); err == nil {
		t.Fatal("the channel still resolves to withdrawn bytes")
	}
	if _, err := f.blobs.Get(ctx, res.Revision.BundleSHA256); err != nil {
		t.Fatalf("withdrawn bytes left the store: %v", err)
	}
	// The recovery path: the same policy content, a fresh sequence, a live
	// digest (POL-007's higher-sequence last-good publication).
	f.clock.Advance(time.Hour)
	reissue, err := f.svc.Publish(ctx, PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatalf("re-publication of the same content refused: %v", err)
	}
	if reissue.Sequence != res.Sequence+1 || reissue.Revision.BundleSHA256 == res.Revision.BundleSHA256 {
		t.Fatalf("re-publication: sequence %d digest %s", reissue.Sequence, reissue.Revision.BundleSHA256)
	}
	if _, err := f.svc.ResolveChannel(ctx, "switch", "stable"); err != nil {
		t.Fatalf("channel dead after re-publication: %v", err)
	}
}

// Session lifecycle: issuance, expiry, explicit revocation and the client
// revocation epoch each deny on their own (AUTH-002, AUTH-006).
func TestBuildSessionLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hash := sha256.Sum256([]byte("client-secret-1"))
	client := Client{
		ID:          "client-1",
		ClientHash:  hex.EncodeToString(hash[:]),
		Repository:  "openpak/citron",
		Kind:        "emulator",
		Status:      ClientProductionApproved,
		Environment: EnvProduction,
		Platforms:   []string{"switch"},
		Channels:    []string{"stable"},
	}
	if err := f.store.PutClient(ctx, client); err != nil {
		t.Fatal(err)
	}
	ci := &CIAssertion{
		Issuer:      "https://token.actions.githubusercontent.com",
		Repository:  "openpak/citron",
		Workflow:    "release.yml",
		Ref:         "refs/heads/main",
		Commit:      "abc123",
		Environment: EnvProduction,
	}
	raw, sess, err := f.svc.OpenBuildSession(ctx, client.ClientHash, "switch", "stable", ci)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if _, _, err := f.svc.AuthorizeSession(ctx, raw); err != nil {
		t.Fatalf("fresh session denied: %v", err)
	}

	// A client hash alone, without the CI assertion's repository, is not a
	// credential.
	wrong := &CIAssertion{Issuer: ci.Issuer, Repository: "openpak/other", Commit: "x", Environment: EnvProduction}
	if _, _, err := f.svc.OpenBuildSession(ctx, client.ClientHash, "switch", "stable", wrong); err == nil {
		t.Fatal("a foreign repository assertion opened a session")
	}
	// A staging registration cannot ask for production.
	f.store.PutClient(ctx, Client{ID: "client-2", ClientHash: hex.EncodeToString([]byte("c2")),
		Repository: "openpak/citron", Status: ClientStagingApproved, Environment: EnvNonproduction,
		Platforms: []string{"switch"}, Channels: []string{"staging"}})
	h2 := sha256.Sum256([]byte("c2"))
	if _, _, err := f.svc.OpenBuildSession(ctx, hex.EncodeToString(h2[:]), "switch", "stable", ci); err == nil {
		t.Fatal("staging registration received a production session")
	}

	// Expiry denies.
	f.clock.Advance(BuildSessionTTL + time.Minute)
	if _, _, err := f.svc.AuthorizeSession(ctx, raw); err == nil {
		t.Fatal("an expired session authorized")
	}
	f.clock.Advance(-BuildSessionTTL - time.Minute)

	// The revocation epoch denies a session issued before a suspension.
	f.clock.Advance(time.Minute)
	if err := f.store.SetClientStatus(ctx, "client-1", ClientSuspended); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.AuthorizeSession(ctx, raw); err == nil {
		t.Fatal("a session survived its client's suspension")
	}
	// Reinstating the client does not revive sessions issued before the
	// suspension: the epoch never moves backward (AUTH-004).
	f.clock.Advance(time.Minute)
	if err := f.store.SetClientStatus(ctx, "client-1", ClientProductionApproved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.AuthorizeSession(ctx, raw); err == nil {
		t.Fatal("reinstatement revived a pre-suspension session")
	}
	// Explicit session revocation denies on its own, too.
	raw2, _, err := f.svc.OpenBuildSession(ctx, client.ClientHash, "switch", "stable", ci)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RevokeBuildSession(ctx, sess.ID+"x"); err != nil && err != ErrNotFound {
		t.Fatalf("revoking an unknown session: %v", err)
	}
	raw3, sess3, err := f.svc.OpenBuildSession(ctx, client.ClientHash, "switch", "stable", ci)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RevokeBuildSession(ctx, sess3.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.AuthorizeSession(ctx, raw3); err == nil {
		t.Fatal("a revoked session authorized")
	}
	if _, _, err := f.svc.AuthorizeSession(ctx, raw2); err != nil {
		t.Fatalf("an unrelated session was collateral damage: %v", err)
	}
}

// The key file round trip, because provisioning will run it for real.
func TestEd25519SeedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "root.seed")
	priv, err := GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveEd25519(path, priv); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("seed file mode %v, want 0600", info.Mode().Perm())
	}
	back, err := LoadEd25519(path)
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(back) {
		t.Fatal("seed round trip changed the key")
	}
}
