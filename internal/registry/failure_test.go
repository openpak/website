package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/openpak/website/internal/policy"
)

// Fault injection for POL-M4: a publication attempt that fails anywhere —
// artifact storage, manifest storage, metadata storage, the revision record,
// the pointer move — must leave every consumer on the prior complete
// revision, and the sequences it burned must never come back.

// flakyBlobs fails selected Put calls by their 1-based index within the
// publication attempt under test. Publish's put order is deterministic:
// policy.json, each artifact, the manifest, then the four metadata roles.
type flakyBlobs struct {
	Blobs
	mu      sync.Mutex
	failPut map[int]bool
	puts    int
}

func (b *flakyBlobs) arm(indexes ...int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failPut = map[int]bool{}
	for _, i := range indexes {
		b.failPut[i] = true
	}
	b.puts = 0
}

func (b *flakyBlobs) Put(ctx context.Context, data []byte) (string, error) {
	b.mu.Lock()
	b.puts++
	n := b.puts
	fail := b.failPut[n]
	b.mu.Unlock()
	if fail {
		return "", fmt.Errorf("injected blob outage at put %d", n)
	}
	return b.Blobs.Put(ctx, data)
}

// flakyStore fails the revision record or the pointer move on demand.
type flakyStore struct {
	Store
	mu           sync.Mutex
	failInsert   bool
	failPromote  bool
	insertsSeen  int
	promotesSeen int
}

func (s *flakyStore) arm(failInsert, failPromote bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failInsert, s.failPromote = failInsert, failPromote
}

func (s *flakyStore) InsertRevision(ctx context.Context, r Revision) (int64, error) {
	s.mu.Lock()
	s.insertsSeen++
	fail := s.failInsert
	s.mu.Unlock()
	if fail {
		return 0, errors.New("injected revision-record outage")
	}
	return s.Store.InsertRevision(ctx, r)
}

func (s *flakyStore) Promote(ctx context.Context, platform, channel, bundleSHA256 string) error {
	s.mu.Lock()
	s.promotesSeen++
	fail := s.failPromote
	s.mu.Unlock()
	if fail {
		return errors.New("injected pointer-move outage")
	}
	return s.Store.Promote(ctx, platform, channel, bundleSHA256)
}

