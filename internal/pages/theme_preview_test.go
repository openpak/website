package pages

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards/rewardstest"
)

// TestWritePreviewThemes renders the home, account, Rewards and Shop pages with a theme equipped, and the Shop with
// the preview bar up, for looking at: only when PREVIEW_DIR is set. REWARDS_THEMES_DIR is the theme assets
// (research/rewards/art/v2/export/themes) and REWARDS_ART_DIR the item art, as for the Rewards previews.
//
//	PREVIEW_DIR=$PWD/.preview REWARDS_THEMES_DIR=... GOWORK=off go test ./internal/pages -run PreviewThemes
func TestWritePreviewThemes(t *testing.T) {
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
	st.Balance, st.Lifetime, st.PlaySeconds = 480, 900, 40*3600
	st.Upcoming = true
	st.Owned["theme-moss"] = true
	st.Progress["first-online"] = 120
	s.Rewards = rewardstest.New(t, st)
	img := regexp.MustCompile(`/account/rewards/preview/([A-Za-z0-9_-]+)`)

	shoot := func(name, path, inject string) {
		t.Helper()
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		html := img.ReplaceAllStringFunc(w.Body.String(), func(u string) string {
			id := img.FindStringSubmatch(u)[1]
			p := authedRequest(s, rec, http.MethodGet, u, nil)
			ext := ".png"
			if strings.Contains(p.Header().Get("Content-Type"), "svg") {
				ext = ".svg"
			}
			_ = os.WriteFile(filepath.Join(dir, "art", id+ext), p.Body.Bytes(), 0o644)
			return "art/" + id + ext
		})
		if inject != "" {
			html = strings.Replace(html, "<head>", "<head><script>"+inject+"</script>", 1)
		}
		WritePreviewFile(t, dir, name, html)
	}

	for _, id := range []string{"theme-era-ps3", "theme-splat", "theme-sakura", "theme-era-ds", "theme-ember", "theme-harbor"} {
		st.Equipped["theme"] = id
		st.Owned[id] = true
		s.themes.acct = nil
		short := strings.TrimPrefix(id, "theme-")
		shoot("t-"+short+"-home", "/", "")
		shoot("t-"+short+"-account", "/account", "")
		shoot("t-"+short+"-rewards", "/account/rewards?tab=missions", "")
		shoot("t-"+short+"-shop", "/account/rewards?tab=shop", "")
	}
	// The Shop as the default look, then with a theme being previewed: the bar, from sessionStorage as the page keeps it.
	st.Equipped["theme"] = ""
	s.themes.acct = nil
	shoot("shop", "/account/rewards?tab=shop", "")
	shoot("collection", "/account/rewards?tab=collection", "")
	for _, id := range []string{"theme-era-xbox", "theme-sakura"} {
		raw, err := os.ReadFile(filepath.Join(os.Getenv("REWARDS_THEMES_DIR"), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		th, err := parseTheme(raw)
		if err != nil {
			t.Fatal(err)
		}
		item := map[string]any{"id": id, "name": strings.Title(strings.ReplaceAll(strings.TrimPrefix(id, "theme-"), "-", " ")), "price": 450, "owned": false, "equipped": false, "light": th.Light, "dark": th.Dark}
		b, _ := json.Marshal(item)
		shoot("bar-"+strings.TrimPrefix(id, "theme-"), "/account/rewards?tab=shop", `sessionStorage.setItem("openpak-theme-preview",`+"'"+string(b)+"'"+`)`)
	}
	owned := map[string]any{"id": "theme-moss", "name": "Moss", "price": 400, "owned": true, "equipped": false}
	raw, _ := os.ReadFile(filepath.Join(os.Getenv("REWARDS_THEMES_DIR"), "theme-moss.json"))
	th, _ := parseTheme(raw)
	owned["light"], owned["dark"] = th.Light, th.Dark
	b, _ := json.Marshal(owned)
	shoot("bar-moss-owned", "/account/rewards?tab=collection", `sessionStorage.setItem("openpak-theme-preview",`+"'"+string(b)+"'"+`)`)
}
