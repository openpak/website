package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/rewards/rewardstest"
	"github.com/openpak/website/internal/store"
)

func TestRewardsAPI(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := mintFor(t, s, "a@x.test", "password-A-1")

	if rec := do(t, s, http.MethodGet, "/api/v1/me/rewards", tok, nil); rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), "rewards_unavailable") {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	st := rewardstest.NewState()
	st.Balance, st.Lifetime, st.PlayPoints = 120, 130, 3
	st.Progress["daily-play-30"] = 900
	st.Progress["first-online"] = 120
	st.Owned["badge-founder"] = true
	st.Equipped["badge"] = "badge-founder"
	st.Ledger = append(st.Ledger, rewardstest.LedgerLine(2, "earn", "mission", "daily-play-30:2026-11-02", time.Hour))
	cli := rewardstest.New(t, st)
	s.Rewards, s.Pages = cli, &pages.Server{Rewards: cli}

	if rec := do(t, s, http.MethodGet, "/api/v1/me/rewards", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", rec.Code)
	}
	rec := do(t, s, http.MethodGet, "/api/v1/me/rewards", tok, nil)
	var v pages.RewardsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	if v.Balance != 120 || v.TodayPoints != 3 || v.TodayCap != 8 || v.Ready != 1 || len(v.ReadyTiles) != 1 || len(v.Ledger) != 1 {
		t.Fatalf("header: %+v", v)
	}
	if v.ReadyTiles[0].MissionID == "" || !strings.HasPrefix(v.ReadyTiles[0].Img, "/") || v.ReadyTiles[0].ClaimText == "" {
		t.Fatalf("tile: %+v", v.ReadyTiles[0])
	}
	if v.Badge == nil || v.Badge.ID != "badge-founder" {
		t.Fatalf("badge: %+v", v.Badge)
	}
	for _, g := range v.Shop {
		for _, it := range g.Items {
			if it.Owned || it.Price == nil {
				t.Errorf("shop lists %+v", it)
			}
		}
	}

	// Claim, buy, equip and settings go through the account's own session.
	rec = do(t, s, http.MethodPost, "/api/v1/me/rewards/claim", tok, map[string]string{"mission": v.ReadyTiles[0].MissionID, "period": v.ReadyTiles[0].Period})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"points"`) {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/rewards/equip", tok, map[string]string{"slot": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("equip without slot: %d", rec.Code)
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/rewards/equip", tok, map[string]string{"slot": "badge", "item": "badge-founder"}); rec.Code != 200 {
		t.Fatalf("equip: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/rewards/settings", tok, map[string]bool{"showcase": true, "paused": false}); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodGet, "/api/v1/me/rewards/theme", tok, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"theme":null`) {
		t.Fatalf("theme: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/rewards/redeem", tok, map[string]string{"item": "no-such-item"}); rec.Code == 200 {
		t.Fatalf("redeem of nothing: %d", rec.Code)
	}
}

