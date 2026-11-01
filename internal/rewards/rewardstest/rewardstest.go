// Package rewardstest is a stand-in for the rewards service, for the website tests and page previews: one
// account whose state the caller sets, answering the user and admin routes the website uses. Nothing in the
// server imports it.
package rewardstest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openpak/website/internal/rewards"
)

// State is the account the fake answers for; the service computes owned, affordable, claimable and
// locked from it, as the real one does.
type State struct {
	Balance, Lifetime   int
	PlaySeconds         int64
	Frozen, Paused, Pub bool
	PlayPoints, PlayCap int
	Progress            map[string]int64
	Counts              map[string]int64 // running counts by mission metric (ladders); wins over Progress
	Claimed             map[string]bool
	Owned               map[string]bool
	Equipped            map[string]string
	Ledger              []map[string]any
	Down                bool // /v1/me and the admin stats answer 502
	Bearer              string
}

type Item struct {
	ID, Kind, Name, Description string
	Price                       *int
	Platforms                   []string
	Limited                     bool
}

type Mission struct {
	ID, Title, Description, Kind, Metric, Requires, RewardItem string
	Target                                                     int64
	Points                                                     int
}

var Missions = []Mission{
	{"daily-play-30", "Play for 30 minutes", "Play online for 30 minutes today.", "daily", "play_seconds", "", "", 1800, 2},
	{"weekly-4-days", "Four days of play", "Play online on four different days this week.", "weekly", "play_days", "", "", 4, 5},
	{"weekly-3-titles", "Three different games", "Play three different games online this week.", "weekly", "titles_played", "", "", 3, 5},
	{"weekly-2-networks", "Two networks", "Play on two different networks this week.", "weekly", "networks_played", "", "", 2, 5},
	{"first-online", "First steps online", "Play online for the first time.", "once", "play_seconds", "", "badge-first-link", 60, 3},
	{"first-friend", "Make a friend", "Add your first friend.", "once", "friends_added", "", "badge-first-friend", 1, 5},
	{"hours-10", "10 hours online", "Play online for 10 hours in total.", "once", "lifetime_play_seconds", "", "badge-warm-up", 36000, 20},
	{"all-networks", "Three networks", "Play on three different networks.", "once", "networks_played", "", "", 3, 30},
	{"hours-50", "50 hours online", "Play online for 50 hours in total.", "series", "lifetime_play_seconds", "hours-10", "badge-regular", 180000, 50},
}

func NewState() *State {
	return &State{PlayCap: 8, Progress: map[string]int64{}, Counts: map[string]int64{}, Claimed: map[string]bool{}, Owned: map[string]bool{}, Equipped: map[string]string{}}
}

func (st *State) items(t testing.TB) []Item {
	var raw []struct {
		ID, Kind, Name, Description string
		Price                       *int
		Platforms                   []string
		Limited                     bool
	}
	if err := json.Unmarshal([]byte(itemsJSON), &raw); err != nil {
		t.Fatal(err)
	}
	out := make([]Item, len(raw))
	for i, r := range raw {
		out[i] = Item{r.ID, r.Kind, r.Name, r.Description, r.Price, r.Platforms, r.Limited}
	}
	return out
}

// artFor is an item's preview: the real art from REWARDS_ART_DIR (research/rewards/art/export/preview) when
// that is set and has the file, otherwise a flat generated tile, so the tests need no files.
func artFor(id string) ([]byte, string) {
	if dir := os.Getenv("REWARDS_ART_DIR"); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, id+".png")); err == nil {
			return b, "image/png"
		}
	}
	hue := 0
	for _, c := range id {
		hue = (hue*31 + int(c)) % 360
	}
	return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256"><rect width="256" height="256" fill="hsl(%d 35%% 55%%)"/><circle cx="128" cy="128" r="70" fill="hsl(%d 40%% 85%%)"/></svg>`, hue, hue)), "image/svg+xml"
}

