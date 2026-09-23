package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openpak/website/internal/policy"
)

// Service publishes bundles: verify first, store immutably, sign, then —
// and only then — record the revision and move the channel pointer
// (POL-007, POL-008). A failure at any stage leaves consumers on the prior
// complete revision.
type Service struct {
	Store     Store
	Blobs     Blobs
	Publisher *Publisher
	PolicyID  string
	// Now is the clock; tests inject.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// PublishInput carries one publication: the pinned canonical revision plus
// the platform/channel to project. The service does the projecting, so the
// bundle bytes, the registry sequence and the TUF metadata versions are one
// and the same number, bound at publication time.
type PublishInput struct {
	Canonical *policy.Canonical
	Platform  string
	Channel   string
	// Artifacts are the payloads beyond the policy document itself: the CA
	// PEM at policy.TrustArtifactPath, console patch artifacts, and so on.
	Artifacts []policy.PayloadInput
	// Promote moves the channel pointer to this revision atomically after
	// verification and storage succeed.
	Promote bool
}

// PublicationResult reports what one Publish produced.
type PublicationResult struct {
	Revision Revision
	Metadata map[string]string // metadata name → blob digest
	Manifest []byte
	Promoted bool
	Sequence int64
}

// Publish verifies and stores one revision.
func (s *Service) Publish(ctx context.Context, in PublishInput) (PublicationResult, error) {
	if s.Publisher == nil {
		return PublicationResult{}, fmt.Errorf("registry: no signing identity configured; an unsigned bundle can never ship (POL-006)")
	}
	// 1. Allocate: the sequence is a registry fact, not a generator fact.
	// Everything signed below carries exactly this number.
	sequence, err := s.Store.NextSequence(ctx, in.Platform, in.Channel)
	if err != nil {
		return PublicationResult{}, err
	}

	// 2. Project and verify: decode strict, validate, build the manifest,
	// and prove the bundle and its artifacts describe one coherent
	// publication. A bundle that fails here leaves no bytes behind; the
	// burned sequence is the only trace.
	b, err := in.Canonical.Project(in.Platform, in.Channel, sequence, s.now())
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	if b.PolicyID != s.PolicyID {
		return PublicationResult{}, fmt.Errorf("%w: bundle policy id %q is not %q", ErrUnverified, b.PolicyID, s.PolicyID)
	}
	if err := b.Validate(envFor(in.Channel)); err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	bundleBytes, err := policy.MarshalBundle(b)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	payloads := append([]policy.PayloadInput{{
		Path:      policy.PolicyPayloadPath,
		MediaType: policy.MediaTypeJSON,
		Bytes:     bundleBytes,
	}}, in.Artifacts...)
	manifest, err := policy.BuildManifest(b.PolicyID, b.Platform, in.Channel, sequence, b.RequiredCapabilities, payloads)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return PublicationResult{}, err
	}
	if err := policy.ValidateBundleCoherence(b, manifest); err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}

	// 3. Store: every byte the publication needs goes into the blob store
	// before any record exists — a partially stored publication promotes
	// nothing (POL-M4's failure-injection target).
	digest := sha256.Sum256(bundleBytes)
	bundleSHA256 := hex.EncodeToString(digest[:])
	for _, p := range payloads {
		if _, err := s.Blobs.Put(ctx, p.Bytes); err != nil {
			return PublicationResult{}, fmt.Errorf("registry: store %s: %w", p.Path, err)
		}
	}
	manifestDigest, err := s.Blobs.Put(ctx, manifestBytes)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("registry: store manifest: %w", err)
	}

	// 4. Sign: TUF metadata over the manifest, versioned with the allocated
	// sequence, so metadata versions follow sequences and anti-rollback
	// falls out of the sequence rule.
	metadataDigests, err := s.signAndStore(ctx, signInput{
		sequence:      sequence,
		manifestBytes: manifestBytes,
		bundle:        b,
		channel:       in.Channel,
	})
	if err != nil {
		return PublicationResult{}, err
	}

	// 5. Record: the revision row appears only once every byte it names is
	// in the blob store. The metadata name → digest map is part of the
	// record, so the API serves exactly what was signed.
	rev := Revision{
		PolicyID:              b.PolicyID,
		Platform:              b.Platform,
		Channel:               in.Channel,
		Sequence:              sequence,
		BundleSHA256:          bundleSHA256,
		ManifestSHA256:        manifestDigest,
		CanonicalPolicySHA256: b.CanonicalPolicySHA256,
		Metadata:              metadataDigests,
		IssuedAt:              s.now(),
	}
	if _, err := s.Store.InsertRevision(ctx, rev); err != nil {
		return PublicationResult{}, err
	}

	// 6. Promote: the one atomic pointer move. Everything above could crash
	// without a consumer noticing.
	if in.Promote {
		if err := s.Store.Promote(ctx, b.Platform, in.Channel, bundleSHA256); err != nil {
			return PublicationResult{}, err
		}
	}
	return PublicationResult{
		Revision: rev,
		Metadata: metadataDigests,
		Manifest: manifestBytes,
		Promoted: in.Promote,
		Sequence: sequence,
	}, nil
}

