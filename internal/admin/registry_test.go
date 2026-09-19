package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryAdmin(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/status" || r.Header.Get("Authorization") != "Bearer reg-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Exactly the agreed shape, plus a field this page does not know.
		_, _ = w.Write([]byte(`{"channels":[{"name":"stable","revision":"rev-2","sequence":7,"updated_at":"2026-09-18T12:00:00Z","signed_by":"x"}],
			"revisions":[{"id":"rev-2","policy_id":"pol-1","created_at":"2026-09-18T11:00:00Z","withdrawn_at":null},
			             {"id":"rev-1","policy_id":"pol-1","created_at":"2026-09-01T11:00:00Z","withdrawn_at":"2026-09-02T00:00:00Z"}],
			"clients":[{"id":"c1","name":"Citron","status":"active"}]}`))
	}))
	defer svc.Close()

	s, _, mux := adminMux(t)
	if rec := get(mux, "/admin/registry"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("registry unset: %d", rec.Code)
	}
	s.Registry = NewRegistryAPI(svc.URL, "reg-key")
	rec := get(mux, "/admin/registry")
	body := rec.Body.String()
	for _, want := range []string{"stable", "rev-2", ">7<", "Withdrawn 2026-09-02", "Citron"} {
		if !strings.Contains(body, want) {
			t.Errorf("registry page missing %q", want)
		}
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("registry: %d", rec.Code)
	}

	// A wrong key is the page's error notice, not a 500.
	s.Registry = NewRegistryAPI(svc.URL, "wrong")
	if rec := get(mux, "/admin/registry"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "401") {
		t.Fatalf("registry refused: %d", rec.Code)
	}
}
