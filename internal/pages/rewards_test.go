package pages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards"
	"github.com/openpak/website/internal/rewards/rewardstest"
	"time"
)

func TestRewardsPage(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	w := authedRequest(s, rec, http.MethodGet, "/account/rewards", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "not available") {
		t.Fatalf("no backend: %d", w.Code)
	}

	st := rewardstest.NewState()
	st.Balance, st.Lifetime, st.PlayPoints = 120, 130, 3
	st.Progress["daily-play-30"] = 900
	st.Progress["first-online"] = 120 // done, claimable
	st.Owned["badge-founder"] = true
	st.Equipped["badge"] = "badge-founder"
	st.Ledger = append(st.Ledger, rewardstest.LedgerLine(2, "earn", "mission", "daily-play-30:2026-11-02", time.Hour), rewardstest.LedgerLine(-100, "spend", "redeem", "badge-launch-season", 2*time.Hour))
	s.Rewards = rewardstest.New(t, st)

	get := func(path string) string {
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		return w.Body.String()
	}
	has := func(path string, want ...string) {
		t.Helper()
		body := get(path)
		for _, x := range want {
			if !strings.Contains(body, x) {
				t.Errorf("%s lacks %q", path, x)
			}
		}
	}
	lacks := func(path string, not ...string) {
		t.Helper()
		body := get(path)
		for _, x := range not {
			if strings.Contains(body, x) {
				t.Errorf("%s has %q", path, x)
			}
		}
	}

	// Missions is the default tab: the balance, today's cap, the claimable mission with its button, the
	// one in progress with its numbers, and the history with names rather than ids.
	has("/account/rewards", ">120<", "3 of 8", "Rewards ready to claim: 1", "Claim 3 points", "Play for 30 minutes", "15 min / 30 min",
		"Recent points", "Mission", "<b>Play for 30 minutes</b>", `class="lcard"`, "/static/rewards/act-daily-play.png", "/account/rewards/preview/badge-first-link", `aria-current="page"`, "rewards.js")
	lacks("/account/rewards", "rewards.tab", "rewards.m.", "rewards.claim", "daily-play-30:") // no raw keys, no raw ids
	lacks("/account/rewards", "cardrows")                                                     // no list rows for missions or the points history: everything is a card

	// Every /account page is inside the account shell, with its own entry current.
	for path, entry := range map[string]string{"/account/rewards": "/account/rewards", "/account/activity": "/account/activity", "/account/trophies": "/account/trophies",
		"/account/parental": "/account/parental", "/account/saves": "/account/saves", "/account/chat": "/account/chat", "/account": "/account"} {
		body := get(path)
		if !strings.Contains(body, `class="acct-main"`) || !strings.Contains(body, `<a href="`+entry+`" aria-current="page">`) && !strings.Contains(body, `<a href="`+entry+`" aria-current="page"`) {
			t.Errorf("%s is not inside the account shell with %s current", path, entry)
		}
	}

	// The shop is a shelf of tiles with a confirm dialog each; an affordable item is a button, one that is too dear says how much is missing.
	has("/account/rewards?tab=shop", `class="gtiles under"`, `class="gtiles under wide"`, `href="#r-badge-launch-season"`, `id="r-badge-launch-season"`, "100 pts",
		"Redeem for 100 points", "You need 280 more points.", `name="key"`)
	has("/account/rewards?tab=shop&kind=theme", "Harbor")
	lacks("/account/rewards?tab=shop&kind=theme", "Launch Season")
	lacks("/account/rewards?tab=shop", `id="r-badge-founder"`) // granted-only items are not for sale

	// The collection: the Founder seal apart, the rest grouped.
	has("/account/rewards?tab=collection", "Founder", "Equipped", `name="slot" value="badge"`, "Unequip")
	has("/account/rewards?tab=settings", `name="showcase"`, `name="paused"`)
	has("/account/rewards?tab=nonsense", "Ready to claim") // an unknown tab is the first one

	// Messages come back typed, and name only what the service listed.
	has("/account/rewards?tab=shop&m=nopoints", `notice bad`, `role="alert"`, "enough points")
	has("/account/rewards?tab=missions&m=claimed&n=3", `notice ok`, "Claimed 3 points.")
	has("/account/rewards?tab=collection&m=equipped&i=badge-founder", "Founder is equipped.")
	lacks("/account/rewards?tab=collection&m=redeemed&i=<script>", "<script>alert")

	if !strings.HasPrefix(st.Bearer, "Bearer ") || len(st.Bearer) < 10 {
		t.Errorf("the service was not given the session: %q", st.Bearer)
	}

	// Actions go back to the tab they belong to, with a message.
	loc := func(path string, form url.Values) string {
		t.Helper()
		w := authedRequest(s, rec, http.MethodPost, path, &form)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d", path, w.Code)
		}
		return w.Header().Get("Location")
	}
	if l := loc("/account/rewards/redeem", url.Values{"item": {"theme-harbor"}}); l != "/account/rewards?m=nopoints&tab=shop" {
		t.Errorf("too dear: %q", l)
	}
	if l := loc("/account/rewards/redeem", url.Values{"item": {"badge-launch-season"}}); l != "/account/rewards?i=badge-launch-season&m=redeemed&tab=collection" {
		t.Errorf("redeem: %q", l)
	}
	if l := loc("/account/rewards/redeem", url.Values{"item": {"badge-launch-season"}}); l != "/account/rewards?m=owned&tab=shop" {
		t.Errorf("owned: %q", l)
	}
	if l := loc("/account/rewards/claim", url.Values{"mission": {"first-online"}, "period": {"once"}}); l != "/account/rewards?m=claimed&n=3&tab=missions" {
		t.Errorf("claim: %q", l)
	}
	if l := loc("/account/rewards/equip", url.Values{"slot": {"badge"}, "item": {""}}); l != "/account/rewards?i=&m=unequipped&tab=collection" {
		t.Errorf("unequip: %q", l)
	}
	if l := loc("/account/rewards/settings", url.Values{"showcase": {"1"}}); l != "/account/rewards?m=saved&tab=settings" || !st.Pub || st.Paused {
		t.Errorf("settings: %q %v %v", l, st.Pub, st.Paused)
	}
	st.Frozen = true
	if l := loc("/account/rewards/redeem", url.Values{"item": {"icon-tabby"}}); l != "/account/rewards?m=frozen&tab=shop" {
		t.Errorf("frozen: %q", l)
	}

	// A service that does not answer is a page that says so and offers another try, not an error page.
	st.Down = true
	has("/account/rewards?tab=shop", "not available right now", "Try again", `href="/account/rewards?tab=shop"`)
}