// MetadataFor serves one revision's signed metadata by name: the bytes the
// publisher recorded, content-addressed, never re-signed on the way out.
func (s *Service) MetadataFor(ctx context.Context, platform, channel, manifestSHA256, name string) ([]byte, bool, error) {
	if !IsMetadataName(name) {
		return nil, false, nil
	}
	rev, err := s.Store.RevisionByManifestDigest(ctx, platform, channel, manifestSHA256)
	if err != nil {
		return nil, false, err
	}
	digest, ok := rev.Metadata[name]
	if !ok {
		return nil, false, nil
	}
	data, err := s.Blobs.Get(ctx, digest)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// ResolveChannel answers a channel question with its promoted revision,
// refusing withdrawn content.
func (s *Service) ResolveChannel(ctx context.Context, platform, channel string) (Revision, error) {
	cur, err := s.Store.Channel(ctx, platform, channel)
	if err != nil {
		return Revision{}, err
	}
	rev, err := s.Store.RevisionByBundleDigest(ctx, platform, channel, cur.BundleSHA256)
	if err != nil {
		return Revision{}, err
	}
	if rev.WithdrawnAt != nil {
		return Revision{}, ErrWithdrawn
	}
	return rev, nil
}

type signInput struct {
	sequence      int64
	manifestBytes []byte
	bundle        *policy.Bundle
	channel       string
}

func (s *Service) signAndStore(ctx context.Context, in signInput) (map[string]string, error) {
	custom, err := TargetCustomBytes(in.bundle.PolicyID, in.bundle.Platform, in.channel, in.sequence, in.bundle.CanonicalPolicySHA256, in.bundle.RequiredCapabilities)
	if err != nil {
		return nil, err
	}
	meta, err := s.Publisher.Publish(Publication{
		Sequence:     in.sequence,
		Manifest:     in.manifestBytes,
		Custom:       custom,
		ExpiryAnchor: s.now(),
	})
	if err != nil {
		return nil, err
	}
	digests := make(map[string]string, len(meta))
	for _, name := range MetadataNames {
		digest, err := s.Blobs.Put(ctx, meta[name])
		if err != nil {
			return nil, fmt.Errorf("registry: store %s: %w", name, err)
		}
		digests[name] = digest
	}
	return digests, nil
}

// Rollback answers a bad publication the only safe way (POL-007): reissue
// the last good semantic content at a higher sequence. The bundle and
// manifest both carry publication identity, so they must be rebuilt rather
// than paired with fresh metadata under their old sequence. Nothing is
// rewritten; the audit trail keeps every revision, and the channel pointer
// moves only when the reissue is complete.
func (s *Service) Rollback(ctx context.Context, platform, channel string) (PublicationResult, error) {
	cur, err := s.Store.Channel(ctx, platform, channel)
	if err != nil {
		return PublicationResult{}, err
	}
	lastGood, err := s.Store.RevisionByBundleDigest(ctx, platform, channel, cur.BundleSHA256)
	if err != nil {
		return PublicationResult{}, err
	}
	if lastGood.WithdrawnAt != nil {
		return PublicationResult{}, ErrWithdrawn
	}
	manifestBytes, err := s.Blobs.Get(ctx, lastGood.ManifestSHA256)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("registry: last good manifest unavailable: %w", err)
	}
	bundleBytes, err := s.Blobs.Get(ctx, lastGood.BundleSHA256)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("registry: last good bundle unavailable: %w", err)
	}
	b, err := policy.DecodeBundle(bundleBytes)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	oldManifest, err := policy.DecodeManifest(manifestBytes)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	if err := policy.ValidateBundleCoherence(b, oldManifest); err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	sequence, err := s.Store.NextSequence(ctx, platform, channel)
	if err != nil {
		return PublicationResult{}, err
	}
	b.Sequence = sequence
	b.IssuedAt = s.now().UTC()
	if err := b.Validate(envFor(channel)); err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	bundleBytes, err = policy.MarshalBundle(b)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	payloads := []policy.PayloadInput{{
		Path: policy.PolicyPayloadPath, MediaType: policy.MediaTypeJSON, Bytes: bundleBytes,
	}}
	for _, old := range oldManifest.Payloads {
		if old.Path == policy.PolicyPayloadPath {
			continue
		}
		data, err := s.Blobs.Get(ctx, old.SHA256)
		if err != nil {
			return PublicationResult{}, fmt.Errorf("registry: last good payload %s unavailable: %w", old.Path, err)
		}
		payloads = append(payloads, policy.PayloadInput{Path: old.Path, MediaType: old.MediaType, Bytes: data})
	}
	manifest, err := policy.BuildManifest(b.PolicyID, b.Platform, b.Channel, sequence, b.RequiredCapabilities, payloads)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	if err := policy.ValidateBundleCoherence(b, manifest); err != nil {
		return PublicationResult{}, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	manifestBytes, err = json.Marshal(manifest)
	if err != nil {
		return PublicationResult{}, err
	}
	bundleSHA256, err := s.Blobs.Put(ctx, bundleBytes)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("registry: store rollback policy: %w", err)
	}
	manifestSHA256, err := s.Blobs.Put(ctx, manifestBytes)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("registry: store rollback manifest: %w", err)
	}
	metadataDigests, err := s.signAndStore(ctx, signInput{
		sequence:      sequence,
		manifestBytes: manifestBytes,
		bundle:        b,
		channel:       channel,
	})
	if err != nil {
		return PublicationResult{}, err
	}
	rev := Revision{
		PolicyID:              lastGood.PolicyID,
		Platform:              platform,
		Channel:               channel,
		Sequence:              sequence,
		BundleSHA256:          bundleSHA256,
		ManifestSHA256:        manifestSHA256,
		CanonicalPolicySHA256: lastGood.CanonicalPolicySHA256,
		Metadata:              metadataDigests,
		IssuedAt:              b.IssuedAt,
	}
	if _, err := s.Store.InsertRevision(ctx, rev); err != nil {
		return PublicationResult{}, err
	}
	if err := s.Store.Promote(ctx, platform, channel, bundleSHA256); err != nil {
		return PublicationResult{}, err
	}
	return PublicationResult{
		Revision: rev,
		Metadata: metadataDigests,
		Manifest: manifestBytes,
		Promoted: true,
		Sequence: sequence,
	}, nil
}