// New starts the fake and returns a client for it; it answers the calls the pages and the admin page make.
func New(t testing.TB, st *State) *rewards.Client {
	t.Helper()
	items := st.items(t)
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	mux := http.NewServeMux()
	item := func(it Item, withOwn bool) map[string]any {
		m := map[string]any{"id": it.ID, "kind": it.Kind, "name": it.Name, "description": it.Description, "price": it.Price,
			"platforms": it.Platforms, "limited": it.Limited, "has_preview": it.Kind != "title"}
		if withOwn {
			m["owned"] = st.Owned[it.ID]
			m["affordable"] = it.Price != nil && st.Balance >= *it.Price
		}
		return m
	}
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
		st.Bearer = r.Header.Get("Authorization")
		if st.Down {
			http.Error(w, `{"error":"down"}`, http.StatusBadGateway)
			return
		}
		write(w, map[string]any{
			"account":  map[string]any{"account_id": "a", "balance": st.Balance, "lifetime_earned": st.Lifetime, "lifetime_play_seconds": st.PlaySeconds, "frozen": st.Frozen, "paused": st.Paused, "showcase_public": st.Pub},
			"today":    map[string]any{"day": "2026-11-02", "play_points": st.PlayPoints, "play_cap": st.PlayCap},
			"equipped": st.Equipped,
		})
	})
	mux.HandleFunc("GET /v1/me/missions", func(w http.ResponseWriter, r *http.Request) {
		var out []map[string]any
		for _, m := range Missions {
			p := st.Progress[m.ID]
			if c, ok := st.Counts[m.Metric]; ok {
				p = c
			}
			done := p >= m.Target
			locked := m.Requires != "" && !st.Claimed[m.Requires]
			period := "2026-11-02"
			if m.Kind == "weekly" {
				period = "2026-W44"
			}
			if m.Kind == "once" || m.Kind == "series" {
				period = "once"
			}
			mm := map[string]any{"id": m.ID, "title": m.Title, "description": m.Description, "kind": m.Kind, "metric": m.Metric, "target": m.Target,
				"reward_points": m.Points, "period": period, "progress": p, "completed": done, "claimed": st.Claimed[m.ID],
				"claimable": done && !st.Claimed[m.ID] && !locked, "locked": locked}
			if m.RewardItem != "" {
				mm["reward_item"] = m.RewardItem
			}
			out = append(out, mm)
		}
		write(w, map[string]any{"missions": out})
	})
	mux.HandleFunc("GET /v1/me/catalog", func(w http.ResponseWriter, r *http.Request) {
		var out []map[string]any
		for _, it := range items {
			if it.Price != nil || st.Owned[it.ID] {
				out = append(out, item(it, true))
			}
		}
		write(w, map[string]any{"items": out})
	})
	mux.HandleFunc("GET /v1/me/inventory", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		for _, it := range items {
			if st.Owned[it.ID] {
				m := item(it, false)
				m["equipped"] = st.Equipped[it.Kind] == it.ID
				out = append(out, m)
			}
		}
		write(w, map[string]any{"items": out})
	})
	mux.HandleFunc("GET /v1/me/ledger", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"entries": append([]map[string]any{}, st.Ledger...)})
	})
	mux.HandleFunc("POST /v1/me/redeem", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ItemID string `json:"item_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		it, ok := byID[in.ItemID]
		switch {
		case !ok:
			http.Error(w, `{"error":"unknown item"}`, http.StatusNotFound)
		case st.Frozen:
			http.Error(w, `{"error":"frozen"}`, http.StatusForbidden)
		case st.Owned[in.ItemID] || it.Price == nil:
			http.Error(w, `{"error":"already owned"}`, http.StatusConflict)
		case st.Balance < *it.Price:
			http.Error(w, `{"error":"not enough points"}`, http.StatusPaymentRequired)
		default:
			st.Balance -= *it.Price
			st.Owned[in.ItemID] = true
			w.WriteHeader(http.StatusCreated)
			write(w, map[string]any{"item_id": in.ItemID, "price": *it.Price, "balance": st.Balance})
		}
	})
	mux.HandleFunc("POST /v1/me/missions/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		for _, m := range Missions {
			if m.ID == r.PathValue("id") {
				pts := m.Points
				if st.Claimed[m.ID] {
					pts = 0
				}
				if !st.Claimed[m.ID] && m.RewardItem != "" {
					st.Owned[m.RewardItem] = true // the badge comes with the claim
				}
				st.Claimed[m.ID] = true
				st.Balance += pts
				write(w, map[string]any{"mission_id": m.ID, "points": pts, "balance": st.Balance})
				return
			}
		}
		http.Error(w, `{"error":"unknown mission"}`, http.StatusNotFound)
	})
	mux.HandleFunc("PUT /v1/me/equipped/{slot}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ItemID string `json:"item_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		st.Equipped[r.PathValue("slot")] = in.ItemID
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/me/equipped/{slot}", func(w http.ResponseWriter, r *http.Request) {
		delete(st.Equipped, r.PathValue("slot"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /v1/me/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Showcase bool `json:"showcase_public"`
			Paused   bool `json:"paused"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		st.Pub, st.Paused = in.Showcase, in.Paused
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/items/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := byID[r.PathValue("id")]; !ok {
			http.NotFound(w, r)
			return
		}
		b, ct := artFor(r.PathValue("id"))
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /internal/v1/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Key") != "adm" || r.Header.Get("X-Admin-Actor") == "" {
			http.Error(w, "{}", http.StatusUnauthorized)
			return
		}
		if st.Down {
			http.Error(w, `{"error":"down"}`, http.StatusBadGateway)
			return
		}
		write(w, map[string]any{"accounts": 214, "points_issued": 18420, "points_spent": 6350, "issued_today": 412, "open_flags": 2, "frozen_accounts": 1,
			"redemptions_by_item": map[string]int{"icon-tabby": 14, "badge-launch-season": 31, "theme-graphite": 6, "icon-pak": 9}})
	})
	mux.HandleFunc("GET /internal/v1/admin/flags", func(w http.ResponseWriter, r *http.Request) {
		flags := []map[string]any{
			{"id": 9, "account_id": "acct-4f2a", "reason": "play-cap-streak", "detail": "9 days at the cap", "raised_at": "2026-11-01T10:00:00Z"},
			{"id": 8, "account_id": "acct-77c1", "reason": "elapsed-clamp", "detail": "3 polls clamped today", "raised_at": "2026-10-31T21:12:00Z"},
		}
		if r.URL.Query().Get("all") == "1" {
			flags = append(flags, map[string]any{"id": 5, "account_id": "acct-0be9", "reason": "play-cap-streak", "raised_at": "2026-10-20T08:00:00Z",
				"resolved_by": "acct-admin", "resolved_at": "2026-10-21T09:30:00Z"})
		}
		write(w, map[string]any{"flags": flags})
	})
	mux.HandleFunc("POST /internal/v1/admin/flags/{id}/resolve", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return rewards.New(srv.URL, "int", "adm")
}

func LedgerLine(delta int, kind, source, ref string, ago time.Duration) map[string]any {
	return map[string]any{"id": 1, "delta": delta, "kind": kind, "source": source, "ref": ref, "created_at": time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).Add(-ago).Format(time.RFC3339)}
}
