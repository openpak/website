package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// migrations is embedded SQL applied at boot. Keyed by ordered version name.
var migrations = map[string]string{
	"0001_site": `CREATE TABLE IF NOT EXISTS site_config (
		key text PRIMARY KEY,
		value text NOT NULL
	);
	INSERT INTO site_config(key, value) VALUES ('registrations_open','true'), ('banner_message','')
		ON CONFLICT (key) DO NOTHING;

	CREATE TABLE IF NOT EXISTS web_sessions (
		core_session_id text PRIMARY KEY,
		account_id text NOT NULL,
		email text NOT NULL DEFAULT '',
		display_name text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS web_sessions_account_idx ON web_sessions(account_id);

	CREATE TABLE IF NOT EXISTS api_tokens (
		id text PRIMARY KEY,
		token_hash text NOT NULL UNIQUE,
		account_id text NOT NULL,
		core_session_id text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		expires_at timestamptz NOT NULL,
		revoked_at timestamptz,
		last_seen_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS api_tokens_account_idx ON api_tokens(account_id);

	CREATE TABLE IF NOT EXISTS admin_audit (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		actor_id text NOT NULL,
		action text NOT NULL,
		detail text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS media (
		account_id text NOT NULL,
		kind text NOT NULL,
		updated_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (account_id, kind)
	);

	CREATE TABLE IF NOT EXISTS titles (
		name text NOT NULL,
		platform text NOT NULL,
		status text NOT NULL,
		PRIMARY KEY (name, platform)
	);`,

	// Push. The token is the primary key because it identifies the install,
	// not the person: reinstalling gives a new one, and the same phone signing
	// in as someone else must move rather than duplicate — hence the upsert on
	// conflict rewriting account_id.
	"0002_push": `CREATE TABLE IF NOT EXISTS push_tokens (
		token text PRIMARY KEY,
		account_id text NOT NULL,
		platform text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		last_seen_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS push_tokens_account_idx ON push_tokens(account_id);

	CREATE TABLE IF NOT EXISTS push_cursor (
		id int PRIMARY KEY,
		version bigint NOT NULL
	);
	INSERT INTO push_cursor(id, version) VALUES (1, 0) ON CONFLICT (id) DO NOTHING;`,

	// Requests + votes. A request is a votable item — a title or a network
	// feature — seeded from the catalog. request_votes is the vote ledger, one
	// row per (request, account); a count over it is the score. ON DELETE
	// CASCADE keeps the ledger clean if a request is ever removed, but reseeding
	// never deletes a request, so votes persist across every reseed.
	"0003_requests": `CREATE TABLE IF NOT EXISTS requests (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		kind text NOT NULL,
		name text NOT NULL,
		console text NOT NULL DEFAULT '',
		category text NOT NULL DEFAULT '',
		backend text NOT NULL DEFAULT '',
		status text NOT NULL DEFAULT '',
		notes text NOT NULL DEFAULT '',
		sort int NOT NULL DEFAULT 0,
		UNIQUE (kind, name)
	);

	CREATE TABLE IF NOT EXISTS request_votes (
		request_id bigint NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
		account_id text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (request_id, account_id)
	);
	CREATE INDEX IF NOT EXISTS request_votes_account_idx ON request_votes(account_id);`,

	// A title is unique per console, not by name: Call of Duty: Black Ops is
	// three different games on three different networks, and each gets its own
	// row and its own votes.
	"0004_requests_per_console": `ALTER TABLE requests DROP CONSTRAINT IF EXISTS requests_kind_name_key;
	CREATE UNIQUE INDEX IF NOT EXISTS requests_kind_name_console_key ON requests(kind, name, console);`,

	// The database is the catalog: requests are written through the store (API and admin
	// forms), never seeded from catalog.json. Existing rows start at the migration time;
	// scripts/backfill-request-dates sets them from the catalog repository's history.
	// request_changes has no foreign key on purpose: a delete is logged too.
	"0005_request_changes": `ALTER TABLE requests
		ADD COLUMN IF NOT EXISTS date_added timestamptz NOT NULL DEFAULT now(),
		ADD COLUMN IF NOT EXISTS last_updated timestamptz NOT NULL DEFAULT now();

	CREATE TABLE IF NOT EXISTS request_changes (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		request_id bigint NOT NULL,
		actor text NOT NULL,
		action text NOT NULL,
		changed_at timestamptz NOT NULL DEFAULT now(),
		changes jsonb NOT NULL
	);
	CREATE INDEX IF NOT EXISTS request_changes_request_idx ON request_changes(request_id);`,

	// A title's console id, so emulators and tools join on it instead of on the name. Filled
	// here from the notes that lead with one; the rest are set through the API, which logs
	// them. Unique when set: one id is one game.
	"0006_request_title_id": `ALTER TABLE requests ADD COLUMN IF NOT EXISTS title_id text NOT NULL DEFAULT '';
	UPDATE requests SET title_id = upper(substring(notes from '^([0-9A-Fa-f]{16})'))
		WHERE kind = 'title' AND title_id = '' AND notes ~ '^[0-9A-Fa-f]{16}([^0-9A-Za-z]|$)';
	CREATE UNIQUE INDEX IF NOT EXISTS requests_title_id_key ON requests(title_id) WHERE title_id <> '';`,

	// Every id a title has: a Wii U or 3DS game has one per region (and a pair like Pokémon
	// X / Y two games), and a friend's presence carries whichever their console runs.
	// title_id stays as the primary, always title_ids[1]. Ids are unique across rows; the
	// store checks that, since an array cannot carry a unique index.
	"0007_request_title_ids": `ALTER TABLE requests ADD COLUMN IF NOT EXISTS title_ids text[] NOT NULL DEFAULT '{}';
	UPDATE requests SET title_ids = ARRAY[title_id] WHERE title_id <> '' AND title_ids = '{}';
	CREATE INDEX IF NOT EXISTS requests_title_ids_idx ON requests USING gin (title_ids);`,
}

