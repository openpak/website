package pages

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type titlesPage struct {
	baseData
	Titles      []titleRow
	Requestable []store.Request
	Consoles    []string
	Statuses    []string
	AllStatuses []string
	ByVotes     bool
	Back        string
}

func renderTitles(t *testing.T, pg titlesPage) string {
	t.Helper()
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "titles.html", pg); err != nil {
		t.Fatalf("titles.html failed to render: %v", err)
	}
	return b.String()
}

// A not-started (requestable/planned) title carries a Request control; live,
// beta and now alpha titles just show a status pill. The vote ranking is
// admin-only. Render the combinations so a gating regression fails a test, not
// production.
func TestTitlesVoteGating(t *testing.T) {
	votableRow := []store.Request{{ID: 7, Kind: "title", Name: "Tetris 99", Console: "Switch", Status: "requestable", Votes: 3, Voted: true}}
	betaRow := []store.Request{{ID: 8, Kind: "title", Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "beta", Votes: 0}}
	alphaRow := []store.Request{{ID: 9, Kind: "title", Name: "Fall Guys", Console: "Switch", Status: "alpha", Votes: 4}}

	// Signed out, not-started: a Request affordance to sign in, no form, and a
	// console logo for the row.
	out := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles"}, titleRows(votableRow, nil, nil, nil, nil), nil, []string{"Switch"}, []string{"requestable"}, nil, false, "/titles"})
	for _, want := range []string{"Tetris 99", "/static/titles.js", "Sign in to request", "Request", "/static/consoles/switch.svg"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("signed-out not-started missing %q", want)
		}
	}
	if bytes.Contains([]byte(out), []byte(`action="/request/vote"`)) {
		t.Error("signed-out must not show a request form")
	}

	// Signed in, not-started: a request form with the id and CSRF token, the
	// "Requested" label (Voted), and NO vote count (ranking is admin-only).
	in := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, titleRows(votableRow, nil, nil, nil, nil), nil, []string{"Switch"}, []string{"requestable"}, nil, false, "/titles"})
	for _, want := range []string{`action="/request/vote"`, `name="id" value="7"`, `value="tok"`, "Requested"} {
		if !bytes.Contains([]byte(in), []byte(want)) {
			t.Errorf("signed-in not-started missing %q", want)
		}
	}
	if bytes.Contains([]byte(in), []byte("· 3")) {
		t.Error("non-admin must not see the vote count")
	}

	// Admin, not-started: the vote count rides the button, and the scoreboard
	// toggle is present.
	adm := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok", Admin: true}, titleRows(votableRow, nil, nil, nil, nil), nil, []string{"Switch"}, []string{"requestable"}, nil, false, "/titles"})
	for _, want := range []string{"· 3", `href="/titles?sort=votes"`} {
		if !bytes.Contains([]byte(adm), []byte(want)) {
			t.Errorf("admin view missing %q", want)
		}
	}

	// Signed in, alpha: a status pill, never a request form (alpha is started).
	al := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, titleRows(alphaRow, nil, nil, nil, nil), nil, []string{"Switch"}, []string{"alpha"}, nil, false, "/titles"})
	if !bytes.Contains([]byte(al), []byte(">alpha<")) {
		t.Error("alpha row should show its status pill")
	}
	if bytes.Contains([]byte(al), []byte(`action="/request/vote"`)) {
		t.Error("alpha title must not show a request form")
	}

	// Signed in, NON-votable (beta): status shown, but no request UI at all.
	beta := renderTitles(t, titlesPage{baseData{Lang: "en", Path: "/titles", User: &viewUser{ID: "acct-a"}, CSRF: "tok"}, titleRows(betaRow, nil, nil, nil, nil), nil, []string{"Switch"}, []string{"beta"}, nil, false, "/titles"})
	if !bytes.Contains([]byte(beta), []byte("Mario Kart 8 Deluxe")) || !bytes.Contains([]byte(beta), []byte(">beta<")) {
		t.Error("beta row should still show name and status")
	}
	for _, forbidden := range []string{`action="/request/vote"`, "Sign in to request"} {
		if bytes.Contains([]byte(beta), []byte(forbidden)) {
			t.Errorf("non-votable beta title must not show %q", forbidden)
		}
	}
	// Its details dialog: every Switch client, CeX and GameStop, and no report form (reports
	// are switched off).
	if bytes.Contains([]byte(beta), []byte(`micromania`)) {
		t.Error("an English reader gets GameStop, not Micromania")
	}
	if bytes.Contains([]byte(beta), []byte(`/titles/report`)) {
		t.Error("the report form is switched off")
	}
	for _, want := range []string{`id="t-8"`, `href="#t-8"`, `title="Harbor · Linux: Untested"`, `title="Switch: Untested"`, "https://webuy.com/search?stext=Mario&#43;Kart&#43;8&#43;Deluxe", "/help?category=switch&amp;message=Mario", "Report a problem", "https://www.gamestop.com/search/?q=Mario&#43;Kart&#43;8&#43;Deluxe"} {
		if !bytes.Contains([]byte(beta), []byte(want)) {
			t.Errorf("beta dialog missing %q", want)
		}
	}
}

