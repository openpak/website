package registry

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// The metadata security model is TUF (POL-006): signed roles, artifact
// verification, rollback and freeze defenses. Recorded profile, per the
// kickoff record: spec version 1.0.31 via go-tuf v2; Ed25519 signatures;
// SHA-256 digests; threshold 1 per role; an offline root and one online key
// for targets/snapshot/timestamp; metadata versions follow the publication
// sequence, so a rollback publishes a higher version referencing the last
// good content and anti-rollback falls out of the sequence rule.
const (
	specVersion   = metadata.SPECIFICATION_VERSION
	rootExpiry    = 365 * 24 * time.Hour
	targetsExpiry = 90 * 24 * time.Hour
	// Timestamp expires fastest: a frozen timestamp is what stops clients
	// from seeing anything new, so it is the role that must rotate.
	snapshotExpiry = 30 * 24 * time.Hour
	timestampTTL   = 7 * 24 * time.Hour
)

// Publisher holds the signing identity of one publication environment and
// produces the signed metadata for each revision. The root key never signs
// anything but root; the online key signs targets, snapshot and timestamp.
type Publisher struct {
	rootBytes     []byte
	onlineKeyID   string
	onlineSigner  signature.Signer
	targetsPubKey *metadata.Key
}

// GenerateEd25519 makes a fresh key for tests and first-time provisioning.
func GenerateEd25519() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// SaveEd25519 writes the hex-encoded seed, a provisioning-file format we
// control end to end; 0o600 because it is a private key even where a
// private dir already protects it.
func SaveEd25519(path string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(priv.Seed())+"\n"), 0o600)
}

