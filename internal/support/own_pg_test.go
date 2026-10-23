package support

// Cross-account ticket isolation: B can neither read nor append to A's
// ticket, and the refusal is indistinguishable from "no such ticket".

import (
	"context"
	"os"
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestTicketCrossAccountIsolation(t *testing.T) {
	dsn := os.Getenv("WEBSITE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEBSITE_TEST_DATABASE_URL not set; Postgres tests skipped")
	}
	db, err := store.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	svc := &Service{DB: db}
	ctx := context.Background()

	opened, err := svc.Open(ctx, store.Ticket{AccountID: "acct-a", Subject: "help", Category: "other"}, "mine", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, _, err := svc.Own(ctx, opened.ID, "acct-a"); err != nil {
		t.Fatalf("owner cannot read own ticket: %v", err)
	}
	if _, _, err := svc.Own(ctx, opened.ID, "acct-b"); err == nil {
		t.Fatal("another account read the ticket")
	}
	if _, err := svc.Reply(ctx, opened.ID, "acct-b", "hijack", nil); err == nil {
		t.Fatal("another account appended to the ticket")
	}
	if _, err := svc.Reply(ctx, opened.ID, "acct-a", "thanks", nil); err != nil {
		t.Fatalf("owner cannot reply: %v", err)
	}
	if _, _, err := svc.Own(ctx, -opened.ID, "acct-a"); err == nil {
		t.Fatal("missing ticket did not fail")
	}
}