func TestMostRequestedOrdersByVotes(t *testing.T) {
	got := mostRequested([]store.Request{
		{Name: "Low", Sort: 0, Votes: 1},
		{Name: "High", Sort: 1, Votes: 9},
		{Name: "TieA", Sort: 2, Votes: 5},
		{Name: "TieB", Sort: 3, Votes: 5},
	})
	want := []string{"High", "TieA", "TieB", "Low"} // votes desc, ties by catalog sort
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, n)
		}
	}
}

// Playtime marks a cell tested: 15 minutes in one session or 30 across every player,
// matched by any of the title's ids and the core's client and OS tokens. With failure
// reports too it is a conflict (amber), and an emulator that named no OS is not placed.
func TestTitleRowsPlaytime(t *testing.T) {
	reqs := []store.Request{
		{ID: 1, Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "beta", TitleID: "0100C2500FC20000", TitleIDs: []string{"0100C2500FC20000", "0100C2500FC21000"}},
		{ID: 2, Kind: "title", Name: "Mario Kart 8", Console: "Wii U", Status: "live", TitleID: "000500001010EC00", TitleIDs: []string{"000500001010EC00"}},
	}
	m := time.Minute
	plays := []core.PlayTally{
		{Namespace: "switch", TitleID: "0100c2500fc21000", Client: "harbor", OS: "macos", Total: 16 * m, Longest: 16 * m},    // one long session
		{Namespace: "switch", TitleID: "0100C2500FC20000", Client: "harbor", OS: "android", Total: 20 * m, Longest: 10 * m},  // short
		{Namespace: "switch", TitleID: "0100C2500FC21000", Client: "harbor", OS: "android", Total: 12 * m, Longest: 5 * m},   // other region: 32 in total
		{Namespace: "switch", TitleID: "0100C2500FC20000", Client: "harbor", OS: "linux", Total: 40 * m, Longest: 40 * m},    // conflicts with a report
		{Namespace: "switch", TitleID: "0100C2500FC20000", Client: "harbor", OS: "", Total: 60 * m, Longest: 60 * m},         // no OS: nowhere to put it
		{Namespace: "switch", TitleID: "0100C2500FC20000", Client: "ryujinx", OS: "windows", Total: 60 * m, Longest: 60 * m}, // a retired emulator: not in the grid
		{Namespace: "switch", TitleID: "0100C2500FC20000", Client: "switch", OS: "", Total: 9 * m, Longest: 9 * m},           // console, too short
		{Namespace: "wiiu", TitleID: "0005000E1010EC00", Client: "wiiu", OS: "", Total: 31 * m, Longest: 31 * m},             // an update's id
	}
	reports := []store.CompatTally{{RequestID: 1, Client: "Harbor", OS: "Linux", Broken: 1}}
	rows := titleRows(reqs, reports, plays, nil, nil)
	cell := func(r int, client, os string) compatCell {
		for _, c := range rows[r].Compat {
			if c.Client == client && c.OS == os {
				return c
			}
		}
		t.Fatalf("no cell %s/%s", client, os)
		return compatCell{}
	}
	for _, c := range []struct {
		row        int
		client, os string
		state      string
	}{
		{0, "Harbor", "macOS", "ok"},
		{0, "Harbor", "Android", "ok"},
		{0, "Harbor", "Linux", "warn"},
		{0, "Harbor", "Windows", ""},
		{0, "Switch", "hardware", ""},
		{1, "Wii U", "hardware", "ok"},
	} {
		if got := cell(c.row, c.client, c.os).State(); got != c.state {
			t.Errorf("%s/%s: state %q, want %q", c.client, c.os, got, c.state)
		}
	}
	for _, c := range rows[0].Compat {
		if c.Client == "Ryujinx" {
			t.Error("a retired emulator must not have a row")
		}
	}
	if got := cell(0, "Switch", "hardware").Minutes(); got != 9 {
		t.Errorf("console minutes %d, want 9", got)
	}
}

