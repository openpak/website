package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestPGRequestWrites runs the catalog writes against real Postgres: the transaction, the
// change log, the last_updated bump and the vote-protected delete. CI has no database, so
// this runs only when WEBSITE_TEST_DATABASE_URL is set (a throwaway database; it is wiped).
func TestPGRequestWrites(t *testing.T) {
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
	if _, err := s.pool.Exec(ctx, `TRUNCATE requests, request_votes, request_changes, admin_audit`); err != nil {
		t.Fatal(err)
	}
	changes := func(id int64) (n int, last string) {
		t.Helper()
		if err := s.pool.QueryRow(ctx, `SELECT count(*), COALESCE((array_agg(actor || ' ' || action || ' ' || changes::text ORDER BY id DESC))[1], '')
			FROM request_changes WHERE request_id=$1`, id).Scan(&n, &last); err != nil {
			t.Fatal(err)
		}
		return n, last
	}

	r, err := s.CreateRequest(ctx, Request{Kind: "title", Name: "Kirby's Dream Buffet", Console: "Switch", Status: "beta", Sort: -1}, "api-key")
	if err != nil || r.ID == 0 || r.DateAdded.IsZero() {
		t.Fatalf("create: %+v %v", r, err)
	}
	if _, err := s.CreateRequest(ctx, Request{Kind: "title", Name: "Kirby's Dream Buffet", Console: "Switch", Status: "live"}, "api-key"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.CreateRequest(ctx, Request{Kind: "title", Name: "X", Status: "done"}, "api-key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status: %v", err)
	}
	live, notes := "live", "  played online  "
	u, err := s.UpdateRequest(ctx, r.ID, RequestPatch{Status: &live, Notes: &notes}, "acct-admin")
	if err != nil || u.Status != "live" || u.Notes != "played online" || !u.LastUpdated.After(r.LastUpdated) || !u.DateAdded.Equal(r.DateAdded) {
		t.Fatalf("update: %+v %v", u, err)
	}
	if n, last := changes(r.ID); n != 2 || last != `acct-admin update {"notes": {"new": "played online", "old": ""}, "status": {"new": "live", "old": "beta"}}` {
		t.Fatalf("log: %d %s", n, last)
	}
	var audit int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE action='title.status' AND detail LIKE '%beta -> live'`).Scan(&audit)
	if audit != 1 {
		t.Fatalf("title.status audit rows: %d", audit)
	}
	if _, err := s.UpdateRequest(ctx, r.ID, RequestPatch{Status: &live}, "acct-admin"); err != nil {
		t.Fatal(err)
	}
	if n, _ := changes(r.ID); n != 2 {
		t.Fatalf("a no-op update was logged: %d", n)
	}

	voted, _ := s.CreateRequest(ctx, Request{Kind: "title", Name: "Tetris 99", Status: "requestable", Sort: -1}, "api-key")
	if voted.Sort != r.Sort+1 {
		t.Errorf("sort -1 should file last: %d after %d", voted.Sort, r.Sort)
	}
	if _, err := s.ToggleVote(ctx, voted.ID, "acct-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRequest(ctx, voted.ID, "api-key"); !errors.Is(err, ErrHasVotes) {
		t.Fatalf("delete voted: %v", err)
	}
	if err := s.DeleteRequest(ctx, r.ID, "api-key"); err != nil {
		t.Fatal(err)
	}
	if n, last := changes(r.ID); n != 3 || last[:16] != "api-key delete {" {
		t.Fatalf("delete log: %d %s", n, last)
	}
	if err := s.DeleteRequest(ctx, r.ID, "api-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
