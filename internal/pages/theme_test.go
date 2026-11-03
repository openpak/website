package pages

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards/rewardstest"
)

func TestCSSColour(t *testing.T) {
	for _, ok := range []string{"#fff", "#FFFF", "#a54612", "#a54612cc", "rgb(1, 2, 3)", "rgba(0,0,0,.5)", "rgba(255 ,255,255, 0.25)", "rgb(255,255,255)"} {
		if !cssColour(ok) {
			t.Errorf("%q should pass", ok)
		}
	}
	for _, bad := range []string{"", "red", "#ggg", "#12345", "url(x)", "rgb(256,0,0)", "rgb(1 2 3)", "#fff;}body{display:none", "#fff/**/", "var(--x)", "expression(1)", "rgb(1,2,3);", "#fff\n", "rgb(1,2,3,2)", "</style><script>"} {
		if cssColour(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestParseTheme(t *testing.T) {
	good := rewardstest.StockTheme("theme-x")
	th, err := parseTheme([]byte(good))
	if err != nil || th.Light["paper"] == "" || th.Dark["accent"] != "#f6a579" || len(th.Light) != len(themeTokens) {
		t.Fatalf("stock theme: %v %+v", err, th)
	}
	if css := th.CSS(); !strings.HasPrefix(css, ":root{--paper:#fdf0e0;") || !strings.Contains(css, "@media (prefers-color-scheme:dark){:root{--paper:#2a2520;") {
		t.Fatalf("css: %s", css)
	}
	// A bad value is dropped, an unknown token never reaches the output.
	dirty := strings.Replace(good, `"muted":"#2a241d"`, `"muted":"red;}body{display:none"`, 1)
	dirty = strings.Replace(dirty, `"ok-weak"`, `"evil":"#fff","ok-weak"`, 1)
	th, err = parseTheme([]byte(dirty))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := th.Light["muted"]; ok || strings.Contains(th.CSS(), "display") || strings.Contains(th.CSS(), "evil") {
		t.Fatalf("not filtered: %s", th.CSS())
	}
	for name, raw := range map[string]string{
		"schema":   strings.Replace(good, "openpak-theme/1", "other/9", 1),
		"required": strings.Replace(good, `"on-accent":"#ffffff"`, `"on-accent":"nope"`, 1),
		"junk":     "not json",
	} {
		if _, err := parseTheme([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestThemeEndpoint(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	// Without rewards configured there is nothing to serve.
	if w := anon(s, "/themes/theme-harbor", nil); w.Code != 404 {
		t.Fatalf("no rewards: %d", w.Code)
	}
	st := rewardstest.NewState()
	st.ThemeAssets = map[string]string{"theme-ember": strings.Replace(rewardstest.StockTheme("theme-ember"), `"muted":"#2a241d"`, `"muted":"url(//evil)"`, 1)}
	s.Rewards = rewardstest.New(t, st)

	get := func(path string) *http.Response {
		w := anon(s, path, nil)
		return w.Result()
	}
	r := get("/themes/theme-harbor")
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("harbor: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	var out struct {
		ID, Name    string
		Light, Dark map[string]string
	}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil || out.ID != "theme-harbor" || out.Name != "Harbor" || out.Light["paper"] == "" || out.Dark["on-accent"] == "" {
		t.Fatalf("body: %v %+v", err, out)
	}
	r = get("/themes/theme-ember")
	var ember struct{ Light map[string]string }
	_ = json.NewDecoder(r.Body).Decode(&ember)
	if _, ok := ember.Light["muted"]; ok {
		t.Fatal("an invalid value was served")
	}
	for _, id := range []string{"nope", "theme-unknown", "badge-first-link", "THEME-HARBOR", "theme-harbor%00", "a%2fb", strings.Repeat("a", 90)} {
		if c := get("/themes/" + id).StatusCode; c != 404 {
			t.Errorf("%q: %d, want 404", id, c)
		}
	}
}

// anon is a signed-out GET.
func anon(s *Server, path string, _ *url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

var styleBlock = regexp.MustCompile(`(?s)<style>(.*?)</style>`)

func TestLayoutAppliesEquippedTheme(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	st := rewardstest.NewState()
	st.Owned["theme-ember"] = true
	st.Equipped["theme"] = "theme-ember"
	s.Rewards = rewardstest.New(t, st)

	w := authedRequest(s, rec, http.MethodGet, "/account", nil)
	body := w.Body.String()
	m := styleBlock.FindStringSubmatch(body)
	if w.Code != 200 || m == nil || !strings.HasPrefix(m[1], ":root{--paper:#fdf0e0;") {
		t.Fatalf("no theme style: %d", w.Code)
	}
	sum := sha256.Sum256([]byte(m[1]))
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'self' 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Fatalf("the style is not in the CSP: %s", csp)
	}
	if !strings.Contains(body, `<meta name="theme-color" content="#fdf0e0" media="(prefers-color-scheme: light)">`) ||
		!strings.Contains(body, `<meta name="theme-color" content="#2a2520" media="(prefers-color-scheme: dark)">`) {
		t.Fatal("theme-color not from the theme's paper")
	}

	// Nothing equipped, and a signed-out page: the default look, a plain CSP.
	for name, req := range map[string]*http.Response{
		"signed out": anon(s, "/help", nil).Result(),
	} {
		if req.Header.Get("Content-Security-Policy") != "default-src 'self'; img-src 'self' data:; style-src 'self'; frame-ancestors 'none'" {
			t.Errorf("%s: %s", name, req.Header.Get("Content-Security-Policy"))
		}
	}
	// An equip changes it at once (the cache is dropped), and an unequip too.
	st.Equipped["theme"] = ""
	s.themes.acct = nil
	if w := authedRequest(s, rec, http.MethodGet, "/account", nil); styleBlock.MatchString(w.Body.String()) {
		t.Fatal("style left after unequip")
	}
	// Rewards down: the page still renders, with the default look.
	st.Equipped["theme"] = "theme-ember"
	st.Down = true
	s.themes.acct = nil
	if w := authedRequest(s, rec, http.MethodGet, "/account", nil); w.Code != 200 || styleBlock.MatchString(w.Body.String()) {
		t.Fatalf("rewards down: %d", w.Code)
	}
	// A broken asset is the default look as well.
	st.Down = false
	st.ThemeAssets = map[string]string{"theme-ember": `{"schema":"openpak-theme/1","tokens":{}}`}
	s.themes = themeCache{}
	if w := authedRequest(s, rec, http.MethodGet, "/account", nil); w.Code != 200 || styleBlock.MatchString(w.Body.String()) {
		t.Fatalf("broken asset: %d", w.Code)
	}
}

func TestShopPreviewAndUpcoming(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	rec := login(t, s, "a@x.test", "password-A-1")
	st := rewardstest.NewState()
	st.Balance, st.Lifetime = 500, 500
	st.Owned["theme-moss"] = true
	st.Upcoming = true
	s.Rewards = rewardstest.New(t, st)

	shop := authedRequest(s, rec, http.MethodGet, "/account/rewards?tab=shop", nil).Body.String()
	for _, want := range []string{`data-preview-id="theme-harbor"`, `data-preview-owned="0"`, "Winter 2026", "Coming 1 Dec", "Coming 28 Dec", `class="gtile soon"`, "120 pts"} {
		if !strings.Contains(shop, want) {
			t.Errorf("shop lacks %q", want)
		}
	}
	if strings.Contains(shop, `data-preview-id="theme-moss"`) {
		t.Error("an owned theme is not in the shop")
	}
	if strings.Index(shop, "Winter 2026") < strings.LastIndex(shop, `class="gtile"`) {
		t.Error("upcoming cards come after what can be bought")
	}
	if strings.Contains(shop, `href="#r-badge-winter-2026"`) {
		t.Error("an upcoming item must not open a buy dialog")
	}
	mine := authedRequest(s, rec, http.MethodGet, "/account/rewards?tab=collection", nil).Body.String()
	if !strings.Contains(mine, `data-preview-id="theme-moss"`) || !strings.Contains(mine, `data-preview-owned="1"`) {
		t.Error("my items: owned theme has no Preview")
	}
	// The bar is in every page's layout, with its forms for a signed-in visitor.
	if !strings.Contains(mine, `id="pvbar"`) || !strings.Contains(mine, `data-pv="buy"`) || !strings.Contains(mine, "themepreview.js") {
		t.Error("preview bar missing")
	}
	out := anon(s, "/help", nil).Body.String()
	if !strings.Contains(out, `id="pvbar"`) || strings.Contains(out, `data-pv="buy"`) {
		t.Error("signed out: the bar has no buy form")
	}
}
