package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

// A fake openpak-opsd: two services on two networks, one with a newer image.
func fakeOpsd(t *testing.T, got *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ops-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			*got = append(*got, r.URL.Path+" by "+r.Header.Get("X-OpenPak-Actor"))
			_, _ = w.Write([]byte(`{"ok":true,"message":"done"}`))
			return
		}
		_, _ = w.Write([]byte(`{"host":"prod","checked_at":"2026-09-19T10:00:00Z","services":[
			{"name":"openpak-switch-nexd","unit":"openpak-switch-nexd.service","image":"ghcr.io/openpak/switch-nex:latest","state":"running","health":"none","started_at":null,"networks":["switch"],"update_available":true,"protected":false},
			{"name":"openpak-nn-friends","unit":"openpak-nn-friends.service","image":"ghcr.io/openpak/nn-friends:latest","state":"exited","health":"none","started_at":null,"networks":["wiiu","3ds"],"update_available":false,"protected":false}]}`))
	}))
}

func TestServicesPage(t *testing.T) {
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{
		CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root", Email: "root@x.test",
	})
	var posts []string
	ops := fakeOpsd(t, &posts)
	defer ops.Close()
	s.Ops = NewOpsAPI("prod="+ops.URL+",gone=http://127.0.0.1:1", "ops-key")
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	get := func(path string) string {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	body := get("/admin/services")
	for _, want := range []string{"openpak-switch-nexd", "openpak-nn-friends", "Update available", "1 update<", "gone"} {
		if !strings.Contains(body, want) {
			t.Errorf("services page is missing %q", want)
		}
	}
	// The network dropdown narrows the view to that network's containers.
	if body := get("/admin/services?network=switch"); strings.Contains(body, "openpak-nn-friends") {
		t.Error("switch filter still shows a Wii U/3DS service")
	}

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/services/action", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	base := url.Values{"host": {"prod"}, "name": {"openpak-switch-nexd"}}
	with := func(kv ...string) url.Values {
		v := url.Values{}
		for k, vs := range base {
			v[k] = vs
		}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	if rec := post(with("action", "restart")); rec.Code != http.StatusForbidden {
		t.Errorf("an action without the CSRF token must be refused, got %d", rec.Code)
	}
	if rec := post(with("action", "stop", "csrf", "tok-sess-admin")); rec.Code != http.StatusBadRequest {
		t.Errorf("stop without the tick box must be refused, got %d", rec.Code)
	}
	if rec := post(with("action", "restart", "csrf", "tok-sess-admin")); rec.Code != http.StatusSeeOther {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body.String())
	}
	if len(posts) != 1 || posts[0] != "/v1/services/openpak-switch-nexd/restart by root@x.test" {
		t.Errorf("the agent received %v", posts)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) == 0 || audit[0].Action != "service.restart" || audit[0].Detail != "prod/openpak-switch-nexd" {
		t.Errorf("restart was not audited: %+v", audit)
	}
}
