package store

// Account erasure: what the website itself holds about an account, for the
// erasure finaliser (internal/erasure) once the deletion grace period is over.

import "context"

// ErasureStore is the website's part of erasing an account.
type ErasureStore interface {
	// AttachmentKeysForAccount lists the files on disk that EraseAccount would
	// orphan: attachments on the account's tickets and on its crash reports.
	AttachmentKeysForAccount(ctx context.Context, accountID string) ([]string, error)
	// EraseAccount deletes every website row of the account: web sessions,
	// API tokens, avatar metadata, push installs, votes, compatibility
	// reports, help tickets with their whole threads, crash reports, the ban
	// record and operator applications. The admin audit log stays (it names
	// admins, and the account only by its ID). Idempotent.
	EraseAccount(ctx context.Context, accountID string) error
	// TryLock takes a named lease that only one website instance holds at a
	// time; ok is false when another holds it. unlock releases it.
	TryLock(ctx context.Context, name string) (unlock func(), ok bool, err error)
}

func (s *PG) AttachmentKeysForAccount(ctx context.Context, accountID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.attachment_key FROM ticket_messages m JOIN tickets t ON t.id = m.ticket_id
		WHERE t.account_id = $1 AND m.attachment_key <> ''
		UNION SELECT attachment_key FROM crash_reports WHERE account_id = $1 AND attachment_key <> ''`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *PG) EraseAccount(ctx context.Context, accountID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM web_sessions WHERE account_id=$1`,
		`DELETE FROM api_tokens WHERE account_id=$1`,
		`DELETE FROM media WHERE account_id=$1`,
		`DELETE FROM push_tokens WHERE account_id=$1`,
		`DELETE FROM request_votes WHERE account_id=$1`,
		`DELETE FROM title_reports WHERE account_id=$1`,
		`DELETE FROM crash_reports WHERE account_id=$1`,
		`DELETE FROM tickets WHERE account_id=$1`, // ticket_messages cascade
		`DELETE FROM account_bans WHERE account_id=$1`,
		`DELETE FROM operator_applications WHERE account_id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, accountID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// TryLock is a session-level Postgres advisory lock on its own connection, so
// it survives as long as the holder runs and dies with it.
func (s *PG) TryLock(ctx context.Context, name string) (func(), bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, name)
		conn.Release()
	}, true, nil
}

func (m *Memory) AttachmentKeysForAccount(_ context.Context, accountID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mine := map[int64]bool{}
	for _, t := range m.support.tickets {
		if t.AccountID == accountID {
			mine[t.ID] = true
		}
	}
	var keys []string
	for _, msg := range m.support.messages {
		if mine[msg.TicketID] && msg.Attachment.Key != "" {
			keys = append(keys, msg.Attachment.Key)
		}
	}
	for _, c := range m.support.crashes {
		if c.AccountID == accountID && c.Attachment.Key != "" {
			keys = append(keys, c.Attachment.Key)
		}
	}
	return keys, nil
}

func (m *Memory) EraseAccount(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, ws := range m.webSessions {
		if ws.AccountID == accountID {
			delete(m.webSessions, k)
		}
	}
	for k, t := range m.tokens {
		if t.AccountID == accountID {
			delete(m.tokens, k)
		}
	}
	for k := range m.media {
		if k[0] == accountID {
			delete(m.media, k)
		}
	}
	for tok, owner := range m.pushOwner {
		if owner == accountID {
			delete(m.pushTokens, tok)
			delete(m.pushOwner, tok)
		}
	}
	for _, voters := range m.votes {
		delete(voters, accountID)
	}
	for k := range m.compat {
		if k.account == accountID {
			delete(m.compat, k)
		}
	}
	delete(m.bans, accountID)
	gone := map[int64]bool{}
	tickets := m.support.tickets[:0]
	for _, t := range m.support.tickets {
		if t.AccountID == accountID {
			gone[t.ID] = true
			continue
		}
		tickets = append(tickets, t)
	}
	m.support.tickets = tickets
	msgs := m.support.messages[:0]
	for _, msg := range m.support.messages {
		if !gone[msg.TicketID] {
			msgs = append(msgs, msg)
		}
	}
	m.support.messages = msgs
	crashes := m.support.crashes[:0]
	for _, c := range m.support.crashes {
		if c.AccountID != accountID {
			crashes = append(crashes, c)
		}
	}
	m.support.crashes = crashes
	return nil
}

// TryLock on Memory is per store: a memory store is one process.
func (m *Memory) TryLock(_ context.Context, name string) (func(), bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks[name] {
		return nil, false, nil
	}
	if m.locks == nil {
		m.locks = map[string]bool{}
	}
	m.locks[name] = true
	return func() {
		m.mu.Lock()
		delete(m.locks, name)
		m.mu.Unlock()
	}, true, nil
}
