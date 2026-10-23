package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestPGOperatorApplications runs the application lifecycle against real
// Postgres; skipped without WEBSITE_TEST_DATABASE_URL (a throwaway database).
func TestPGOperatorApplications(t *testing.T) {
	dsn := os.Getenv("WEBSITE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEBSITE_TEST_DATABASE_URL not set; Postgres writes skipped")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.pool.Exec(ctx, `TRUNCATE operator_applications`); err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateOperatorApplication(ctx, OperatorApplication{AccountID: "acct-op", Email: "op@x.test", Location: "sa-east", Country: "BR",
		City: "São Paulo", Provider: "Hetzner", TermsAt: time.Now(),
		Servers: []OperatorServerPlan{{Cores: 8, RAMGB: 16, DiskGB: 200, DiskSSD: true, BandwidthMbps: 1000, IPv6: true, Roles: []string{"photon", "turn"}}}})
	if err != nil || a.ID == 0 || a.Status != OpPending || len(a.Servers) != 1 || a.Servers[0].Roles[1] != "turn" {
		t.Fatalf("create: %+v %v", a, err)
	}
	if err := s.ReplyOperatorApplication(ctx, a.ID, "acct-op", "early"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reply while pending: %v", err)
	}
	if err := s.DecideOperatorApplication(ctx, a.ID, OpInfo, "Which datacentre?", "", "acct-admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplyOperatorApplication(ctx, a.ID, "someone-else", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reply by another account: %v", err)
	}
	if err := s.ReplyOperatorApplication(ctx, a.ID, "acct-op", "Falkenstein"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideOperatorApplication(ctx, a.ID, OpApproved, "", "br-sao", "acct-admin"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetOperatorApplication(ctx, a.ID)
	if err != nil || got.Status != OpApproved || got.NodeID != "br-sao" || got.Reply != "Falkenstein" || got.DecidedBy != "acct-admin" {
		t.Fatalf("after approve: %+v %v", got, err)
	}
	if l, _ := s.ListOperatorApplications(ctx, OpPending); len(l) != 0 {
		t.Fatalf("pending list: %+v", l)
	}
	if l, _ := s.OperatorApplicationsFor(ctx, "acct-op"); len(l) != 1 {
		t.Fatalf("mine: %+v", l)
	}
	if err := s.DecideOperatorApplication(ctx, a.ID, "bogus", "", "", "x"); err == nil {
		t.Fatal("status outside the check constraint accepted")
	}
}