func mustPublishPromoted(t *testing.T, f *fixture, platform string) PublicationResult {
	t.Helper()
	res, err := f.svc.Publish(context.Background(), PublishInput{
		Canonical: f.canonical, Platform: platform, Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	return res
}

// Each injection point is one stage of Publish. After the injected failure
// the channel still answers with the prior revision, the prior revision's
// signed bytes are all still served, and the next good publication continues
// after the burned sequence instead of reusing it.
func TestFailureInjectedPublicationLeavesConsumersOnThePriorRevision(t *testing.T) {
	// Blob-put indexes within one Publish: 1 policy.json, 2 the CA
	// artifact, 3 the manifest, 4-7 the metadata roles.
	stages := []struct {
		name   string
		inject func(fb *flakyBlobs, fs *flakyStore)
	}{
		{"artifact blob", func(fb *flakyBlobs, fs *flakyStore) { fb.arm(2) }},
		{"manifest blob", func(fb *flakyBlobs, fs *flakyStore) { fb.arm(3) }},
		{"first metadata blob", func(fb *flakyBlobs, fs *flakyStore) { fb.arm(4) }},
		{"last metadata blob", func(fb *flakyBlobs, fs *flakyStore) { fb.arm(7) }},
		{"revision record", func(fb *flakyBlobs, fs *flakyStore) { fs.arm(true, false) }},
		{"channel promote", func(fb *flakyBlobs, fs *flakyStore) { fs.arm(false, true) }},
	}
	for _, tc := range stages {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			fb := &flakyBlobs{Blobs: f.blobs, failPut: map[int]bool{}}
			fs := &flakyStore{Store: f.store}
			f.svc.Blobs = fb
			f.svc.Store = fs
			ctx := context.Background()

			first := mustPublishPromoted(t, f, "switch")

			tc.inject(fb, fs)
			f.clock.Advance(time.Hour)
			_, err := f.svc.Publish(ctx, PublishInput{
				Canonical: f.canonical, Platform: "switch", Channel: "stable",
				Artifacts: f.artifacts(), Promote: true,
			})
			if err == nil {
				t.Fatal("the injected failure did not fail the publication")
			}

			// Consumers resolve the prior revision and every signed byte of
			// it is still there — nothing partial, nothing missing.
			rev, err := f.svc.ResolveChannel(ctx, "switch", "stable")
			if err != nil {
				t.Fatalf("the channel stopped resolving after a failed publication: %v", err)
			}
			if rev.BundleSHA256 != first.Revision.BundleSHA256 {
				t.Fatal("a failed publication moved the channel pointer")
			}
			for name, digest := range first.Revision.Metadata {
				if _, err := f.blobs.Get(ctx, digest); err != nil {
					t.Fatalf("prior revision metadata %s became unavailable: %v", name, err)
				}
			}

			// The burned sequence is gone for good: the next good
			// publication takes first+2, never the failed attempt's number.
			fb.arm()
			fs.arm(false, false)
			f.clock.Advance(time.Hour)
			again, err := f.svc.Publish(ctx, PublishInput{
				Canonical: f.canonical, Platform: "switch", Channel: "stable",
				Artifacts: f.artifacts(), Promote: true,
			})
			if err != nil {
				t.Fatalf("recovery publication refused: %v", err)
			}
			if want := first.Sequence + 2; again.Sequence != want {
				t.Fatalf("recovery sequence %d, want %d (the burned number must not be reused)", again.Sequence, want)
			}
			cur, err := f.svc.ResolveChannel(ctx, "switch", "stable")
			if err != nil || cur.BundleSHA256 != again.Revision.BundleSHA256 {
				t.Fatalf("channel after recovery: %+v err=%v", cur, err)
			}
		})
	}
}

// A publisher outage — no signing identity at all — ships nothing at all,
// not even an unsigned revision (POL-006).
func TestPublicationWithoutSigningIdentityShipsNothing(t *testing.T) {
	f := newFixture(t)
	f.svc.Publisher = nil
	_, err := f.svc.Publish(context.Background(), PublishInput{
		Canonical: f.canonical, Platform: "switch", Channel: "stable",
		Artifacts: f.artifacts(), Promote: true,
	})
	if err == nil {
		t.Fatal("publication proceeded without a signing identity")
	}
	if _, err := f.store.Channel(context.Background(), "switch", "stable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a channel pointer appeared despite the outage: %+v err=%v", f.store.channels, err)
	}
}

