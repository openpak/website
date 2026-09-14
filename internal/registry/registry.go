// Package registry owns revisions and publication of the network policy
// bundles (PRD universal-tls-forwarding/01, gate G1): certified client
// registrations and their build sessions, immutable content-addressed bundle
// storage, transactionally allocated monotonic sequences per platform/channel,
// atomic channel promotion and higher-sequence rollback, and the signed TUF
// metadata that binds it all.
//
// Ownership rules (POL-001): routing and platform metadata stay in
// website/internal/netprofile; this package never edits host data. It stores
// and serves what internal/policy generated and validated.
//
// Storage follows the website's own pattern: a Store interface with an
// in-memory implementation for tests and a Postgres implementation with
// embedded migrations; CI runs the memory path, and the PG path is exercised
// when REGISTRY_TEST_DATABASE_URL points at a database.
package registry

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors handlers map onto HTTP statuses. ErrNotFound is deliberately
// vague: an absent object an unauthorized caller asks about reveals nothing.
var (
	ErrNotFound      = errors.New("registry: not found")
	ErrConflict      = errors.New("registry: conflict")
	ErrWithdrawn     = errors.New("registry: bundle withdrawn")
	ErrUnverified    = errors.New("registry: publication failed verification")
	ErrNotPermitted  = errors.New("registry: not permitted for this registration")
	ErrAlreadyExists = errors.New("registry: already exists")
)

// Client lifecycle states (AUTH-004). Only production-approved
// registrations fetch production bundles.
const (
	ClientRequested          = "requested"
	ClientReviewing          = "reviewing"
	ClientStagingApproved    = "staging-approved"
	ClientProductionApproved = "production-approved"
	ClientSuspended          = "suspended"
	ClientRevoked            = "revoked"
)

// ClientEnvironments.
const (
	EnvProduction    = "production"
	EnvNonproduction = "nonproduction"
)

// BuildSessionTTL is the proposed maximum build credential lifetime
// (AUTH-002): a release job authenticates, resolves, fetches and gets out.
const BuildSessionTTL = 10 * time.Minute

// Client is one certified registration (02's identity model, the fields the
// distribution service enforces). The client hash is a public lookup
// identifier — the SHA-256 of a server-issued value — never a credential.
type Client struct {
	ID         string
	ClientHash string // lowercase hex sha256, unique
	Repository string // immutable identity, e.g. "openpak/citron"
	Kind       string // emulator | setup | service
	Status     string
	// Environment scopes the registration: production-approved clients fetch
	// production bundles; staging access is a nonproduction registration.
	Environment string
	Platforms   []string
	Channels    []string
	// Workflows pins the approved CI workflow paths (AUTH-001: an approved
	// workflow, protected environment/ref). Empty means review has not yet
	// pinned one, and a production registration without one must not pass
	// review — the distribution service only enforces what is recorded.
	Workflows []string
	// RevocationEpoch bumps on suspension/revocation; sessions issued before
	// the epoch die on next use without waiting for their TTL.
	RevocationEpoch int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// BuildSession is one short-lived build credential: the bearer a release job
// holds while resolving and fetching one bundle. Digest is the SHA-256 of the
// bearer token; the raw token is never stored. ClientEpochAtIssue snapshots
// the client's revocation epoch at issuance, so a suspension or revocation
// after issuance kills the session on its next request (AUTH-006).
type BuildSession struct {
	ID                 string
	Digest             string
	ClientID           string
	ClientHash         string
	Platform           string
	Channel            string
	ClientEpochAtIssue int64
	IssuedAt           time.Time
	ExpiresAt          time.Time
	RevokedAt          *time.Time
}

// Revision is one published bundle of a platform/channel: the bytes are
// immutable and content-addressed; only the sequence moves forward. The
// Metadata map records the signed TUF metadata of the publication by file
// name, so the API serves exactly what was signed, by name.
type Revision struct {
	PolicyID              string
	Platform              string
	Channel               string
	Sequence              int64
	BundleSHA256          string // the policy bundle bytes
	ManifestSHA256        string // the signed target manifest bytes
	CanonicalPolicySHA256 string
	Metadata              map[string]string // name → blob digest
	IssuedAt              time.Time
	WithdrawnAt           *time.Time
}

// ChannelState is the channel pointer: what a channel resolution answers
// with. Promotion swaps it atomically; rollback publishes a higher sequence
// referencing the last good content and moves the pointer there.
type ChannelState struct {
	Platform     string
	Channel      string
	BundleSHA256 string
	Sequence     int64
	UpdatedAt    time.Time
}

// Store persists registrations, sessions, revisions and channel pointers.
// Implementations: memory.go (tests) and pg.go (production).
type Store interface {
	// Clients (02).
	PutClient(ctx context.Context, c Client) error
	ClientByHash(ctx context.Context, clientHash string) (Client, error)
	SetClientStatus(ctx context.Context, clientID, status string) error
	// Build sessions.
	CreateBuildSession(ctx context.Context, s BuildSession) error
	BuildSessionByDigest(ctx context.Context, tokenDigest string) (BuildSession, error)
	RevokeBuildSession(ctx context.Context, sessionID string) error
	MarkSessionResolved(ctx context.Context, sessionID, manifestSHA256 string) error
	SessionResolution(ctx context.Context, sessionID string) (string, error)
	// Revisions and channel pointers.
	// NextSequence allocates the next monotonic sequence for the
	// platform/channel, transactionally, surviving restarts. An allocation
	// whose publication later fails burns the number: the sequence moves
	// forward or not at all, and is never reused.
	NextSequence(ctx context.Context, platform, channel string) (int64, error)
	// InsertRevision records one verified revision under the sequence the
	// service allocated. The bytes themselves are already in the blob
	// store; the row is the audit and lookup record.
	InsertRevision(ctx context.Context, r Revision) (int64, error)
	RevisionByBundleDigest(ctx context.Context, platform, channel, bundleSHA256 string) (Revision, error)
	RevisionByManifestDigest(ctx context.Context, platform, channel, manifestSHA256 string) (Revision, error)
	Revision(ctx context.Context, platform, channel string, sequence int64) (Revision, error)
	Promote(ctx context.Context, platform, channel, bundleSHA256 string) error
	Channel(ctx context.Context, platform, channel string) (ChannelState, error)
	// Withdraw marks every revision carrying the bundle digest withdrawn;
	// fetches answer 410 afterwards. Bytes stay for audit (AUTH-006: already
	// distributed public bytes cannot be recalled).
	Withdraw(ctx context.Context, bundleSHA256 string) error

	Close() error
}
