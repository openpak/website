package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func testPG(t *testing.T) *PG {
	t.Helper()
	dsn := os.Getenv("WEBSITE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEBSITE_TEST_DATABASE_URL not set; Postgres tests skipped")
	}
	s, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// The ban record, including the backfill from admin_audit that 0009 runs once.
func TestPGBans(t *testing.T) {
	s := testPG(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE admin_audit; DROP TABLE account_bans;
		DELETE FROM schema_migrations WHERE version = '0009_account_bans'`); err != nil {
		t.Fatal(err)
	}
	_ = s.AddAudit(ctx, "adm-1", "account.ban", `account=acct-1 reason="said \"hi\" rudely"`)
	_ = s.AddAudit(ctx, "adm-1", "account.ban", `account=acct-2 reason="spam"`)
	_ = s.AddAudit(ctx, "adm-2", "account.unban", `account=acct-2 reason="appeal"`)
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := s.GetBan(ctx, "acct-1")
	if err != nil || !b.Banned || b.Reason != `said "hi" rudely` || b.Actor != "adm-1" {
		t.Fatalf("backfilled ban: %+v %v", b, err)
	}
	if b, _ := s.GetBan(ctx, "acct-2"); b.Banned || b.Actor != "adm-2" {
		t.Fatalf("backfilled unban: %+v", b)
	}
	if err := s.RecordBan(ctx, Ban{AccountID: "acct-3", Banned: true, Reason: "cheating", Actor: "adm-3"}); err != nil {
		t.Fatal(err)
	}
	bans, err := s.ListBans(ctx, 10)
	if err != nil || len(bans) != 2 || bans[0].AccountID != "acct-3" {
		t.Fatalf("list: %+v %v", bans, err)
	}
	if _, err := s.GetBan(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestPGTicketsAndCrashes(t *testing.T) {
	s := testPG(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE tickets, ticket_messages, crash_reports`); err != nil {
		t.Fatal(err)
	}
	runSupport(t, s)
}

// The same journey in memory, so CI (which has no database) covers it too.
func TestMemoryTicketsAndCrashes(t *testing.T) { runSupport(t, NewMemory()) }

func runSupport(t *testing.T, s Store) {
	ctx := context.Background()
	if _, err := s.CreateTicket(ctx, Ticket{AccountID: "a", Subject: "x", Category: "nope"}, TicketMessage{Body: "b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad category: %v", err)
	}
	tk, err := s.CreateTicket(ctx, Ticket{AccountID: "a", DisplayName: "Ann", Subject: " Can't sign in ", Category: "account"},
		TicketMessage{Body: "help", Attachment: Attachment{Key: "k1", Name: "log.txt", ContentType: "text/plain", Size: 3}})
	if err != nil || tk.Status != TicketOpen || tk.Subject != "Can't sign in" || tk.Messages != 1 {
		t.Fatalf("create: %+v %v", tk, err)
	}
	if _, err := s.AddTicketMessage(ctx, TicketMessage{TicketID: tk.ID, AuthorID: "adm", Staff: true, Internal: true, Body: "note"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketMessage(ctx, TicketMessage{TicketID: tk.ID, AuthorID: "adm", Staff: true, Body: "try again"}, TicketWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketMessage(ctx, TicketMessage{TicketID: 999999, Body: "x"}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ticket: %v", err)
	}
	pub, _ := s.TicketMessages(ctx, tk.ID, false)
	all, _ := s.TicketMessages(ctx, tk.ID, true)
	if len(pub) != 2 || len(all) != 3 || pub[0].Attachment.Name != "log.txt" {
		t.Fatalf("thread: %d public, %d all", len(pub), len(all))
	}
	adm := "adm"
	tk, err = s.UpdateTicket(ctx, tk.ID, nil, &adm)
	if err != nil || tk.Status != TicketWaiting || tk.Assignee != "adm" || tk.Messages != 2 {
		t.Fatalf("assign: %+v %v", tk, err)
	}
	if list, _ := s.ListTickets(ctx, TicketFilter{Status: TicketWaiting, Assignee: "adm"}, 10); len(list) != 1 {
		t.Fatalf("queue: %+v", list)
	}
	if list, _ := s.ListTickets(ctx, TicketFilter{AccountID: "b"}, 10); len(list) != 0 {
		t.Fatalf("someone else's list: %+v", list)
	}

	// Two runs of one bug (different addresses) share a signature; a third does not.
	day := time.Now().Add(-time.Hour)
	for _, msg := range []string{"panic at 0x7fff1234abcd in Foo", "panic at 0x7fff9999ffff in Foo", "other"} {
		if _, err := s.CreateCrash(ctx, CrashReport{Source: "ryujinx", Version: "1.2", TitleID: "0100a", ErrorCode: "2162-0002",
			Message: msg, Fields: json.RawMessage(`{"gpu":"x"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateCrash(ctx, CrashReport{Source: "bogus"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad source: %v", err)
	}
	groups, err := s.CrashGroups(ctx, CrashFilter{Source: "ryujinx", From: day}, 10)
	if err != nil || len(groups) != 2 || groups[0].Count+groups[1].Count != 3 {
		t.Fatalf("groups: %+v %v", groups, err)
	}
	var big CrashGroup
	for _, g := range groups {
		if g.Count == 2 {
			big = g
		}
	}
	if big.Open != 2 || len(big.Versions) != 1 || big.TitleID != "0100A" {
		t.Fatalf("group: %+v", big)
	}
	if n, err := s.ResolveCrashes(ctx, 0, big.Signature, true, "adm"); err != nil || n != 2 {
		t.Fatalf("resolve group: %d %v", n, err)
	}
	if open, _ := s.ListCrashes(ctx, CrashFilter{Status: "open", ErrorCode: "2162-0002"}, 10); len(open) != 1 {
		t.Fatalf("open after resolve: %d", len(open))
	}
	c, _ := s.GetCrash(ctx, big.LatestID)
	if !c.Resolved || c.ResolvedBy != "adm" || c.ResolvedAt == nil || string(c.Fields) == "" {
		t.Fatalf("resolved crash: %+v", c)
	}
	if err := s.LinkCrashTicket(ctx, c.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkCrashTicket(ctx, c.ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link to a missing ticket: %v", err)
	}
	if c, _ := s.GetCrash(ctx, c.ID); c.TicketID != tk.ID {
		t.Fatalf("linked: %+v", c)
	}
}
