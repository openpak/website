package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/store"
)

// adminMux is an admin server with a signed-in admin, ready to serve.
func adminMux(t *testing.T) (*Server, *store.Memory, *http.ServeMux) {
	t.Helper()
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{
		CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root",
	})
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return s, db, mux
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The monitor renders what answers, what does not, and what is not configured
// — and a missing backend is never a 500.
func TestMonitor(t *testing.T) {
	for _, k := range []string{"MHGU_HEALTH_URL", "PHOTON_HEALTH_URL", "SPLATOON3_HEALTH_URL", "STARDEW_HEALTH_URL"} {
		t.Setenv(k, "off")
	}
	statusPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Headline":"x","State":"some","Groups":[{"Title":"On the web","Services":[
			{"Name":"Website","Up":true,"Uptime":99.5,"Checked":"1m ago","Bars":[]},
			{"Name":"Account API","Up":false,"Uptime":80,"Checked":"2m ago"}]}]}`))
	}))
	defer statusPage.Close()
	savesSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	defer savesSvc.Close()

	s, _, mux := adminMux(t)
	s.StatusAPI = statusPage.URL
	s.Saves = saves.New(savesSvc.URL, "k")
	rec := get(mux, "/admin/monitor")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("monitor: %d", rec.Code)
	}
	for _, want := range []string{
		`state-row ok"><span class="name">On the web · Website`,
		`state-row bad"><span class="name">On the web · Account API`,
		`state-row ok"><span class="name">Saves service`,
		`state-row idle"><span class="name">Chat service</span><span class="pill break-anywhere">Not configured`,
		`aria-current="page">Monitor`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("monitor missing %q", want)
		}
	}

	// The availability page being down is its own amber row, not a failure.
	statusPage.Close()
	if rec := get(mux, "/admin/monitor"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `state-row warn"><span class="name">Availability page`) {
		t.Fatalf("unreachable status page: %d", rec.Code)
	}
}
