package pages

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

// A status change leaves an audit row saying what the title was before; a
// feature row is refused, because its status follows the catalog.
func TestSetTitleStatusAudits(t *testing.T) {
	ctx := context.Background()
	db := store.NewMemory()
	_ = db.SeedRequests(ctx, []store.Request{
		{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "beta"},
		{Kind: "feature", Name: "Cloud saves", Status: "planned"},
	})
	all, _ := db.ListRequests(ctx, "", "")
	var title, feature int64
	for _, r := range all {
		if r.Kind == "title" {
			title = r.ID
		} else {
			feature = r.ID
		}
	}
	if err := SetTitleStatus(ctx, db, "acct-admin", title, "live", "verified"); err != nil {
		t.Fatal(err)
	}
	audit, _ := db.ListAudit(ctx, 10)
	if len(audit) != 1 || audit[0].Action != "title.status" || !strings.Contains(audit[0].Detail, "beta -> live") {
		t.Fatalf("audit: %+v", audit)
	}
	if err := SetTitleStatus(ctx, db, "acct-admin", feature, "live", ""); !errors.Is(err, ErrBadTitleStatus) {
		t.Fatalf("feature edit must be refused, got %v", err)
	}
	if err := SetTitleStatus(ctx, db, "acct-admin", title, "shipped", ""); !errors.Is(err, ErrBadTitleStatus) {
		t.Fatalf("unknown status must be refused, got %v", err)
	}
}