// OpenBuildSession exchanges a verified CI assertion plus a client hash for
// a short-lived build credential (AUTH-001/002). The hash is a lookup key;
// the CI assertion is what authorizes. Unknown clients get the same answer
// as unauthorized ones: nothing about the registration leaks.
func (s *Service) OpenBuildSession(ctx context.Context, clientHash, platform, channel string, ci *CIAssertion) (string, BuildSession, error) {
	client, err := s.Store.ClientByHash(ctx, clientHash)
	if err != nil {
		return "", BuildSession{}, ErrNotPermitted
	}
	if err := authorizeClient(client, platform, channel); err != nil {
		return "", BuildSession{}, err
	}
	if err := ci.Authorize(client); err != nil {
		return "", BuildSession{}, err
	}
	raw, err := randomToken()
	if err != nil {
		return "", BuildSession{}, err
	}
	now := s.now()
	sess := BuildSession{
		ID:                 randomID(),
		Digest:             TokenDigest(raw),
		ClientID:           client.ID,
		ClientHash:         client.ClientHash,
		Platform:           platform,
		Channel:            channel,
		ClientEpochAtIssue: client.RevocationEpoch,
		IssuedAt:           now,
		ExpiresAt:          now.Add(BuildSessionTTL),
	}
	if err := s.Store.CreateBuildSession(ctx, sess); err != nil {
		return "", BuildSession{}, err
	}
	return raw, sess, nil
}

// AuthorizeSession re-checks a presented bearer on every request (AUTH-002):
// expiry, revocation and the client's current registration state all apply
// now, not just at issuance.
func (s *Service) AuthorizeSession(ctx context.Context, rawToken string) (BuildSession, Client, error) {
	sess, err := s.Store.BuildSessionByDigest(ctx, TokenDigest(rawToken))
	if err != nil {
		return BuildSession{}, Client{}, ErrNotPermitted
	}
	now := s.now()
	if now.After(sess.ExpiresAt) || sess.RevokedAt != nil {
		return BuildSession{}, Client{}, ErrNotPermitted
	}
	client, err := s.Store.ClientByHash(ctx, sess.ClientHash)
	if err != nil {
		return BuildSession{}, Client{}, ErrNotPermitted
	}
	if client.RevocationEpoch > sess.ClientEpochAtIssue {
		// Suspended or revoked after this session was issued: the session
		// dies here, within the 60-second revocation target on every
		// state-reading path.
		return BuildSession{}, Client{}, ErrNotPermitted
	}
	if client.Status == ClientSuspended || client.Status == ClientRevoked {
		return BuildSession{}, Client{}, ErrNotPermitted
	}
	return sess, client, nil
}

func authorizeClient(c Client, platform, channel string) error {
	switch c.Status {
	case ClientProductionApproved, ClientStagingApproved:
	case ClientSuspended, ClientRevoked:
		return ErrNotPermitted
	default:
		return ErrNotPermitted
	}
	if !contains(c.Platforms, platform) {
		return ErrNotPermitted
	}
	if !contains(c.Channels, channel) {
		return ErrNotPermitted
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func bundleDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func envFor(channel string) policy.Environment {
	if channel == "staging" {
		return policy.EnvNonproduction
	}
	return policy.EnvProduction
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "opb_" + hex.EncodeToString(b), nil
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