// A stored product page wins over the search; the admin form only takes a link on
// the shop's own site over https.
func TestShopLinks(t *testing.T) {
	rows := titleRows([]store.Request{{ID: 1, Name: "Mario Kart 7", Console: "3DS"}}, nil, nil,
		[]store.ShopLink{{RequestID: 1, Shop: "cex", URL: "https://uk.webuy.com/product-detail?id=045496521264"}}, nil)
	got := rows[0].Shops()
	if len(got) != 3 || !got[0].Product || got[0].URL != "https://uk.webuy.com/product-detail?id=045496521264" {
		t.Fatalf("cex: %+v", got)
	}
	if got[1].Product || got[1].URL != "https://www.gamestop.com/search/?q=Mario+Kart+7" {
		t.Errorf("gamestop fallback: %+v", got[1])
	}
	for lang, want := range map[string]string{"en": "CeX GameStop", "de": "CeX GameStop", "fr": "CeX Micromania"} {
		var names []string
		for _, l := range rows[0].ShopsFor(lang) {
			names = append(names, l.Name)
		}
		if got := strings.Join(names, " "); got != want {
			t.Errorf("%s shops: %q, want %q", lang, got, want)
		}
	}
	for _, c := range []struct {
		shop, url string
		ok        bool
	}{
		{"gamestop", "https://www.gamestop.com/video-games/nintendo-3ds/products/mario-kart-7/10095226.html", true},
		{"micromania", "https://www.micromania.fr/mario-kart-7-58906.html", true},
		{"cex", "https://ie.webuy.com/product-detail?id=1", true},
		{"cex", "", true},
		{"cex", "http://uk.webuy.com/product-detail?id=1", false},
		{"gamestop", "https://evil.test/gamestop.com", false},
		{"gamestop", "https://notgamestop.com/x", false},
		{"gamestop", "https://user@www.gamestop.com/x", false},
		{"ebay", "https://www.ebay.com/x", false},
		{"cex", "javascript:alert(1)", false},
	} {
		if _, ok := shopURL(c.shop, c.url); ok != c.ok {
			t.Errorf("shopURL(%s, %q) ok=%v, want %v", c.shop, c.url, ok, c.ok)
		}
	}
}

// Missing covers have a visible placeholder; remote icons keep the same fallback
// hidden in the DOM so an error can reveal it without an inline CSP handler.
func TestTitlesArtworkFallback(t *testing.T) {
	requests := []store.Request{
		{ID: 101, Kind: "title", Name: "Missing cover", Console: "Wii U", Status: "beta"},
		{ID: 102, Kind: "title", Name: "Risk of Rain", Console: "Switch", Status: "beta", TitleID: "0100AAA00ACBE800"},
	}
	out := renderTitles(t, titlesPage{
		baseData: baseData{Lang: "en", Path: "/titles"},
		Titles:   titleRows(requests, nil, nil, nil, nil),
	})
	for _, want := range []string{
		`class="mark art-placeholder">`,
		`class="mark art-placeholder" hidden>`,
		`data-title-image src="/titles/icon/0100AAA00ACBE800"`,
		`/static/consoles/wii-u.svg`,
		`/static/consoles/switch.svg`,
		`Picture coming soon`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("artwork fallback missing %q", want)
		}
	}
	if strings.Contains(out, "onerror=") {
		t.Error("artwork fallback must work with the site's CSP")
	}
}
