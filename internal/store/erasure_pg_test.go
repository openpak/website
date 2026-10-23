package store

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// Account erasure takes every website row of the account and nobody else's,
// lists the files it would orphan first, and the lease is held by one
// connection at a time.
func TestPGEraseAccount(t *testing.T) {
	s := testPG(t)
	ctx := context.Background()
	const gone, stays = "erase-gone", "erase-stays"
	var reqID int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO requests (kind, name, console) VALUES ('title', 'Erasure Test Game', 'switch')
		ON CONFLICT (kind, name, console) DO UPDATE SET notes = requests.notes RETURNING id`).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{gone, stays} {
		for _, q := range []string{
			`INSERT INTO web_sessions (core_session_id, account_id) VALUES ('ws-' || $1, $1) ON CONFLICT DO NOTHING`,
			`INSERT INTO api_tokens (id, token_hash, account_id, core_session_id, expires_at) VALUES ('tok-' || $1, 'hash-' || $1, $1, 'cs', now() + interval '1 day') ON CONFLICT DO NOTHING`,
			`INSERT INTO media (account_id, kind) VALUES ($1, 'avatar') ON CONFLICT DO NOTHING`,
			`INSERT INTO push_tokens (token, account_id, platform) VALUES ('push-' || $1, $1, 'fcm') ON CONFLICT DO NOTHING`,
			`INSERT INTO account_bans (account_id, banned) VALUES ($1, true) ON CONFLICT DO NOTHING`,
			`INSERT INTO operator_applications (account_id, email, country, terms_at) VALUES ($1, 'op@example.com', 'PT', now())`,
			`INSERT INTO crash_reports (source, account_id, signature, attachment_key) VALUES ('harbor', $1, 'sig', 'crashes/aa/' || md5($1))`,
		} {
			if _, err := s.pool.Exec(ctx, q, id); err != nil {
				t.Fatalf("seed %q: %v", q[:40], err)
			}
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO request_votes (request_id, account_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, reqID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO title_reports (request_id, account_id, client, os, works) VALUES ($1, $2, 'harbor', 'linux', true) ON CONFLICT DO NOTHING`, reqID, id); err != nil {
			t.Fatal(err)
		}
		tk, err := s.CreateTicket(ctx, Ticket{AccountID: id, Subject: "help", Category: "other"},
			TicketMessage{AuthorID: id, Body: "it broke", Attachment: Attachment{Key: "tickets/bb/" + strings.Repeat("0", 30) + id[len(id)-2:], Name: "log.txt"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddTicketMessage(ctx, TicketMessage{TicketID: tk.ID, AuthorID: "staff-1", Staff: true, Body: "on it"}, ""); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := s.AttachmentKeysForAccount(ctx, gone)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	if len(keys) != 2 || !strings.HasPrefix(keys[0], "crashes/") || !strings.HasPrefix(keys[1], "tickets/") {
		t.Fatalf("attachment keys = %v", keys)
	}
	if err := s.EraseAccount(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := s.EraseAccount(ctx, gone); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	for _, table := range []string{"web_sessions", "api_tokens", "media", "push_tokens", "account_bans",
		"operator_applications", "crash_reports", "request_votes", "title_reports", "tickets"} {
		for id, want := range map[string]bool{gone: false, stays: true} {
			var n int
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE account_id=$1`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if (n > 0) != want {
				t.Errorf("%s: %s has %d rows after erasing %s", table, id, n, gone)
			}
		}
	}
	var orphans int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ticket_messages m WHERE NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id = m.ticket_id)`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("orphaned ticket messages: %d %v", orphans, err)
	}
	if keys, _ := s.AttachmentKeysForAccount(ctx, gone); len(keys) != 0 {
		t.Fatalf("keys after erasure: %v", keys)
	}
	if err := s.EraseAccount(ctx, stays); err != nil {
		t.Fatal(err)
	}

	// The lease: a second holder is refused until the first lets go.
	other := testPG(t)
	unlock, ok, err := s.TryLock(ctx, "erasure-test")
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok, err := other.TryLock(ctx, "erasure-test"); err != nil || ok {
		t.Fatalf("second holder got the lease: %v %v", ok, err)
	}
	unlock()
	unlock2, ok, err := other.TryLock(ctx, "erasure-test")
	if err != nil || !ok {
		t.Fatalf("lock after release: %v %v", ok, err)
	}
	unlock2()
}
