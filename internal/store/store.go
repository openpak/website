// Package store owns the website's own Postgres tables: site config, mobile
// API tokens, admin audit trail and media metadata. Account data lives in
// the core; this store never duplicates it (except opaque account ids).
package store

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// SiteConfig is the operator-controlled site configuration (WEB-07).
type SiteConfig struct {
	RegistrationsOpen bool
	BannerMessage     string // empty = no banner
}

// APIToken is a mobile bearer token bound to a core web session.
type APIToken struct {
	ID            string
	AccountID     string
	CoreSessionID string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

// AuditEntry records an operator/moderator action (website-side audit).
type AuditEntry struct {
	ID        int64
	ActorID   string
	Action    string
	Detail    string
	CreatedAt time.Time
}

// MediaRecord is metadata for an uploaded avatar.
type MediaRecord struct {
	ID        string
	AccountID string
	Kind      string // "avatar"
	UpdatedAt time.Time
}

// WebSession is the website's session index: the core web session id it
// wrapped in its cookie, mapped to the account it was minted for. Account
// facts come from the core; this only records ids we issued.
type WebSession struct {
	CoreSessionID string
	AccountID     string
	Email         string
	DisplayName   string
	CreatedAt     time.Time
}

// Store is the interface handlers depend on (faked in tests).
type Store interface {
	PutWebSession(ctx context.Context, s WebSession) error
	GetWebSession(ctx context.Context, coreSessionID string) (WebSession, error)
	DeleteWebSession(ctx context.Context, coreSessionID string) error
	DeleteWebSessionsForAccount(ctx context.Context, accountID string) error

	Ping(ctx context.Context) error
	GetSiteConfig(ctx context.Context) (SiteConfig, error)
	SetSiteConfig(ctx context.Context, cfg SiteConfig) error

	CreateAPIToken(ctx context.Context, id, tokenHash, accountID, coreSessionID string, expiresAt time.Time) error
	APITokenByHash(ctx context.Context, hash string) (APIToken, error)
	RevokeAPIToken(ctx context.Context, id string) error
	TouchAPIToken(ctx context.Context, id string) error
	RevokeAPITokensForAccount(ctx context.Context, accountID string) error

	AddAudit(ctx context.Context, actorID, action, detail string) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)

	UpsertMedia(ctx context.Context, m MediaRecord) error
	GetMedia(ctx context.Context, accountID, kind string) (MediaRecord, error)

	ListRequests(ctx context.Context, kind, viewerAccountID string) ([]Request, error)
	SeedRequests(ctx context.Context, reqs []Request) error
	ToggleVote(ctx context.Context, requestID int64, accountID string) (bool, error)
	RequestVotes(ctx context.Context, requestID int64, accountID string) (votes int, voted bool, err error)

	SavePushToken(ctx context.Context, accountID, token, platform string) error
	DeletePushToken(ctx context.Context, token string) error
	PushTokensForAccount(ctx context.Context, accountID string) ([]PushToken, error)
	// PushCursor/SetPushCursor persist how far the notifier has read the core's
	// event stream, so a restart resumes instead of replaying or skipping.
	PushCursor(ctx context.Context) (uint64, error)
	SetPushCursor(ctx context.Context, version uint64) error
}

// PushToken is one app install that can be notified.
type PushToken struct {
	Token    string
	Platform string // "fcm" today; the column exists so a second transport needs no migration
}

// PG is the Postgres-backed Store.
type PG struct {
	pool *pgxpool.Pool
}

// New opens the pool and applies migrations.
func New(ctx context.Context, databaseURL string) (*PG, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s := &PG{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *PG) Close() { s.pool.Close() }

// migrate applies embedded migrations in filename order, tracked in
// schema_migrations. Intentionally minimal: one table of applied versions.
func (s *PG) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	// Sorted: 0004 rewrites a constraint 0003 creates, so map order will not do.
	for _, name := range slices.Sorted(maps.Keys(migrations)) {
		sql := migrations[name]
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
