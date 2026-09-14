package registry

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PG is the Postgres-backed Store. Its tables are prefixed registry_ and its
// migrations are recorded in the website's schema_migrations table under
// registry_-prefixed versions, so one database hosts both stores without
// either stepping on the other.
type PG struct {
	pool *pgxpool.Pool
}

// OpenPG opens the pool and applies the registry migrations.
func OpenPG(ctx context.Context, databaseURL string) (*PG, error) {
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
func (s *PG) Close() error { s.pool.Close(); return nil }

func (s *PG) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(pgMigrations)) {
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
		if _, err := tx.Exec(ctx, pgMigrations[name]); err != nil {
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

var pgMigrations = map[string]string{"registry_0001_identity": `
		CREATE TABLE IF NOT EXISTS registry_clients (
			id text PRIMARY KEY,
			client_hash text NOT NULL UNIQUE,
			repository text NOT NULL,
			kind text NOT NULL DEFAULT 'emulator',
			status text NOT NULL,
			environment text NOT NULL DEFAULT 'production',
			platforms text[] NOT NULL DEFAULT '{}',
			channels text[] NOT NULL DEFAULT '{}',
			revocation_epoch bigint NOT NULL DEFAULT 0,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS registry_build_sessions (
			id text PRIMARY KEY,
			token_digest text NOT NULL UNIQUE,
			client_id text NOT NULL,
			client_hash text NOT NULL,
			platform text NOT NULL,
			channel text NOT NULL,
			client_epoch_at_issue bigint NOT NULL DEFAULT 0,
			issued_at timestamptz NOT NULL DEFAULT now(),
			expires_at timestamptz NOT NULL,
			revoked_at timestamptz,
			resolved_manifest_sha256 text NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS registry_sessions_client_idx
			ON registry_build_sessions(client_id);
	`,
	"registry_0002_revisions": `
		CREATE TABLE IF NOT EXISTS registry_revisions (
			policy_id text NOT NULL,
			platform text NOT NULL,
			channel text NOT NULL,
			sequence bigint NOT NULL,
			bundle_sha256 text NOT NULL,
			manifest_sha256 text NOT NULL,
			canonical_policy_sha256 text NOT NULL DEFAULT '',
			issued_at timestamptz NOT NULL DEFAULT now(),
			withdrawn_at timestamptz,
			PRIMARY KEY (platform, channel, sequence)
		);
		CREATE INDEX IF NOT EXISTS registry_revisions_bundle_idx
			ON registry_revisions(platform, channel, bundle_sha256);

		CREATE TABLE IF NOT EXISTS registry_channels (
			platform text NOT NULL,
			channel text NOT NULL,
			bundle_sha256 text NOT NULL,
			sequence bigint NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (platform, channel)
		);
	`,
	"registry_0003_sequences": `
		CREATE TABLE IF NOT EXISTS registry_sequences (
			platform text NOT NULL,
			channel text NOT NULL,
			last_sequence bigint NOT NULL DEFAULT 0,
			PRIMARY KEY (platform, channel)
		);
		-- Seed from anything already recorded as revisions, so enabling the
		-- counter on an existing registry never rewinds it.
		INSERT INTO registry_sequences (platform, channel, last_sequence)
		SELECT platform, channel, COALESCE(MAX(sequence), 0)
		FROM registry_revisions GROUP BY platform, channel
		ON CONFLICT (platform, channel) DO NOTHING;

		ALTER TABLE registry_revisions ADD COLUMN IF NOT EXISTS metadata jsonb NOT NULL DEFAULT '{}';
	`,
	"registry_0004_workflows": `
		ALTER TABLE registry_clients ADD COLUMN IF NOT EXISTS workflows text[] NOT NULL DEFAULT '{}';
	`,
}

const clientColumns = `id, client_hash, repository, kind, status, environment,
	platforms, channels, workflows, revocation_epoch, created_at, updated_at`

func scanClient(row pgx.Row) (Client, error) {
	var c Client
	err := row.Scan(&c.ID, &c.ClientHash, &c.Repository, &c.Kind, &c.Status, &c.Environment,
		&c.Platforms, &c.Channels, &c.Workflows, &c.RevocationEpoch, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Client{}, err
	}
	return c, nil
}

func (s *PG) PutClient(ctx context.Context, c Client) error {
	// An empty scope is a fact (review has not pinned one yet), not an
	// absence: the columns are NOT NULL, so nil slices must go out as empty
	// arrays rather than NULLs.
	if c.Platforms == nil {
		c.Platforms = []string{}
	}
	if c.Channels == nil {
		c.Channels = []string{}
	}
	if c.Workflows == nil {
		c.Workflows = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO registry_clients (`+clientColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE($11, now()),now())
		ON CONFLICT (id) DO UPDATE SET repository=EXCLUDED.repository, kind=EXCLUDED.kind,
			status=EXCLUDED.status, environment=EXCLUDED.environment, platforms=EXCLUDED.platforms,
			channels=EXCLUDED.channels, workflows=EXCLUDED.workflows, updated_at=now()`,
		c.ID, c.ClientHash, c.Repository, c.Kind, c.Status, c.Environment,
		c.Platforms, c.Channels, c.Workflows, c.RevocationEpoch, nilOrTime(c.CreatedAt))
	return err
}

func nilOrTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (s *PG) ClientByHash(ctx context.Context, clientHash string) (Client, error) {
	c, err := scanClient(s.pool.QueryRow(ctx, `SELECT `+clientColumns+`
		FROM registry_clients WHERE client_hash=$1`, clientHash))
	if err == pgx.ErrNoRows {
		return Client{}, ErrNotFound
	}
	return c, err
}

func (s *PG) SetClientStatus(ctx context.Context, clientID, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE registry_clients SET status=$2,
		revocation_epoch = revocation_epoch + CASE WHEN $2 IN ('suspended','revoked') THEN 1 ELSE 0 END,
		updated_at=now() WHERE id=$1`, clientID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const sessionColumns = `id, token_digest, client_id, client_hash, platform, channel,
	client_epoch_at_issue, issued_at, expires_at, revoked_at`

func scanSession(row pgx.Row) (BuildSession, error) {
	var s BuildSession
	err := row.Scan(&s.ID, &s.Digest, &s.ClientID, &s.ClientHash, &s.Platform, &s.Channel,
		&s.ClientEpochAtIssue, &s.IssuedAt, &s.ExpiresAt, &s.RevokedAt)
	if err != nil {
		return BuildSession{}, err
	}
	return s, nil
}

func (s *PG) CreateBuildSession(ctx context.Context, sess BuildSession) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO registry_build_sessions (`+sessionColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		sess.ID, sess.Digest, sess.ClientID, sess.ClientHash, sess.Platform, sess.Channel,
		sess.ClientEpochAtIssue, sess.IssuedAt, sess.ExpiresAt, sess.RevokedAt)
	return err
}

func (s *PG) BuildSessionByDigest(ctx context.Context, tokenDigest string) (BuildSession, error) {
	sess, err := scanSession(s.pool.QueryRow(ctx, `SELECT `+sessionColumns+`
		FROM registry_build_sessions WHERE token_digest=$1`, tokenDigest))
	if err == pgx.ErrNoRows {
		return BuildSession{}, ErrNotFound
	}
	return sess, err
}

func (s *PG) RevokeBuildSession(ctx context.Context, sessionID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE registry_build_sessions
		SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, sessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) MarkSessionResolved(ctx context.Context, sessionID, manifestSHA256 string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE registry_build_sessions
		SET resolved_manifest_sha256=$2 WHERE id=$1`, sessionID, manifestSHA256)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) SessionResolution(ctx context.Context, sessionID string) (string, error) {
	var digest string
	err := s.pool.QueryRow(ctx, `SELECT resolved_manifest_sha256
		FROM registry_build_sessions WHERE id=$1`, sessionID).Scan(&digest)
	if err == pgx.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if digest == "" {
		return "", ErrNotFound
	}
	return digest, nil
}

// NextSequence allocates under a row lock: the UPDATE serializes concurrent
// publications per platform/channel, the RETURNING hands out distinct
// numbers, and the counter survives restarts. A publication that fails after
// this point burns the number — sequences move forward or not at all.
func (s *PG) NextSequence(ctx context.Context, platform, channel string) (int64, error) {
	var seq int64
	err := s.pool.QueryRow(ctx, `INSERT INTO registry_sequences (platform, channel, last_sequence)
		VALUES ($1, $2, 1)
		ON CONFLICT (platform, channel) DO UPDATE
			SET last_sequence = registry_sequences.last_sequence + 1
		RETURNING last_sequence`, platform, channel).Scan(&seq)
	return seq, err
}

func (s *PG) InsertRevision(ctx context.Context, r Revision) (int64, error) {
	// A bundle once withdrawn never comes back, not even as a "new"
	// revision of the same bytes.
	var withdrawn bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registry_revisions
		WHERE platform=$1 AND channel=$2 AND bundle_sha256=$3 AND withdrawn_at IS NOT NULL)`,
		r.Platform, r.Channel, r.BundleSHA256).Scan(&withdrawn)
	if err != nil {
		return 0, err
	}
	if withdrawn {
		return 0, ErrWithdrawn
	}
	if r.Sequence < 1 {
		return 0, ErrConflict
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO registry_revisions
		(policy_id, platform, channel, sequence, bundle_sha256, manifest_sha256, canonical_policy_sha256, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING sequence`,
		r.PolicyID, r.Platform, r.Channel, r.Sequence, r.BundleSHA256, r.ManifestSHA256, r.CanonicalPolicySHA256, r.Metadata,
	).Scan(&r.Sequence)
	if isUniqueViolation(err) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	return r.Sequence, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	if e, ok := err.(interface{ SQLState() string }); ok {
		pgErr = e
		return pgErr.SQLState() == "23505"
	}
	return false
}

const revisionColumns = `policy_id, platform, channel, sequence, bundle_sha256,
	manifest_sha256, canonical_policy_sha256, metadata, issued_at, withdrawn_at`

func scanRevision(row pgx.Row) (Revision, error) {
	var r Revision
	err := row.Scan(&r.PolicyID, &r.Platform, &r.Channel, &r.Sequence, &r.BundleSHA256,
		&r.ManifestSHA256, &r.CanonicalPolicySHA256, &r.Metadata, &r.IssuedAt, &r.WithdrawnAt)
	if err != nil {
		return Revision{}, err
	}
	return r, err
}

func (s *PG) RevisionByBundleDigest(ctx context.Context, platform, channel, bundleSHA256 string) (Revision, error) {
	r, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionColumns+`
		FROM registry_revisions WHERE platform=$1 AND channel=$2 AND bundle_sha256=$3
		ORDER BY sequence DESC LIMIT 1`, platform, channel, bundleSHA256))
	if err == pgx.ErrNoRows {
		return Revision{}, ErrNotFound
	}
	return r, err
}

func (s *PG) RevisionByManifestDigest(ctx context.Context, platform, channel, manifestSHA256 string) (Revision, error) {
	r, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionColumns+`
		FROM registry_revisions WHERE platform=$1 AND channel=$2 AND manifest_sha256=$3
		ORDER BY sequence DESC LIMIT 1`, platform, channel, manifestSHA256))
	if err == pgx.ErrNoRows {
		return Revision{}, ErrNotFound
	}
	return r, err
}

func (s *PG) Revision(ctx context.Context, platform, channel string, sequence int64) (Revision, error) {
	r, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionColumns+`
		FROM registry_revisions WHERE platform=$1 AND channel=$2 AND sequence=$3`,
		platform, channel, sequence))
	if err == pgx.ErrNoRows {
		return Revision{}, ErrNotFound
	}
	return r, err
}

func (s *PG) Promote(ctx context.Context, platform, channel, bundleSHA256 string) error {
	tag, err := s.pool.Exec(ctx, `INSERT INTO registry_channels
		(platform, channel, bundle_sha256, sequence, updated_at)
		SELECT $1, $2, $3, r.sequence, now()
		FROM registry_revisions r
		WHERE r.platform=$1 AND r.channel=$2 AND r.bundle_sha256=$3 AND r.withdrawn_at IS NULL
		ORDER BY r.sequence DESC LIMIT 1
		ON CONFLICT (platform, channel) DO UPDATE
			SET bundle_sha256=EXCLUDED.bundle_sha256, sequence=EXCLUDED.sequence, updated_at=now()`,
		platform, channel, bundleSHA256)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) Channel(ctx context.Context, platform, channel string) (ChannelState, error) {
	var c ChannelState
	err := s.pool.QueryRow(ctx, `SELECT platform, channel, bundle_sha256, sequence, updated_at
		FROM registry_channels WHERE platform=$1 AND channel=$2`, platform, channel).
		Scan(&c.Platform, &c.Channel, &c.BundleSHA256, &c.Sequence, &c.UpdatedAt)
	if err == pgx.ErrNoRows {
		return ChannelState{}, ErrNotFound
	}
	return c, err
}

func (s *PG) Withdraw(ctx context.Context, bundleSHA256 string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE registry_revisions SET withdrawn_at=now()
		WHERE bundle_sha256=$1 AND withdrawn_at IS NULL`, bundleSHA256)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