// Ping verifies database connectivity (status page).
func (s *PG) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// PutWebSession upserts the session index row.
func (s *PG) PutWebSession(ctx context.Context, ws WebSession) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO web_sessions(core_session_id, account_id, email, display_name)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (core_session_id) DO UPDATE SET account_id=EXCLUDED.account_id,
			email=EXCLUDED.email, display_name=EXCLUDED.display_name`,
		ws.CoreSessionID, ws.AccountID, ws.Email, ws.DisplayName)
	return err
}

// GetWebSession resolves the session index row.
func (s *PG) GetWebSession(ctx context.Context, coreSessionID string) (WebSession, error) {
	var ws WebSession
	err := s.pool.QueryRow(ctx, `SELECT core_session_id, account_id, email, display_name, created_at
		FROM web_sessions WHERE core_session_id=$1`, coreSessionID).Scan(
		&ws.CoreSessionID, &ws.AccountID, &ws.Email, &ws.DisplayName, &ws.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ws, ErrNotFound
	}
	return ws, err
}

// DeleteWebSession removes one index row (logout).
func (s *PG) DeleteWebSession(ctx context.Context, coreSessionID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE core_session_id=$1`, coreSessionID)
	return err
}

// DeleteWebSessionsForAccount removes every index row of an account (used
// after a password reset or ban).
func (s *PG) DeleteWebSessionsForAccount(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE account_id=$1`, accountID)
	return err
}

// GetSiteConfig reads the operator-controlled configuration. Missing keys
// fall back to safe defaults.
func (s *PG) GetSiteConfig(ctx context.Context) (SiteConfig, error) {
	cfg := SiteConfig{RegistrationsOpen: false}
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM site_config`)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return cfg, err
		}
		switch k {
		case "registrations_open":
			cfg.RegistrationsOpen = v == "true"
		case "banner_message":
			cfg.BannerMessage = v
		}
	}
	return cfg, rows.Err()
}

// SetSiteConfig writes both keys in one transaction.
func (s *PG) SetSiteConfig(ctx context.Context, cfg SiteConfig) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	open := "false"
	if cfg.RegistrationsOpen {
		open = "true"
	}
	for _, kv := range [][2]string{{"registrations_open", open}, {"banner_message", cfg.BannerMessage}} {
		if _, err := tx.Exec(ctx, `INSERT INTO site_config(key,value) VALUES($1,$2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CreateAPIToken stores a hashed mobile bearer token.
func (s *PG) CreateAPIToken(ctx context.Context, id, tokenHash, accountID, coreSessionID string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO api_tokens(id, token_hash, account_id, core_session_id, expires_at)
		VALUES($1,$2,$3,$4,$5)`, id, tokenHash, accountID, coreSessionID, expiresAt)
	return err
}