// A badge ladder is one tile in Missions (the next tier) and one in My items (the highest earned), never both
// in the same place and never listed twice; the shop holds only what is not owned.
func TestRewardsBadgeTiles(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	st := rewardstest.NewState()
	st.Balance = 500
	st.Counts["kart_sessions"], st.Counts["turf_sessions"] = 26, 10
	for _, id := range []string{"kart-races-1", "kart-races-10", "kart-races-25", "splatoon-matches-1"} {
		st.Claimed[id] = true
	}
	for _, m := range rewardstest.Missions {
		if strings.HasPrefix(m.ID, "friends-played-") {
			st.Claimed[m.ID] = true // a finished ladder
		}
	}
	st.Counts["friends_played_with"] = 250
	for _, id := range []string{"badge-kart-1", "badge-kart-10", "badge-kart-25", "badge-turf-1", "icon-tabby"} {
		st.Owned[id] = true
	}
	st.Equipped["badge"] = "badge-kart-25"
	s.Rewards = rewardstest.New(t, st)
	get := func(path string) string { return authedRequest(s, rec, http.MethodGet, path, nil).Body.String() }

	m := get("/account/rewards?tab=missions")
	for _, want := range []string{"In progress", `href="#l-kart"`, "Kart Sessions · Silver", "26 / 50", `id="l-kart"`, "Get 15 points &#43; badge", `href="#l-turf"`, "Claim 5 points", "Turf Sessions · Copper"} {
		if !strings.Contains(m, want) {
			t.Errorf("missions lacks %q", want)
		}
	}
	if i := strings.Index(m, `id="done-h"`); i < 0 || !strings.Contains(m[i:], `href="#l-friends"`) || strings.Contains(m[:i], `href="#l-friends"`) || strings.Contains(m, ">Equip<") {
		t.Error("a finished ladder is not under Completed, or an equip button is in Missions")
	}
	if strings.Contains(m, "cardrows") {
		t.Error("a mission or history row in Missions")
	}

	my := get("/account/rewards?tab=collection")
	for _, want := range []string{`href="#l-kart"`, "Kart Sessions · Bronze", "26 / 50", "Equipped", `href="#l-friends"`, "Completed", "Friends Played With · Diamond", "Max", "Equipped now"} {
		if !strings.Contains(my, want) {
			t.Errorf("my items lacks %q", want)
		}
	}
	if strings.Contains(my, "/account/rewards/claim") {
		t.Error("a claim button in My items")
	}
	for _, dup := range []string{`aria-label="Equip: 10 Kart Sessions"`, `aria-label="Equip: 25 Kart Sessions"`, `aria-label="Equip: 1 Turf Sessions"`} {
		if strings.Contains(my, dup) {
			t.Errorf("a ladder badge is listed twice: %s", dup)
		}
	}
	if n := strings.Count(my, `href="#l-kart"`); n != 1 {
		t.Errorf("the Kart ladder is %d tiles", n)
	}

	shop := get("/account/rewards?tab=shop")
	if strings.Contains(shop, `id="r-icon-tabby"`) || !strings.Contains(shop, `id="r-icon-ember-fox"`) || strings.Contains(shop, "Owned") {
		t.Error("the shop lists something owned, or lacks something for sale")
	}

	// Equip puts in the slot the highest tier earned.
	form := url.Values{"slot": {"badge"}, "item": {"badge-kart-25"}}
	if w := authedRequest(s, rec, http.MethodPost, "/account/rewards/equip", &form); w.Code != http.StatusSeeOther {
		t.Errorf("equip: %d", w.Code)
	}
}

