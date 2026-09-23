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

type banCall struct {
	id, reason, actor string
	banned            bool
}

type fakeModeration struct {
	accounts []core.ModeratedAccount
	bans     []banCall
	reports  []core.Report
	resolved map[string]string // report id -> actor
	queries  []string
}

func (f *fakeModeration) SearchAccounts(_ context.Context, q string, _ int) ([]core.ModeratedAccount, error) {
	f.queries = append(f.queries, q)
	var out []core.ModeratedAccount
	for _, a := range f.accounts {
		if strings.Contains(a.Email, q) || a.ID == q {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeModeration) SetBanned(_ context.Context, id string, banned bool, reason, actor string) error {
	f.bans = append(f.bans, banCall{id, reason, actor, banned})
	return nil
}

func (f *fakeModeration) ListReports(_ context.Context, status string, _ int) ([]core.Report, error) {
	var out []core.Report
	for _, r := range f.reports {
		if status == "" || r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeModeration) ResolveReport(_ context.Context, id, _, actor string) error {
	if _, done := f.resolved[id]; done {
		return core.ErrConflict
	}
	f.resolved[id] = actor
	return nil
}

func newModerationAdmin(t *testing.T) (*http.ServeMux, *fakeModeration, *store.Memory) {
	t.Helper()
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{
		CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root",
	})
	fm := &fakeModeration{
		accounts: []core.ModeratedAccount{
			{ID: "acct-1", Email: "leia@x.test", DisplayName: "Leia", Status: "active", EmailVerified: true, CreatedAt: time.Unix(1700000000, 0)},
			{ID: "acct-2", Email: "han@x.test", DisplayName: "Han", Status: "banned", BanReason: "spam"},
		},
		reports: []core.Report{
			{ID: "rep-1", ReporterName: "Leia", SubjectID: "acct-2", SubjectName: "Han", Kind: "harassment", Detail: "rude in chat", Status: "open", CreatedAt: time.Unix(1700000000, 0)},
			{ID: "rep-0", SubjectName: "Luke", Kind: "name", Status: "resolved", Resolution: "renamed", ResolvedBy: "acct-admin"},
		},
		resolved: map[string]string{},
	}
	s.Moderation = fm
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return mux, fm, db
}

func postForm(mux *http.ServeMux, target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Search lists accounts with the action their state allows; a ban goes to
// the core under the admin's id and into the website's audit log; a ban
// without the CSRF token, or without a reason, does nothing.
func TestAdminUsersSearchAndBan(t *testing.T) {
	mux, fm, db := newModerationAdmin(t)
	ctx := context.Background()
	_ = db.PutWebSession(ctx, store.WebSession{CoreSessionID: "sess-leia", AccountID: "acct-1"})
	_ = db.CreateAPIToken(ctx, "tok-leia", "hash-leia", "acct-1", "sess-leia", time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users?q=x.test", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Leia") || !strings.Contains(body, "Han") ||
		!strings.Contains(body, ">Ban<") || !strings.Contains(body, ">Unban<") || !strings.Contains(body, "spam") {
		t.Fatalf("search: %d %s", rec.Code, body)
	}

	rec = postForm(mux, "/admin/users/acct-1/ban", url.Values{"banned": {"1"}, "reason": {"cheating"}})
	if rec.Code != http.StatusForbidden || len(fm.bans) != 0 {
		t.Fatalf("ban without CSRF: %d, bans %v", rec.Code, fm.bans)
	}
	rec = postForm(mux, "/admin/users/acct-1/ban", url.Values{"banned": {"1"}, "reason": {" "}, "csrf": {"tok-sess-admin"}})
	if !strings.Contains(rec.Header().Get("Location"), "m=reason") || len(fm.bans) != 0 {
		t.Fatalf("ban without a reason: %s, bans %v", rec.Header().Get("Location"), fm.bans)
	}
	rec = postForm(mux, "/admin/users/acct-1/ban", url.Values{"banned": {"1"}, "reason": {"cheating"}, "q": {"leia"}, "csrf": {"tok-sess-admin"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "q=leia") {
		t.Fatalf("ban: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(fm.bans) != 1 || fm.bans[0] != (banCall{"acct-1", "cheating", "acct-admin", true}) {
		t.Fatalf("core call: %+v", fm.bans)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "account.ban" || audit[0].ActorID != "acct-admin" || !strings.Contains(audit[0].Detail, "acct-1") {
		t.Fatalf("audit: %+v", audit)
	}

	// The website's own session row and bearer for the account are gone at once.
	if _, err := db.GetWebSession(ctx, "sess-leia"); err == nil {
		t.Fatal("banned account's web session survived")
	}
	if _, err := db.APITokenByHash(ctx, "hash-leia"); err == nil {
		t.Fatal("banned account's bearer survived")
	}
	// The website keeps who banned and why; the users page lists it with no search.
	if b, err := db.GetBan(context.Background(), "acct-1"); err != nil || !b.Banned || b.Reason != "cheating" || b.Actor != "acct-admin" {
		t.Fatalf("ban record: %+v %v", b, err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	if body := rec.Body.String(); !strings.Contains(body, "Banned accounts") || !strings.Contains(body, "acct-1") || !strings.Contains(body, "cheating") {
		t.Fatalf("banned list: %s", body)
	}
	// A banned search hit shows the actor beside the core's reason.
	fm.accounts[1].ID = "acct-1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users?q=han", nil))
	if body := rec.Body.String(); !strings.Contains(body, "<code>acct-admin</code>") || !strings.Contains(body, "every network") {
		t.Fatalf("ban actor on the hit: %s", body)
	}

	postForm(mux, "/admin/users/acct-2/ban", url.Values{"banned": {"0"}, "reason": {"appeal"}, "csrf": {"tok-sess-admin"}})
	if len(fm.bans) != 2 || fm.bans[1].banned {
		t.Fatalf("unban: %+v", fm.bans)
	}
	if b, _ := db.GetBan(context.Background(), "acct-2"); b.Banned || b.Reason != "appeal" {
		t.Fatalf("unban record: %+v", b)
	}
}

// The queue lists open reports by default; resolving goes to the core under
// the admin's id and into the audit log, and needs the CSRF token.
func TestAdminReportsResolve(t *testing.T) {
	mux, fm, db := newModerationAdmin(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/reports", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "rude in chat") || strings.Contains(body, "Luke") {
		t.Fatalf("open queue: %d %s", rec.Code, body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/reports?status=all", nil))
	if !strings.Contains(rec.Body.String(), "Luke") || !strings.Contains(rec.Body.String(), "renamed") {
		t.Fatal("the all view misses the resolved report")
	}

	if rec := postForm(mux, "/admin/reports/rep-1/resolve", url.Values{"resolution": {"warned"}}); rec.Code != http.StatusForbidden || len(fm.resolved) != 0 {
		t.Fatalf("resolve without CSRF: %d", rec.Code)
	}
	rec = postForm(mux, "/admin/reports/rep-1/resolve", url.Values{"resolution": {"warned"}, "csrf": {"tok-sess-admin"}})
	if !strings.Contains(rec.Header().Get("Location"), "m=resolved") || fm.resolved["rep-1"] != "acct-admin" {
		t.Fatalf("resolve: %s %v", rec.Header().Get("Location"), fm.resolved)
	}
	rec = postForm(mux, "/admin/reports/rep-1/resolve", url.Values{"resolution": {"again"}, "csrf": {"tok-sess-admin"}})
	if !strings.Contains(rec.Header().Get("Location"), "m=already") {
		t.Fatalf("resolve twice: %s", rec.Header().Get("Location"))
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "report.resolve" {
		t.Fatalf("audit: %+v", audit)
	}
}
