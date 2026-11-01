package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDiscordTaken means the Discord account is already linked to another
// OpenPak account. One Discord account is one OpenPak account.
var ErrDiscordTaken = errors.New("store: discord account already linked")

// DiscordLink ties an OpenPak account to the Discord user who proved they
// own it by signing in with Discord (OAuth2, identify). The name is only
// for display; the id is the identity.
type DiscordLink struct {
	AccountID string
	DiscordID string
	Username  string
	LinkedAt  time.Time
}

// DiscordStore is the Discord link table. At most one link per account and
// one account per Discord id.
type DiscordStore interface {
	// LinkDiscord stores the link, replacing the account's earlier one. It
	// returns ErrDiscordTaken when another account holds the Discord id.
	LinkDiscord(ctx context.Context, l DiscordLink) error
	// UnlinkDiscord removes the account's link and returns it; ErrNotFound if none.
	UnlinkDiscord(ctx context.Context, accountID string) (DiscordLink, error)
	DiscordLinkByAccount(ctx context.Context, accountID string) (DiscordLink, error)
	DiscordLinkByDiscordID(ctx context.Context, discordID string) (DiscordLink, error)
}

func (s *PG) LinkDiscord(ctx context.Context, l DiscordLink) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO discord_links(account_id, discord_id, username) VALUES($1,$2,$3)
		ON CONFLICT (account_id) DO UPDATE SET discord_id=EXCLUDED.discord_id, username=EXCLUDED.username, linked_at=now()`,
		l.AccountID, l.DiscordID, l.Username)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDiscordTaken
	}
	return err
}

func (s *PG) UnlinkDiscord(ctx context.Context, accountID string) (DiscordLink, error) {
	var l DiscordLink
	err := s.pool.QueryRow(ctx, `DELETE FROM discord_links WHERE account_id=$1 RETURNING account_id, discord_id, username, linked_at`, accountID).
		Scan(&l.AccountID, &l.DiscordID, &l.Username, &l.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

func (s *PG) DiscordLinkByAccount(ctx context.Context, accountID string) (DiscordLink, error) {
	return s.oneDiscord(ctx, `account_id`, accountID)
}

func (s *PG) DiscordLinkByDiscordID(ctx context.Context, discordID string) (DiscordLink, error) {
	return s.oneDiscord(ctx, `discord_id`, discordID)
}

func (s *PG) oneDiscord(ctx context.Context, col, val string) (DiscordLink, error) {
	var l DiscordLink
	err := s.pool.QueryRow(ctx, `SELECT account_id, discord_id, username, linked_at FROM discord_links WHERE `+col+`=$1`, val).
		Scan(&l.AccountID, &l.DiscordID, &l.Username, &l.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

func (m *Memory) LinkDiscord(_ context.Context, l DiscordLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for acct, e := range m.discord {
		if e.DiscordID == l.DiscordID && acct != l.AccountID {
			return ErrDiscordTaken
		}
	}
	if m.discord == nil {
		m.discord = map[string]DiscordLink{}
	}
	l.LinkedAt = time.Now()
	m.discord[l.AccountID] = l
	return nil
}

func (m *Memory) UnlinkDiscord(_ context.Context, accountID string) (DiscordLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.discord[accountID]
	if !ok {
		return l, ErrNotFound
	}
	delete(m.discord, accountID)
	return l, nil
}

func (m *Memory) DiscordLinkByAccount(_ context.Context, accountID string) (DiscordLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.discord[accountID]; ok {
		return l, nil
	}
	return DiscordLink{}, ErrNotFound
}

func (m *Memory) DiscordLinkByDiscordID(_ context.Context, discordID string) (DiscordLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.discord {
		if l.DiscordID == discordID {
			return l, nil
		}
	}
	return DiscordLink{}, ErrNotFound
}