func TestTargetTextGroupsThousands(t *testing.T) {
	for lang, want := range map[string]string{"en": "1,000", "de": "1.000"} {
		if got := targetText(lang, rewards.Mission{Metric: "titles_played", Target: 1000}); got != want {
			t.Errorf("%s: %q, want %q", lang, got, want)
		}
	}
	if got := targetText("en", rewards.Mission{Metric: "lifetime_play_seconds", Target: 2500 * 3600}); !strings.Contains(got, "2,500") {
		t.Errorf("hours: %q", got)
	}
}

// Game cosmetics are items of their own kind: listed in the shop and the collection under the game's name, with no
// art of ours (the placeholder tile), and bought like any other item.
func TestRewardsGameCosmetics(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	st := rewardstest.NewState()
	st.Balance, st.Lifetime = 120, 120
	st.Owned["dst-skin-wilson-formal"] = true
	s.Rewards = rewardstest.New(t, st)
	get := func(path string) string {
		t.Helper()
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		return w.Body.String()
	}
	shop := get("/account/rewards?tab=shop&kind=game_cosmetic")
	for _, want := range []string{"Festive Willow", "Blue Runner", "Don&#39;t Starve Together, Switch", "Stumble Guys, Switch", "Game item"} {
		if !strings.Contains(shop, want) {
			t.Errorf("shop lacks %q", want)
		}
	}
	// The game's own art is the tile; the game and console are on the card; the item's name is printed once per tile.
	for _, want := range []string{`src="/titles/icon/010090100E334000"`, `src="/titles/icon/0100A9E01AE9A000"`, `<span class="badge">Don&#39;t Starve Together</span> <span class="hint">Switch</span>`, `<span class="badge">Stumble Guys</span> <span class="hint">Switch</span>`} {
		if !strings.Contains(shop, want) {
			t.Errorf("shop lacks %q", want)
		}
	}
	// One shelf per game, each headed by the game's icon and name.
	for _, want := range []string{`<h3 class="rwgroup"><img class="gicon" src="/titles/icon/010090100E334000" alt="" width="32" height="32">Don&#39;t Starve Together `, `<h3 class="rwgroup"><img class="gicon" src="/titles/icon/0100A9E01AE9A000" alt="" width="32" height="32">Stumble Guys `} {
		if !strings.Contains(shop, want) {
			t.Errorf("shop lacks the game shelf %q", want)
		}
	}
	if strings.Contains(shop, "<b>Festive Willow</b>") {
		t.Error("the item's name is drawn inside the tile art as well as in its caption")
	}
	if !strings.Contains(shop, "themes and game items") {
		t.Error("the intro does not mention game items")
	}
	if strings.Contains(shop, "Formal Wilson") {
		t.Error("an owned item is still for sale")
	}
	if strings.Contains(shop, "rewards.k") {
		t.Error("raw translation key")
	}
	// A game the site has no art for falls back to the placeholder, named by the game.
	iv := s.itemOf("en", rewards.Me{}, rewards.Item{ID: "x", Kind: "game_cosmetic", Name: "Thing", Game: &rewards.GameInfo{GameID: "some-new-game"}})
	if iv.Art != "" || iv.GameName != "some new game" {
		t.Errorf("fallback: art %q name %q", iv.Art, iv.GameName)
	}
	if got := itemJSON(s.itemOf("en", rewards.Me{}, rewards.Item{ID: "x", Kind: "game_cosmetic", Name: "T", Game: &rewards.GameInfo{GameID: "dst"}})).Img; got != "/titles/icon/010090100E334000" {
		t.Errorf("app image: %q", got)
	}
	mine := get("/account/rewards?tab=collection")
	if !strings.Contains(mine, "Formal Wilson") || !strings.Contains(mine, `<img class="gicon" src="/titles/icon/010090100E334000" alt="" width="32" height="32">Don&#39;t Starve Together`) {
		t.Error("collection lacks the owned game item")
	}
	if strings.Contains(mine, `name="slot" value="game_cosmetic"`) {
		t.Error("a game item has an Equip button: it is equipped in the game")
	}
	if !strings.Contains(mine, `<span class="badge">Don&#39;t Starve Together</span> <span class="hint">Switch</span>`) {
		t.Error("collection does not say which game the item is for")
	}
}