// APITokenByHash resolves a bearer token by its storage hash. Expired or
// revoked tokens read as not found.
func (s *PG) APITokenByHash(ctx context.Context, hash string) (APIToken, error) {
	var t APIToken
	err := s.pool.QueryRow(ctx, `SELECT id, account_id, core_session_id, created_at, expires_at, revoked_at
		FROM api_tokens WHERE token_hash=$1`, hash).Scan(
		&t.ID, &t.AccountID, &t.CoreSessionID, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if t.RevokedAt != nil || time.Now().After(t.ExpiresAt) {
		return t, ErrNotFound
	}
	return t, nil
}

// RevokeAPIToken revokes one token by id.
func (s *PG) RevokeAPIToken(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = now()
		WHERE id=$1 AND revoked_at IS NULL`, id)
	return err
}

// TouchAPIToken updates last_seen_at for a token.
func (s *PG) TouchAPIToken(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET last_seen_at = now() WHERE id=$1`, id)
	return err
}

// RevokeAPITokensForAccount revokes every live token of an account (used
// after password resets and bans).
func (s *PG) RevokeAPITokensForAccount(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = now()
		WHERE account_id=$1 AND revoked_at IS NULL`, accountID)
	return err
}

// AddAudit records a moderation/operator action.
func (s *PG) AddAudit(ctx context.Context, actorID, action, detail string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO admin_audit(actor_id, action, detail) VALUES($1,$2,$3)`,
		actorID, action, detail)
	return err
}

// ListAudit returns the newest audit entries.
func (s *PG) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, actor_id, action, detail, created_at
		FROM admin_audit ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertMedia records avatar metadata for an account.
func (s *PG) UpsertMedia(ctx context.Context, m MediaRecord) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO media(account_id, kind, updated_at) VALUES($1,$2,$3)
		ON CONFLICT (account_id, kind) DO UPDATE SET updated_at = EXCLUDED.updated_at`,
		m.AccountID, m.Kind, m.UpdatedAt)
	return err
}

// GetMedia fetches media metadata.
func (s *PG) GetMedia(ctx context.Context, accountID, kind string) (MediaRecord, error) {
	var m MediaRecord
	err := s.pool.QueryRow(ctx, `SELECT account_id, kind, updated_at FROM media
		WHERE account_id=$1 AND kind=$2`, accountID, kind).Scan(&m.AccountID, &m.Kind, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// SavePushToken records an app install for this account. The token is the key:
// the same install signing in as a different account moves rather than
// duplicating, so one phone never receives another person's notifications.
func (s *PG) SavePushToken(ctx context.Context, accountID, token, platform string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO push_tokens(token, account_id, platform)
		VALUES($1,$2,$3)
		ON CONFLICT (token) DO UPDATE SET account_id=EXCLUDED.account_id,
			platform=EXCLUDED.platform, last_seen_at=now()`, token, accountID, platform)
	return err
}

// DeletePushToken forgets an install: sign-out, or a token the transport told
// us is dead.
func (s *PG) DeletePushToken(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_tokens WHERE token=$1`, token)
	return err
}

// DeleteMediaForAccount drops the account's avatar metadata (account erasure).
func (s *PG) DeleteMediaForAccount(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM media WHERE account_id=$1`, accountID)
	return err
}

// RevokePushTokensForAccount silences every install of a deleted account: a
// token that survives deletion would keep delivering another person's
// notifications to the device.
func (s *PG) RevokePushTokensForAccount(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_tokens WHERE account_id=$1`, accountID)
	return err
}

func (s *PG) PushTokensForAccount(ctx context.Context, accountID string) ([]PushToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT token, platform FROM push_tokens WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushToken
	for rows.Next() {
		var t PushToken
		if err := rows.Scan(&t.Token, &t.Platform); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PG) PushCursor(ctx context.Context) (uint64, error) {
	var v uint64
	err := s.pool.QueryRow(ctx, `SELECT version FROM push_cursor WHERE id=1`).Scan(&v)
	return v, err
}

func (s *PG) SetPushCursor(ctx context.Context, version uint64) error {
	_, err := s.pool.Exec(ctx, `UPDATE push_cursor SET version=$1 WHERE id=1`, version)
	return err
}
