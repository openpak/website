package store

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Ban is the website's record of the latest ban decision on an account: who
// made it and why. The ban itself lives in the core (the account's status);
// this is what the admin shows beside it and what /internal/bans answers with
// (docs/ban-lookup.md). Unbanning keeps the row with Banned false, so the
// last decision stays readable.
type Ban struct {
	AccountID string
	Banned    bool
	Reason    string
	Actor     string
	UpdatedAt time.Time
}

// The website's ban record. Existing bans made before it are recovered from
// admin_audit, whose detail is `account=<id> reason="<reason>"`: the latest
// account.ban/account.unban entry per account wins.
func init() {
	migrations["0009_account_bans"] = `CREATE TABLE IF NOT EXISTS account_bans (
		account_id text PRIMARY KEY,
		banned boolean NOT NULL,
		reason text NOT NULL DEFAULT '',
		actor text NOT NULL DEFAULT '',
		updated_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS account_bans_banned_idx ON account_bans(updated_at DESC) WHERE banned;
	INSERT INTO account_bans(account_id, banned, reason, actor, updated_at)
	SELECT DISTINCT ON (acct) acct, action = 'account.ban',
		COALESCE(replace(substring(detail from 'reason="(.*)"$'), '\"', '"'), ''), actor_id, created_at
	FROM (SELECT substring(detail from '^account=(\S+)') AS acct, action, detail, actor_id, created_at, id
		FROM admin_audit WHERE action IN ('account.ban', 'account.unban')) a
	WHERE acct IS NOT NULL
	ORDER BY acct, id DESC
	ON CONFLICT (account_id) DO NOTHING;`
}

// RecordBan stores the latest ban decision for an account.
func (s *PG) RecordBan(ctx context.Context, b Ban) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO account_bans(account_id, banned, reason, actor, updated_at)
		VALUES($1,$2,$3,$4,now())
		ON CONFLICT (account_id) DO UPDATE SET banned=EXCLUDED.banned, reason=EXCLUDED.reason,
			actor=EXCLUDED.actor, updated_at=EXCLUDED.updated_at`, b.AccountID, b.Banned, b.Reason, b.Actor)
	return err
}

// GetBan reads the latest decision; ErrNotFound when the website never made one.
func (s *PG) GetBan(ctx context.Context, accountID string) (Ban, error) {
	var b Ban
	err := s.pool.QueryRow(ctx, `SELECT account_id, banned, reason, actor, updated_at FROM account_bans
		WHERE account_id=$1`, accountID).Scan(&b.AccountID, &b.Banned, &b.Reason, &b.Actor, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// ListBans lists the accounts currently banned through the website, newest first.
func (s *PG) ListBans(ctx context.Context, limit int) ([]Ban, error) {
	rows, err := s.pool.Query(ctx, `SELECT account_id, banned, reason, actor, updated_at FROM account_bans
		WHERE banned ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ban
	for rows.Next() {
		var b Ban
		if err := rows.Scan(&b.AccountID, &b.Banned, &b.Reason, &b.Actor, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (m *Memory) RecordBan(_ context.Context, b Ban) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bans == nil {
		m.bans = map[string]Ban{}
	}
	b.UpdatedAt = time.Now()
	m.bans[b.AccountID] = b
	return nil
}

func (m *Memory) GetBan(_ context.Context, accountID string) (Ban, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bans[accountID]
	if !ok {
		return b, ErrNotFound
	}
	return b, nil
}

func (m *Memory) ListBans(_ context.Context, limit int) ([]Ban, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Ban
	for _, b := range m.bans {
		if b.Banned {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Ban) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
