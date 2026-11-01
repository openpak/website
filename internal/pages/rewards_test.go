package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards"
)

// fakeRewards answers the few calls the pages make, and records the bearer it was given.
func fakeRewards(t *testing.T, bearer *string) *rewards.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
		*bearer = r.Header.Get("Authorization")
		w.Write([]byte(`{"account":{"account_id":"a","balance":42,"lifetime_earned":130},"today":{"play_points":3,"play_cap":8},"equipped":{"badge":"b1"}}`))
	})
	mux.HandleFunc("GET /v1/me/missions", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"missions":[{"id":"daily-play-30","title":"Play for 30 minutes","kind":"daily","metric":"play_seconds","target":1800,"progress":900,"period":"2026-11-02","reward_points":2}]}`))
	})
	mux.HandleFunc("GET /v1/me/catalog", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[{"id":"b1","kind":"badge","name":"First Steps","price":50,"affordable":false}]}`))
	})
	mux.HandleFunc("GET /v1/me/inventory", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"items":[]}`)) })
	mux.HandleFunc("GET /v1/me/ledger", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"entries":[]}`)) })
	mux.HandleFunc("POST /v1/me/redeem", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not enough points"}`, http.StatusPaymentRequired)
	})
	mux.HandleFunc("GET /internal/v1/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Key") != "adm" || r.Header.Get("X-Admin-Actor") == "" {
			http.Error(w, "{}", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"accounts":3,"open_flags":1}`))
	})
	mux.HandleFunc("GET /internal/v1/admin/flags", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"flags":[{"id":9,"account_id":"acct-x","reason":"play-cap-streak","raised_at":"2026-11-01T10:00:00Z"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return rewards.New(srv.URL, "int", "adm")
}

func TestRewardsPage(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	w := authedRequest(s, rec, http.MethodGet, "/account/rewards", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "not available") {
		t.Fatalf("no backend: %d", w.Code)
	}
	var bearer string
	s.Rewards = fakeRewards(t, &bearer)
	w = authedRequest(s, rec, http.MethodGet, "/account/rewards", nil)
	body := w.Body.String()
	for _, want := range []string{"42", "Play for 30 minutes", "15 min / 30 min", "First Steps", "50 points"} {
		if !strings.Contains(body, want) {
			t.Errorf("rewards page lacks %q", want)
		}
	}
	if !strings.HasPrefix(bearer, "Bearer ") || len(bearer) < 10 {
		t.Errorf("the service was not given the session: %q", bearer)
	}
	// An unaffordable redeem comes back as a message, not an error page.
	w = authedRequest(s, rec, http.MethodPost, "/account/rewards/redeem", &url.Values{"item": {"b1"}})
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "m=nopoints") {
		t.Errorf("redeem: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAdminRewards(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	var bearer string
	s.Rewards = fakeRewards(t, &bearer)
	rec := login(t, s, "a@x.test", "password-A-1")
	if w := authedRequest(s, rec, http.MethodGet, "/admin/rewards", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a non-admin sees the queue: %d", w.Code)
	}
	s.AdminAccountIDs = []string{"acct-a@x.test"}
	w := authedRequest(s, rec, http.MethodGet, "/admin/rewards", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "play-cap-streak") {
		t.Fatalf("admin queue: %d %s", w.Code, w.Body.String())
	}
}
