package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards/rewardstest"
	"github.com/openpak/website/internal/store"
)

func TestAdminRewards(t *testing.T) {
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin"})
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	// Without the service the section is not in the nav and the page is not there.
	if rec := get(mux, "/admin/rewards"); rec.Code != http.StatusNotFound {
		t.Fatalf("no service: %d", rec.Code)
	}
	if rec := get(mux, "/admin/users"); strings.Contains(rec.Body.String(), "/admin/rewards") {
		t.Error("the nav shows a section with no backend")
	}

	st := rewardstest.NewState()
	s.Rewards = rewardstest.New(t, st)
	rec := get(mux, "/admin/rewards")
	body := rec.Body.String()
	for _, want := range []string{"settings-nav", "Sat at the play cap for days", "Play time clamped repeatedly", "acct-4f2a", "18420", "icon-tabby", `href="/admin/rewards"`, "Resolve"} {
		if rec.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("admin rewards lacks %q (%d)", want, rec.Code)
		}
	}
	if !strings.Contains(get(mux, "/admin/users").Body.String(), `href="/admin/rewards"`) {
		t.Error("the nav lacks Rewards")
	}
	if !strings.Contains(get(mux, "/admin/rewards?all=1").Body.String(), "acct-0be9") {
		t.Error("all=1 does not list the resolved flag")
	}

	// Resolving needs the CSRF token and goes back to the queue with a message.
	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/rewards/flags/resolve", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(url.Values{"id": {"9"}}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	rec = post(url.Values{"id": {"9"}, "note": {"checked"}, "csrf": {"tok-sess-admin"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/rewards?m=resolved" {
		t.Errorf("resolve: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(get(mux, "/admin/rewards?m=resolved").Body.String(), "Flag resolved.") {
		t.Error("no confirmation after resolving")
	}

	// A service that fails says so inside the shell.
	st.Down = true
	rec = get(mux, "/admin/rewards")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "did not answer") {
		t.Errorf("failed service: %d", rec.Code)
	}
}
