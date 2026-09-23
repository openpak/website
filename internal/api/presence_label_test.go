package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// The contract the app builds "<title_name> on <label>" from: every online
// friend carries platform, client, label and title_name, flat on the row and
// again under "presence", beside the fields that were always there.
func TestFriendsPresenceCarriesLabelAndTitleName(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.accounts["b@x.test"] = "password-B-1"
	fc.accounts["c@x.test"] = "password-C-1"
	fc.friends["acct-a@x.test"] = []string{"acct-b@x.test", "acct-c@x.test"}
	fc.presence = map[string]core.Presence{
		"acct-b@x.test": {Namespace: "switch", TitleID: "0100E65002BB8000", Client: "ryujinx", Since: time.Now()},
		"acct-c@x.test": {Namespace: "wiiu", Since: time.Now()},
	}
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)
	rec = do(t, s, http.MethodGet, "/api/v1/me/friends", minted.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("friends: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Friends []map[string]any `json:"friends"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, f := range body.Friends {
		byID[f["account_id"].(string)] = f
	}
	want := map[string]map[string]any{
		"acct-b@x.test": {"platform": "switch", "client": "ryujinx", "label": "Ryujinx",
			"title_name": "Stardew Valley", "title_id": "0100E65002BB8000", "namespace": "switch"},
		"acct-c@x.test": {"platform": "wiiu", "client": "", "label": "Wii U", "title_name": "", "title_id": ""},
	}
	for id, fields := range want {
		row := byID[id]
		if row == nil || row["online"] != true {
			t.Fatalf("%s missing or offline: %s", id, rec.Body.String())
		}
		nested, _ := row["presence"].(map[string]any)
		for k, v := range fields {
			if row[k] != v || nested[k] != v {
				t.Errorf("%s %s = %v / presence.%s = %v, want %v", id, k, row[k], k, nested[k], v)
			}
		}
		if _, ok := row["since"]; !ok {
			t.Errorf("%s lost since", id)
		}
	}
}

func TestInternalTitleName(t *testing.T) {
	s, _, _ := newTestAPI(t)
	if rec := doRaw(t, s, http.MethodGet, "/internal/titles/name?title_id=0100E65002BB8000", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured = %d", rec.Code)
	}
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	if rec := doRaw(t, s, http.MethodGet, "/internal/titles/name?title_id=0100E65002BB8000", "Bearer nope", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d", rec.Code)
	}
	rec := doRaw(t, s, http.MethodGet, "/internal/titles/name?namespace=switch&title_id=0100e65002bb8000", "Bearer k", nil)
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body.String())
	}
	var out struct{ Name string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Name != "Stardew Valley" {
		t.Fatalf("name = %q", out.Name)
	}
}

func TestSwitchFriendsAreLabelled(t *testing.T) {
	s, _, _ := newTestAPI(t)
	out := map[string]any{"friends": []any{
		map[string]any{"presence": map[string]any{"namespace": "switch", "client": "eden", "app_id": "0100E65002BB8000", "status": 2.0}},
		map[string]any{"presence": map[string]any{"namespace": "3ds", "app_id": "", "status": 1.0}},
		map[string]any{"presence": map[string]any{"namespace": "", "status": 0.0}},
	}}
	s.labelSwitchFriends(t.Context(), out)
	fr := out["friends"].([]any)
	p0 := fr[0].(map[string]any)["presence"].(map[string]any)
	if p0["label"] != "Eden" || p0["title_name"] != "Stardew Valley" || p0["platform"] != "switch" {
		t.Errorf("switch friend: %v", p0)
	}
	p1 := fr[1].(map[string]any)["presence"].(map[string]any)
	if p1["label"] != "3DS" || p1["client"] != "" || p1["title_name"] != "" {
		t.Errorf("3ds friend: %v", p1)
	}
	if p2 := fr[2].(map[string]any)["presence"].(map[string]any); p2["label"] != "" {
		t.Errorf("offline friend labelled: %v", p2)
	}
}

// A Wii U or 3DS game has an id per region, and a console on an update can report the
// update's id: every one of them names the game.
func TestInternalTitleNameAnyRegion(t *testing.T) {
	s, _, db := newTestAPI(t)
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	if _, err := db.CreateRequest(t.Context(), store.Request{Kind: "title", Name: "Mario Kart 7", Console: "3DS", Status: "beta",
		TitleIDs: []string{"0004000000030800", "0004000000030700", "0004000000030600"}}, "t"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0004000000030800", "0004000000030600", "0004000e00030700"} {
		rec := doRaw(t, s, http.MethodGet, "/internal/titles/name?namespace=3ds&title_id="+id, "Bearer k", nil)
		var out struct{ Name string }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusOK || out.Name != "Mario Kart 7" {
			t.Errorf("%s: %d %q", id, rec.Code, out.Name)
		}
	}
	rec := doRaw(t, s, http.MethodGet, "/internal/titles/name?id=0004000000030700", "Bearer k", nil)
	if !strings.Contains(rec.Body.String(), "Mario Kart 7") {
		t.Errorf("?id= alias: %s", rec.Body)
	}
	rec = doRaw(t, s, http.MethodGet, "/internal/titles/name?namespace=3ds&title_id=0004000000030900", "Bearer k", nil)
	if strings.Contains(rec.Body.String(), "Mario Kart 7") {
		t.Errorf("an unknown id named a game: %s", rec.Body)
	}
}
