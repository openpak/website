package pages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

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
		"Recent points", "Mission", "Play for 30 minutes</span>", `aria-current="page"`, "rewards.js")
	lacks("/account/rewards", "rewards.tab", "rewards.m.", "rewards.claim", "daily-play-30:") // no raw keys, no raw ids

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
