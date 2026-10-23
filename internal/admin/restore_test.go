package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type fakeErasure struct {
	restored []string
	err      error
}

func (f *fakeErasure) GetAccount(_ context.Context, id string) (core.Account, error) {
	return core.Account{ID: id, Status: "deletion-pending", EraseAfter: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeErasure) RestoreDeletion(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	f.restored = append(f.restored, id)
	return nil
}

// A closed account shows its erasure date and a restore button; the restore
// needs the CSRF token, goes to the core and into the audit log, and a late
// one says the erasure has started.
func TestAdminRestoreClosedAccount(t *testing.T) {
	s, db, _ := newAdmin(t)
	ctx := context.Background()
	_ = db.PutWebSession(ctx, store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root"})
	s.Moderation = &fakeModeration{accounts: []core.ModeratedAccount{
		{ID: "acct-3", Email: "luke@x.test", DisplayName: "Luke", Status: "deletion-pending"},
	}, resolved: map[string]string{}}
	fe := &fakeErasure{}
	s.Erasure = fe
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users?q=luke", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "/admin/users/acct-3/restore") || !strings.Contains(body, "22 Nov 2026") ||
		strings.Contains(body, "/admin/users/acct-3/ban") {
		t.Fatalf("users page: %d %s", rec.Code, body)
	}
	if rec := postForm(mux, "/admin/users/acct-3/restore", url.Values{}); rec.Code != http.StatusForbidden || len(fe.restored) != 0 {
		t.Fatalf("restore without CSRF: %d %v", rec.Code, fe.restored)
	}
	rec = postForm(mux, "/admin/users/acct-3/restore", url.Values{"csrf": {"tok-sess-admin"}, "q": {"luke"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "m=restored") || len(fe.restored) != 1 {
		t.Fatalf("restore: %d %s %v", rec.Code, rec.Header().Get("Location"), fe.restored)
	}
	audit, _ := db.ListAudit(ctx, 10)
	if len(audit) != 1 || audit[0].Action != "account.restore" || audit[0].ActorID != "acct-admin" {
		t.Fatalf("audit: %+v", audit)
	}
	fe.err = core.ErrErasureStarted
	rec = postForm(mux, "/admin/users/acct-3/restore", url.Values{"csrf": {"tok-sess-admin"}})
	if !strings.Contains(rec.Header().Get("Location"), "m=erasing") {
		t.Fatalf("late restore: %s", rec.Header().Get("Location"))
	}
}