func TestDiscordAPI(t *testing.T) {
	s, _, db := newTestAPI(t)
	tok := mintFor(t, s, "a@x.test", "password-A-1")
	rec := do(t, s, http.MethodGet, "/api/v1/me/discord", tok, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"configured":false`) || !strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body.String())
	}
	var who struct{ AccountID string }
	rec = do(t, s, http.MethodGet, "/api/v1/me", tok, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &who)
	if err := db.LinkDiscord(context.Background(), store.DiscordLink{AccountID: "acct-a@x.test", DiscordID: "123456789012345", Username: "pak"}); err != nil {
		t.Fatal(err)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/discord", tok, nil)
	if !strings.Contains(rec.Body.String(), `"username":"pak"`) {
		t.Fatalf("linked: %s", rec.Body.String())
	}
	if rec = do(t, s, http.MethodDelete, "/api/v1/me/discord", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if rec = do(t, s, http.MethodGet, "/api/v1/me/discord", tok, nil); !strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Fatalf("after unlink: %s", rec.Body.String())
	}
}

type opFed struct{ core.Federation }

func (opFed) NodeRequirements(context.Context) (core.NodeRequirements, error) {
	return core.NodeRequirements{NodeCores: 8, NodeRAMGB: 16, NodeDiskGB: 160, ServerCores: 2, ServerRAMGB: 4, ServerBandwidthMbps: 100, Roles: []string{"relay", "game"}}, nil
}
func (opFed) ListLocations(context.Context) ([]core.NodeLocation, error) {
	return []core.NodeLocation{{Code: "br-sp", Country: "BR", City: "Sao Paulo", Accepting: true}, {Code: "ru-msk", Country: "RU", Accepting: true}}, nil
}

func TestOperatorAPI(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := mintFor(t, s, "a@x.test", "password-A-1")
	if rec := do(t, s, http.MethodGet, "/api/v1/me/operator", tok, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("off: %d", rec.Code)
	}
	s.Federation, s.Operators = opFed{}, &store.MemoryOperators{}
	rec := do(t, s, http.MethodGet, "/api/v1/me/operator", tok, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"br-sp"`) || !strings.Contains(rec.Body.String(), `"node_cores":8`) {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	good := map[string]any{"location": "br-sp", "city": "Sao Paulo", "provider": "Hetzner", "email": "op@x.test", "terms": true,
		"servers": []map[string]any{{"cores": 8, "ram_gb": 16, "disk_gb": 200, "disk_ssd": true, "bandwidth_mbps": 500, "roles": []string{"relay"}}}}
	bad := map[string]any{"location": "br-sp", "city": "x", "provider": "y", "email": "nope", "terms": false}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator", tok, bad); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"errors"`) {
		t.Fatalf("bad: %d %s", rec.Code, rec.Body.String())
	}
	ru := map[string]any{"location": "ru-msk"}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator", tok, ru); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("excluded: %d", rec.Code)
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator", tok, good); rec.Code != http.StatusCreated {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator", tok, good); rec.Code != http.StatusConflict {
		t.Fatalf("second: %d", rec.Code)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/operator", tok, nil)
	if !strings.Contains(rec.Body.String(), `"in_review":true`) || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("after: %s", rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator/waitlist", tok, map[string]string{"country": "PT", "email": "op@x.test"}); rec.Code != http.StatusCreated {
		t.Fatalf("waitlist: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodPost, "/api/v1/me/operator/waitlist", tok, map[string]string{"country": "CN", "email": "op@x.test"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("waitlist CN: %d", rec.Code)
	}
}

func TestMeListsNNLinksAndMarksCurrentSession(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.nnLinks = map[string]map[string]string{"wiiu": {"acct-a@x.test": "wiiu-subject"}, "3ds": {"acct-a@x.test": "ds-subject"}}
	tok := mintFor(t, s, "a@x.test", "password-A-1")
	rec := do(t, s, http.MethodGet, "/api/v1/me", tok, nil)
	if !strings.Contains(rec.Body.String(), `"namespace":"wiiu"`) || !strings.Contains(rec.Body.String(), `"namespace":"3ds"`) {
		t.Fatalf("me: %s", rec.Body.String())
	}
	if rec = do(t, s, http.MethodDelete, "/api/v1/me/links/wiiu", tok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("wiiu unlink: %d", rec.Code)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/sessions", tok, nil)
	if !strings.Contains(rec.Body.String(), `"current":true`) {
		t.Fatalf("sessions: %s", rec.Body.String())
	}
}

func TestRequestsCarryVerseURLForServedTitles(t *testing.T) {
	q := store.Request{Kind: "title", Name: "Mario Kart Wii", Status: "beta"}
	if m := requestJSON(q); !strings.HasPrefix(m["verse_url"].(string), "https://verse.openpak.org/communities/") {
		t.Fatalf("%v", m)
	}
	q.Status = "planned"
	if _, ok := requestJSON(q)["verse_url"]; ok {
		t.Fatal("unserved title has a community")
	}
}

// Every place an account appears carries its picture, as /me does: fixtures once had avatar_url the live API lacked.
func TestAccountsCarryAvatarURL(t *testing.T) {
	s, fc, db := newTestAPI(t)
	st, err := media.NewFSStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.New(st)
	fc.friends["acct-a@x.test"] = []string{"acct-f"}
	fc.incoming = []core.Friend{{AccountID: "acct-in", DisplayName: "In"}}
	fc.outgoing = []core.Friend{{AccountID: "acct-out", DisplayName: "Out"}}
	fc.blocks = append(fc.blocks, [2]string{"acct-a@x.test", "acct-bl"})
	fc.invitations = []core.PendingInvitation{{ID: "i1", FromAccount: "acct-inv", FromName: "Inv", ExpiresAt: time.Now().Add(time.Hour)}}
	tok := mintFor(t, s, "a@x.test", "password-A-1")
	_ = db
	for path, want := range map[string]string{
		"/api/v1/me/friends":          "/media/avatars/acct-f/256",
		"/api/v1/me/friends/requests": "/media/avatars/acct-in/256",
		"/api/v1/me/blocked":          "/media/avatars/acct-bl/256",
		"/api/v1/me/invitations":      "/media/avatars/acct-inv/256",
	} {
		if body := do(t, s, http.MethodGet, path, tok, nil).Body.String(); !strings.Contains(body, want) {
			t.Errorf("%s lacks %s: %s", path, want, body)
		}
	}
	if body := do(t, s, http.MethodGet, "/api/v1/me/friends/requests", tok, nil).Body.String(); !strings.Contains(body, "/media/avatars/acct-out/256") {
		t.Errorf("outgoing lacks avatar_url: %s", body)
	}
}

func TestEveryTitleCarriesArtURL(t *testing.T) {
	sw := store.Request{Kind: "title", Name: "Splatoon 3", Console: "Switch", TitleID: "0100C2500FC20000", Status: "live"}
	if m := requestJSON(sw); m["art_url"] != "/titles/icon/0100C2500FC20000" {
		t.Fatalf("switch icon: %v", m["art_url"])
	}
	other := store.Request{Kind: "title", Name: "A Game With No Tile", Console: "Wii", Status: "live"}
	if v, ok := requestJSON(other)["art_url"]; !ok || v != "" {
		t.Fatalf("a title with no art still carries art_url (empty): %v %v", v, ok)
	}
	if _, ok := requestJSON(store.Request{Kind: "feature", Name: "Chat"})["art_url"]; ok {
		t.Fatal("features have no art")
	}
}
