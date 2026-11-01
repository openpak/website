package pages

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/rewards/rewardstest"
)

var rewardsPreviewURL = regexp.MustCompile(`/account/rewards/preview/([A-Za-z0-9_-]+)`)

// TestWritePreviewRewards renders every state of /account/rewards to HTML files, like TestWritePreview:
// only when PREVIEW_DIR is set, and the files are for looking at. REWARDS_ART_DIR points at the item art
// (research/rewards/art/export/preview); without it the items get flat generated tiles. The shop's
// dialogs are opened by a #r-<id> fragment, so a file with one is shot with scripting on.
//
//	PREVIEW_DIR=$PWD/.preview REWARDS_ART_DIR=... go test ./internal/pages/ -run WritePreviewRewards
func TestWritePreviewRewards(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the page previews")
	}
	t.Chdir("../..")
	if err := os.MkdirAll(filepath.Join(dir, "art"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	st := rewardstest.NewState()
	s.Rewards = rewardstest.New(t, st)

	// shoot renders path and writes it as name.html, with the item pictures next to it.
	shoot := func(name, path string) {
		t.Helper()
		path, _, _ = strings.Cut(path, "#") // the fragment opens a dialog in the browser, not on the server
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, w.Code)
		}
		html := rewardsPreviewURL.ReplaceAllStringFunc(w.Body.String(), func(u string) string {
			id := rewardsPreviewURL.FindStringSubmatch(u)[1]
			img := authedRequest(s, rec, http.MethodGet, u, nil)
			ext := ".png"
			if strings.Contains(img.Header().Get("Content-Type"), "svg") {
				ext = ".svg"
			}
			if err := os.WriteFile(filepath.Join(dir, "art", id+ext), img.Body.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			return "art/" + id + ext
		})
		WritePreviewFile(t, dir, name, html)
	}
	reset := func() { *st = *rewardstest.NewState(); st.Bearer = "" }
	owned := func(ids ...string) {
		for _, id := range ids {
			st.Owned[id] = true
		}
	}
	ago := func(h int) time.Duration { return time.Duration(h) * time.Hour }

	// A new account: nothing earned, nothing owned.
	reset()
	for _, tab := range []string{"missions", "shop", "collection", "settings"} {
		shoot("rewards-empty-"+tab, "/account/rewards?tab="+tab)
	}

	// Some progress, a little earned, the Founder seal held.
	reset()
	st.Balance, st.Lifetime, st.PlayPoints = 46, 94, 3
	st.Progress["daily-play-30"], st.Progress["weekly-4-days"], st.Progress["weekly-3-titles"] = 900, 2, 1
	st.Progress["weekly-2-networks"], st.Progress["first-online"], st.Progress["hours-10"], st.Progress["all-networks"] = 1, 60, 21600, 2
	st.Claimed["first-online"] = true
	owned("badge-founder", "badge-first-link")
	st.Equipped["badge"] = "badge-founder"
	st.Ledger = append(st.Ledger,
		rewardstest.LedgerLine(2, "earn", "play", "0100ED9024EB8000", ago(1)),
		rewardstest.LedgerLine(4, "earn", "new_title", "0100E65002BB8000", ago(5)),
		rewardstest.LedgerLine(3, "earn", "mission", "first-online:once", ago(30)),
		rewardstest.LedgerLine(2, "earn", "checkin", "", ago(31)),
		rewardstest.LedgerLine(-100, "spend", "redeem", "badge-launch-season", ago(80)))
	shoot("rewards-progress", "/account/rewards?tab=missions")

	// Rewards ready, and the day's cap reached.
	reset()
	st.Balance, st.Lifetime, st.PlayPoints = 120, 214, 8
	st.Progress["daily-play-30"], st.Progress["weekly-4-days"], st.Progress["weekly-3-titles"] = 1800, 4, 2
	st.Progress["first-friend"], st.Progress["hours-10"], st.Progress["first-online"] = 1, 36000, 600
	st.Progress["weekly-2-networks"], st.Progress["all-networks"] = 2, 2
	st.Claimed["first-online"] = true
	owned("badge-founder", "badge-first-link")
	st.Equipped["badge"] = "badge-first-link"
	st.Ledger = append(st.Ledger, rewardstest.LedgerLine(3, "earn", "mission", "first-online:once", ago(2)), rewardstest.LedgerLine(8, "earn", "play", "0100ED9024EB8000", ago(6)))
	shoot("rewards-claimable", "/account/rewards?tab=missions")
	shoot("rewards-claimed", "/account/rewards?tab=missions&m=claimed&n=2")

	// The shop, with enough points for some things.
	reset()
	st.Balance, st.Lifetime, st.PlayPoints = 260, 390, 5
	owned("badge-founder", "badge-first-link", "icon-tabby")
	st.Equipped["badge"], st.Equipped["profile_icon"] = "badge-founder", "icon-tabby"
	shoot("rewards-shop", "/account/rewards?tab=shop")
	shoot("rewards-shop-themes", "/account/rewards?tab=shop&kind=theme")
	shoot("rewards-shop-error", "/account/rewards?tab=shop&m=nopoints")
	shoot("rewards-dialog", "/account/rewards?tab=shop#r-badge-launch-season")
	shoot("rewards-dialog-short", "/account/rewards?tab=shop#r-theme-harbor")
	shoot("rewards-dialog-owned", "/account/rewards?tab=shop#r-icon-tabby")
	shoot("rewards-dialog-text", "/account/rewards?tab=shop#r-title-early-bird")

	// A collection with something of each kind.
	reset()
	st.Balance, st.Lifetime, st.PlayPoints = 35, 880, 8
	owned("badge-founder", "badge-first-link", "badge-warm-up", "badge-regular", "badge-launch-season", "icon-tabby", "icon-ember-fox", "icon-pak", "theme-harbor", "theme-graphite")
	st.Equipped["badge"], st.Equipped["profile_icon"], st.Equipped["theme"] = "badge-launch-season", "icon-pak", "theme-harbor"
	shoot("rewards-collection", "/account/rewards?tab=collection")
	shoot("rewards-collection-equipped", "/account/rewards?tab=collection&m=equipped&i=icon-pak")
	st.Pub = true
	shoot("rewards-settings", "/account/rewards?tab=settings")
	shoot("rewards-settings-saved", "/account/rewards?tab=settings&m=saved")

	// The account's own states: earning paused, rewards frozen, the service down, no service.
	reset()
	st.Balance, st.Lifetime, st.PlayPoints, st.Paused = 80, 200, 2, true
	owned("badge-founder")
	st.Equipped["badge"] = "badge-founder"
	shoot("rewards-paused", "/account/rewards?tab=missions")
	reset()
	st.Balance, st.Lifetime, st.Frozen = 80, 200, true
	owned("badge-founder")
	shoot("rewards-frozen", "/account/rewards?tab=shop")
	st.Down = true
	shoot("rewards-down", "/account/rewards?tab=shop")
	s.Rewards = nil
	shoot("rewards-unconfigured", "/account/rewards")
	t.Logf("rewards previews written to %s", dir)
}
