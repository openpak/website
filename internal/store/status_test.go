package store

import (
	"context"
	"testing"
)

// The database is the truth for a title's status: a reseed must not undo an admin's change.
func TestSeedKeepsStatusSetInDatabase(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	if err := m.SeedRequests(ctx, []Request{{Kind: "title", Name: "Splatoon", Console: "Wii U", Status: "requestable", Sort: 1}}); err != nil {
		t.Fatal(err)
	}
	reqs, _ := m.ListRequests(ctx, "title", "")
	if err := m.SetRequestStatus(ctx, reqs[0].ID, "beta", "deployed"); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRequests(ctx, []Request{{Kind: "title", Name: "Splatoon", Console: "Wii U", Status: "requestable", Sort: 2}, {Kind: "title", Name: "Pikmin 3", Console: "Wii U", Status: "requestable", Sort: 3}}); err != nil {
		t.Fatal(err)
	}
	reqs, _ = m.ListRequests(ctx, "title", "")
	if len(reqs) != 2 || reqs[0].Name != "Splatoon" || reqs[0].Status != "beta" || reqs[0].Notes != "deployed" || reqs[0].Sort != 2 {
		t.Fatalf("after reseed: %+v", reqs)
	}
	if err := m.SetRequestStatus(ctx, 999, "live", ""); err != ErrNotFound {
		t.Fatalf("unknown id: %v", err)
	}
}