// Rollback is the sanctioned answer to a bad publication, so it must survive
// the same failures the original publication can hit — and when it does
// succeed, its fresh metadata must carry the whole verification chain the
// fetch tool runs (POL-M4: rollback remains verifiable).
func TestRollbackRemainsVerifiableThroughInjectedFailures(t *testing.T) {
	f := newFixture(t)
	fb := &flakyBlobs{Blobs: f.blobs, failPut: map[int]bool{}}
	f.svc.Blobs = fb
	ctx := context.Background()
	first := mustPublishPromoted(t, f, "switch")

	// A rollback whose metadata storage fails leaves the pointer alone.
	// Rollback's put order is the four metadata roles; fail the second.
	fb.arm(2)
	f.clock.Advance(time.Hour)
	if _, err := f.svc.Rollback(ctx, "switch", "stable"); err == nil {
		t.Fatal("the injected failure did not fail the rollback")
	}
	cur, err := f.store.Channel(ctx, "switch", "stable")
	if err != nil || cur.BundleSHA256 != first.Revision.BundleSHA256 {
		t.Fatalf("a failed rollback moved the channel: %+v err=%v", cur, err)
	}

	// The recovered rollback publishes a higher sequence referencing the
	// last good content, and the whole chain verifies against its bytes.
	fb.arm()
	f.clock.Advance(time.Hour)
	rb, err := f.svc.Rollback(ctx, "switch", "stable")
	if err != nil {
		t.Fatalf("recovered rollback: %v", err)
	}
	if want := first.Sequence + 2; rb.Sequence != want {
		t.Fatalf("rollback sequence %d, want %d", rb.Sequence, want)
	}
	if rb.Revision.BundleSHA256 != first.Revision.BundleSHA256 {
		t.Fatal("rollback changed the content it references")
	}
	fetched := make(map[string][]byte, 4)
	for _, name := range MetadataNames {
		data, err := f.blobs.Get(ctx, rb.Metadata[name])
		if err != nil {
			t.Fatalf("rollback metadata %s unavailable: %v", name, err)
		}
		fetched[name] = data
	}
	manifest, err := f.blobs.Get(ctx, rb.Revision.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPublication(fetched["root.json"], fetched["targets.json"], fetched["snapshot.json"], fetched["timestamp.json"], manifest, f.clock.Now()); err != nil {
		t.Fatalf("rollback metadata does not verify: %v", err)
	}
}

// The PRD 07 load drill, at CI scale: concurrent consumers read the channel
// while publications promote under them. Every read must land on a complete
// revision — bundle, manifest and all four metadata roles present — the
// sequence a consumer sees must never move backward, and metadata round
// trips stay inside the 500 ms p95 target with room to spare.
func TestLoadDrillConcurrentPublicationKeepsConsumersComplete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := mustPublishPromoted(t, f, "switch")

	var mu sync.Mutex
	published := map[string]bool{first.Revision.BundleSHA256: true}
	var latencies []time.Duration

	stop := make(chan struct{})
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	reader := func(lastSeen *int64, seenMu *sync.Mutex) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			rev, err := f.svc.ResolveChannel(ctx, "switch", "stable")
			if err != nil {
				errs <- fmt.Errorf("resolve: %w", err)
				return
			}
			bundleBytes, err := f.blobs.Get(ctx, rev.BundleSHA256)
			if err != nil {
				errs <- fmt.Errorf("bundle bytes: %w", err)
				return
			}
			b, err := policy.DecodeBundle(bundleBytes)
			if err != nil {
				errs <- fmt.Errorf("bundle decode: %w", err)
				return
			}
			if b.Sequence != rev.Sequence {
				errs <- fmt.Errorf("torn read: bundle says sequence %d, channel says %d", b.Sequence, rev.Sequence)
				return
			}
			for _, digest := range rev.Metadata {
				if _, err := f.blobs.Get(ctx, digest); err != nil {
					errs <- fmt.Errorf("metadata bytes: %w", err)
					return
				}
			}
			seenMu.Lock()
			if rev.Sequence < *lastSeen {
				seenMu.Unlock()
				errs <- fmt.Errorf("the channel moved backward: %d after %d", rev.Sequence, *lastSeen)
				return
			}
			*lastSeen = rev.Sequence
			seenMu.Unlock()
			mu.Lock()
			latencies = append(latencies, time.Since(start))
			published[rev.BundleSHA256] = true
			mu.Unlock()
		}
	}

	const readers = 8
	seenMu := &sync.Mutex{}
	lastSeen := make([]int64, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go reader(&lastSeen[i], seenMu)
	}

	const publications = 25
	for i := 0; i < publications; i++ {
		f.clock.Advance(time.Minute)
		res, err := f.svc.Publish(ctx, PublishInput{
			Canonical: f.canonical, Platform: "switch", Channel: "stable",
			Artifacts: f.artifacts(), Promote: true,
		})
		if err != nil {
			t.Fatalf("load drill publication %d failed: %v", i+1, err)
		}
		mu.Lock()
		published[res.Revision.BundleSHA256] = true
		mu.Unlock()
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(latencies) == 0 {
		t.Fatal("the readers completed no round trips")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[len(latencies)*95/100]
	if p95 > 500*time.Millisecond {
		t.Fatalf("metadata round trip p95 %v is over the 500 ms target", p95)
	}
	t.Logf("%d consumers round trips over %d publications, p95 %v", len(latencies), publications, p95)
}
