package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/netmap"
	"github.com/openpak/website/internal/store"
)

func TestNetworkMapGateAndData(t *testing.T) {
	s, db, _ := newAdmin(t)
	s.NetMap = &netmap.Service{
		Topology: func(context.Context) []netmap.HostTopo {
			return []netmap.HostTopo{{Name: "prod", IP: "203.0.113.1", Raw: netmap.Raw{Host: "prod", Services: []netmap.RawNode{
				{Name: "openpak-account", Unit: "openpak-account.service", Description: "OpenPak account core", State: "running", Ports: []netmap.RawPort{{IP: "127.0.0.1", Port: 20001}}},
				{Name: "openpak-nx-baas", Unit: "openpak-nx-baas.service", Description: "OpenPak Switch adapter", State: "running",
					Refs: []netmap.RawRef{{Var: "ACCOUNT_ADDR", Host: "127.0.0.1", Port: 20001}}},
			}}}}
		},
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec
	}
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-normal"})
	for _, p := range []string{"/admin/network", "/admin/network/data"} {
		if c := get(p).Code; c != http.StatusForbidden {
			t.Fatalf("%s: a non-admin got %d", p, c)
		}
	}
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin"})
	page := get("/admin/network")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-url="/admin/network/data"`) || !strings.Contains(page.Body.String(), "Network map") {
		t.Fatalf("page: %d", page.Code)
	}
	data := get("/admin/network/data")
	body := data.Body.String()
	if data.Code != 200 || !strings.Contains(body, `"id":"nx-baas"`) || !strings.Contains(body, `"from":"nx-baas","to":"account"`) ||
		!strings.Contains(body, `"logs":"openpak-nx-baas"`) {
		t.Fatalf("data: %d %s", data.Code, body)
	}
	if data.Header().Get("Cache-Control") != "private, no-store" {
		t.Error("the admin graph must never be cached by a shared cache")
	}
	s.NetMap = nil
	if c := get("/admin/network/data").Code; c != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: %d", c)
	}
}
