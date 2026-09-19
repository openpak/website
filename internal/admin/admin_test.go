package admin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

type fakeEvents struct{ called bool }

func (f *fakeEvents) Poll(_ context.Context, _ uint64, _ int32) ([]core.Event, uint64, error) {
	f.called = true
	return []core.Event{{Version: 1, EventID: "e1", Type: "account_banned", AccountID: "acct-x"}}, 1, nil
}

type fakeSessions struct{ id string }

func (f *fakeSessions) CoreSessionID(_ *http.Request) string { return f.id }
func (f *fakeSessions) CSRFToken(id string) string           { return "tok-" + id }
func (f *fakeSessions) CheckCSRF(id, tok string) bool        { return tok != "" && tok == "tok-"+id }

func newAdmin(t *testing.T) (*Server, *store.Memory, *fakeEvents) {
	t.Helper()
	tpl, err := pages.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	db := store.NewMemory()
	ev := &fakeEvents{}
	s := New(tpl, ev, db, &fakeSessions{id: "sess-admin"}, []string{"acct-admin"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, db, ev
}

func TestAdminGateFailClosed(t *testing.T) {
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{
		CoreSessionID: "sess-admin", AccountID: "acct-normal", DisplayName: "Norm",
	})
	rec := httptest.NewRecorder()
	s.RegisterRoutes(http.NewServeMux())
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin must get 403, got %d", rec.Code)
	}
}

func TestAdminConfigAndEvents(t *testing.T) {
	s, db, ev := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{
		CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root",
	})
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	// Dashboard allowed for the listed admin.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin dashboard: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="csrf" value="tok-sess-admin"`) {
		t.Fatal("admin pages must carry the CSRF token (sign-out form)")
	}

	// A forged post without the token is refused and changes nothing.
	forged := url.Values{"registrations_open": {"on"}, "banner": {"Pwned"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config", strings.NewReader(forged.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if cfg, _ := db.GetSiteConfig(context.Background()); rec.Code != http.StatusForbidden || cfg.BannerMessage != "" {
		t.Fatalf("post without csrf must be 403 and not saved: %d %+v", rec.Code, cfg)
	}

	// Save config → audit entry written.
	form := url.Values{"registrations_open": {"on"}, "banner": {"Hello"}, "csrf": {"tok-sess-admin"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("config save: %d", rec.Code)
	}
	cfg, _ := db.GetSiteConfig(context.Background())
	if !cfg.RegistrationsOpen || cfg.BannerMessage != "Hello" {
		t.Fatalf("config not saved: %+v", cfg)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "site_config.update" {
		t.Fatalf("audit missing: %+v", audit)
	}

	// Events page polls core Events (audit log, WEB-06).
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/events", nil))
	if rec.Code != http.StatusOK || !ev.called || !strings.Contains(rec.Body.String(), "account_banned") {
		t.Fatalf("events page: %d called=%v", rec.Code, ev.called)
	}

	// Users and reports pages render the pending-contract notices.
	for _, path := range []string{"/admin/users", "/admin/reports"} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CR-") {
			t.Fatalf("%s must render pending-contract notice: %d", path, rec.Code)
		}
	}
}