// LoadEd25519 reads a seed file written by SaveEd25519.
func LoadEd25519(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(trimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("registry: %s is not a hex ed25519 seed", path)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("registry: %s carries %d seed bytes, want %d", path, len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == '\n' || s[start] == ' ' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == '\n' || s[end-1] == ' ' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// NewPublisher builds root metadata from the two keys and signs it. The
// root bytes are fixed for the publisher's lifetime: rotating the root is a
// rehearsed recovery procedure, not a publication step. Provisioning note:
// the root expiry anchors to `now`, so a publisher built this way at every
// process start would mint a different root each boot — production wiring
// must provision the root once (ProvisionRoot) and load it with
// NewPublisherWithRoot.
func NewPublisher(rootKey, onlineKey ed25519.PrivateKey, now time.Time) (*Publisher, error) {
	rootBytes, err := ProvisionRoot(rootKey, onlineKey, now)
	if err != nil {
		return nil, err
	}
	return NewPublisherWithRoot(rootBytes, onlineKey)
}

// ProvisionRoot signs the root metadata once, at key provisioning time: the
// bytes a client pins must be the same for the root key's whole life, not a
// fresh expiry minted at every restart (POL-006).
func ProvisionRoot(rootKey, onlineKey ed25519.PrivateKey, now time.Time) ([]byte, error) {
	rootSigner, err := signature.LoadSignerVerifier(rootKey, crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("registry: root signer: %w", err)
	}
	rootPub, err := metadata.KeyFromPublicKey(rootKey.Public())
	if err != nil {
		return nil, fmt.Errorf("registry: root key: %w", err)
	}
	onlinePub, err := metadata.KeyFromPublicKey(onlineKey.Public())
	if err != nil {
		return nil, fmt.Errorf("registry: online key: %w", err)
	}

	root := metadata.Root(now.Add(rootExpiry))
	signed := root.Signed
	signed.SpecVersion = specVersion
	signed.ConsistentSnapshot = false
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		signed.Roles[role] = &metadata.Role{KeyIDs: []string{}, Threshold: 1}
	}
	// The root role trusts only the offline root key; everything the
	// publication process touches trusts the online key.
	if err := signed.AddKey(rootPub, "root"); err != nil {
		return nil, err
	}
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		if err := signed.AddKey(onlinePub, role); err != nil {
			return nil, err
		}
	}
	if _, err := root.Sign(rootSigner); err != nil {
		return nil, fmt.Errorf("registry: sign root: %w", err)
	}
	return root.ToBytes(false)
}

// NewPublisherWithRoot builds a publisher from already-provisioned root
// bytes and the online key: every publication serves exactly the root the
// operator provisioned, whatever process restarted when.
func NewPublisherWithRoot(rootBytes []byte, onlineKey ed25519.PrivateKey) (*Publisher, error) {
	onlineSigner, err := signature.LoadSignerVerifier(onlineKey, crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("registry: online signer: %w", err)
	}
	onlinePub, err := metadata.KeyFromPublicKey(onlineKey.Public())
	if err != nil {
		return nil, fmt.Errorf("registry: online key: %w", err)
	}
	onlineKeyID, err := onlinePub.ID()
	if err != nil {
		return nil, err
	}
	// The served root must at least parse and carry this online key, so a
	// mismatched provisioning file dies here rather than in a client pin.
	root, err := metadataFromRootBytes(rootBytes)
	if err != nil {
		return nil, err
	}
	// The served root must delegate to this online key, so a mismatched
	// provisioning file dies here rather than in a client pin.
	if !containsKeyID(root, onlineKeyID) {
		return nil, fmt.Errorf("registry: provisioned root does not delegate to the online key")
	}
	return &Publisher{
		rootBytes:     rootBytes,
		onlineKeyID:   onlineKeyID,
		onlineSigner:  onlineSigner,
		targetsPubKey: onlinePub,
	}, nil
}

// containsKeyID reports whether the root's targets role trusts the key id.
func containsKeyID(root *metadata.Metadata[metadata.RootType], keyID string) bool {
	role, ok := root.Signed.Roles["targets"]
	if !ok {
		return false
	}
	for _, id := range role.KeyIDs {
		if id == keyID {
			return true
		}
	}
	return false
}

// MetadataNames are the paths the /metadata/{name} endpoint serves; anything
// else is a 404 before storage is touched. IsMetadataName reports membership.
var MetadataNames = []string{"root.json", "targets.json", "snapshot.json", "timestamp.json"}

// IsMetadataName reports whether name is one of the declared metadata paths.
func IsMetadataName(name string) bool {
	for _, n := range MetadataNames {
		if n == name {
			return true
		}
	}
	return false
}

// Publication is one revision's signed metadata bundle. Version numbers
// equal the revision sequence: publication moves forward or not at all.
type Publication struct {
	Sequence int64
	Manifest []byte
	// Custom rides inside the signed targets: the revision facts a fetch
	// tool reads without trusting the API for them.
	Custom       json.RawMessage
	ExpiryAnchor time.Time
}

// Publish signs targets/snapshot/timestamp for one revision and returns the
// metadata bytes keyed by file name. root.json is not re-signed; it is
// served from NewPublisher's fixed bytes.
func (p *Publisher) Publish(in Publication) (map[string][]byte, error) {
	if in.Sequence < 1 {
		return nil, fmt.Errorf("registry: metadata version %d is not positive", in.Sequence)
	}
	now := in.ExpiryAnchor
	targets := metadata.Targets(now.Add(targetsExpiry))
	targets.Signed.SpecVersion = specVersion
	targets.Signed.Version = in.Sequence
	targets.Signed.Targets["manifest.json"] = &metadata.TargetFiles{
		Length: int64(len(in.Manifest)),
		Hashes: metadata.Hashes{"sha256": metadata.HexBytes(sha256Sum(in.Manifest))},
		Custom: &in.Custom,
	}
	if _, err := targets.Sign(p.onlineSigner); err != nil {
		return nil, fmt.Errorf("registry: sign targets: %w", err)
	}
	targetsBytes, err := targets.ToBytes(false)
	if err != nil {
		return nil, err
	}

	snapshot := metadata.Snapshot(now.Add(snapshotExpiry))
	snapshot.Signed.SpecVersion = specVersion
	snapshot.Signed.Version = in.Sequence
	snapshot.Signed.Meta["targets.json"] = &metadata.MetaFiles{
		Version: in.Sequence,
		Length:  int64(len(targetsBytes)),
		Hashes:  metadata.Hashes{"sha256": metadata.HexBytes(sha256Sum(targetsBytes))},
	}
	if _, err := snapshot.Sign(p.onlineSigner); err != nil {
		return nil, fmt.Errorf("registry: sign snapshot: %w", err)
	}
	snapshotBytes, err := snapshot.ToBytes(false)
	if err != nil {
		return nil, err
	}

	timestamp := metadata.Timestamp(now.Add(timestampTTL))
	timestamp.Signed.SpecVersion = specVersion
	timestamp.Signed.Version = in.Sequence
	timestamp.Signed.Meta["snapshot.json"] = &metadata.MetaFiles{
		Version: in.Sequence,
		Length:  int64(len(snapshotBytes)),
		Hashes:  metadata.Hashes{"sha256": metadata.HexBytes(sha256Sum(snapshotBytes))},
	}
	if _, err := timestamp.Sign(p.onlineSigner); err != nil {
		return nil, fmt.Errorf("registry: sign timestamp: %w", err)
	}
	timestampBytes, err := timestamp.ToBytes(false)
	if err != nil {
		return nil, err
	}

	return map[string][]byte{
		"root.json":      p.rootBytes,
		"targets.json":   targetsBytes,
		"snapshot.json":  snapshotBytes,
		"timestamp.json": timestampBytes,
	}, nil
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// TargetCustom is the machine-readable sidecar a fetch tool reads from the
// signed targets metadata: what revision this is and what a consumer must
// implement, bound by the signature rather than by trust in the API.
type TargetCustom struct {
	PolicyID              string   `json:"policy_id"`
	Platform              string   `json:"platform"`
	Channel               string   `json:"channel"`
	Sequence              int64    `json:"sequence"`
	CanonicalPolicySHA256 string   `json:"canonical_policy_sha256"`
	RequiredCapabilities  []string `json:"required_capabilities"`
}

// TargetCustomBytes builds the custom payload for one bundle.
func TargetCustomBytes(policyID, platform, channel string, sequence int64, canonical string, caps []string) (json.RawMessage, error) {
	return json.Marshal(TargetCustom{
		PolicyID: policyID, Platform: platform, Channel: channel,
		Sequence: sequence, CanonicalPolicySHA256: canonical, RequiredCapabilities: caps,
	})
}

// VerifyPublication is the verification chain the fetch tool runs, held in
// this package so tests and tooling exercise exactly one implementation:
// every role's signature against the pinned root, every expiry, snapshot vs
// targets version, timestamp vs snapshot version and hashes, and the
// manifest's length and digest inside the signed targets.
func VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, manifest []byte, now time.Time) error {
	root, err := metadataFromRootBytes(rootBytes)
	if err != nil {
		return err
	}
	targets, err := metadataFromTargetsBytes(targetsBytes)
	if err != nil {
		return err
	}
	snapshot, err := metadataFromSnapshotBytes(snapshotBytes)
	if err != nil {
		return err
	}
	timestamp, err := metadataFromTimestampBytes(timestampBytes)
	if err != nil {
		return err
	}

	// Freeze defenses first: nothing past its expiry is trusted, whatever
	// its signatures say.
	if err := expired(root.Signed.Expires, now, "root"); err != nil {
		return err
	}
	if err := expired(targets.Signed.Expires, now, "targets"); err != nil {
		return err
	}
	if err := expired(snapshot.Signed.Expires, now, "snapshot"); err != nil {
		return err
	}
	if err := expired(timestamp.Signed.Expires, now, "timestamp"); err != nil {
		return err
	}

	// Signature chain: each role's signatures are checked against the keys
	// and threshold the signed root delegates to it.
	if err := root.VerifyDelegate("targets", targets); err != nil {
		return fmt.Errorf("registry: targets not trusted by root: %w", err)
	}
	if err := root.VerifyDelegate("snapshot", snapshot); err != nil {
		return fmt.Errorf("registry: snapshot not trusted by root: %w", err)
	}
	if err := root.VerifyDelegate("timestamp", timestamp); err != nil {
		return fmt.Errorf("registry: timestamp not trusted by root: %w", err)
	}

	// Rollback and freeze defenses inside the chain: snapshot must describe
	// exactly this targets version; timestamp must describe exactly this
	// snapshot, byte for byte.
	tm, ok := snapshot.Signed.Meta["targets.json"]
	if !ok || tm.Version != targets.Signed.Version {
		return fmt.Errorf("registry: snapshot does not describe the targets version")
	}
	if err := checkMetaBytes(tm, targetsBytes); err != nil {
		return fmt.Errorf("registry: snapshot targets hashes: %w", err)
	}
	sm, ok := timestamp.Signed.Meta["snapshot.json"]
	if !ok || sm.Version != snapshot.Signed.Version {
		return fmt.Errorf("registry: timestamp does not describe the snapshot version")
	}
	if err := checkMetaBytes(sm, snapshotBytes); err != nil {
		return fmt.Errorf("registry: timestamp snapshot hashes: %w", err)
	}

	// The manifest — which binds every payload — must be the signed target.
	tf, ok := targets.Signed.Targets["manifest.json"]
	if !ok {
		return fmt.Errorf("registry: targets carries no manifest target")
	}
	if err := tf.VerifyLengthHashes(manifest); err != nil {
		return fmt.Errorf("registry: manifest does not match the signed target: %w", err)
	}
	return nil
}

func checkMetaBytes(m *metadata.MetaFiles, data []byte) error {
	if m.Length != int64(len(data)) {
		return fmt.Errorf("length %d, want %d", m.Length, len(data))
	}
	sum := sha256.Sum256(data)
	want, ok := m.Hashes["sha256"]
	if !ok {
		return fmt.Errorf("no sha256 hash recorded")
	}
	if string(want) != string(sum[:]) {
		return fmt.Errorf("sha256 mismatch")
	}
	return nil
}

func metadataFromRootBytes(data []byte) (*metadata.Metadata[metadata.RootType], error) {
	m, err := fromBytes[metadata.RootType](data)
	if err != nil {
		return nil, fmt.Errorf("registry: root metadata: %w", err)
	}
	return m, nil
}

func metadataFromTargetsBytes(data []byte) (*metadata.Metadata[metadata.TargetsType], error) {
	m, err := fromBytes[metadata.TargetsType](data)
	if err != nil {
		return nil, fmt.Errorf("registry: targets metadata: %w", err)
	}
	return m, nil
}

func metadataFromSnapshotBytes(data []byte) (*metadata.Metadata[metadata.SnapshotType], error) {
	m, err := fromBytes[metadata.SnapshotType](data)
	if err != nil {
		return nil, fmt.Errorf("registry: snapshot metadata: %w", err)
	}
	return m, nil
}

func metadataFromTimestampBytes(data []byte) (*metadata.Metadata[metadata.TimestampType], error) {
	m, err := fromBytes[metadata.TimestampType](data)
	if err != nil {
		return nil, fmt.Errorf("registry: timestamp metadata: %w", err)
	}
	return m, nil
}

func fromBytes[T metadata.Roles](data []byte) (*metadata.Metadata[T], error) {
	m := &metadata.Metadata[T]{}
	if _, err := m.FromBytes(data); err != nil {
		return nil, err
	}
	return m, nil
}

func expired(when time.Time, now time.Time, role string) error {
	if !when.After(now) {
		return fmt.Errorf("registry: %s metadata expired", role)
	}
	return nil
}
