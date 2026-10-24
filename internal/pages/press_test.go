package pages

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

// The press kit renders with its sections, the live catalogue counts, the downloads
// and the disclaimer, and is linked from the footer of every page.
func TestPressPage(t *testing.T) {
	s, _, db := newTestServer(t)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "A", Console: "Switch", Status: "live"},
		{Kind: "title", Name: "B", Console: "Switch", Status: "live"},
		{Kind: "title", Name: "C", Console: "Switch", Status: "beta"},
		{Kind: "title", Name: "D", Console: "Wii U", Status: "requestable"},
		{Kind: "title", Name: "E", Console: "3DS", Status: "alpha"},
		{Kind: "title", Name: "F", Console: "N-Gage", Status: "planned"},
	})
	rec := get(t, s, "/press")
	if rec.Code != http.StatusOK {
		t.Fatalf("/press: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Press kit", `id="about"`, `id="facts"`, `id="titles"`, `id="logos"`, `id="screenshots"`,
		"2026", "AGPL-3.0", "Public alpha", "mailto:contact@openpak.org",
		"/static/press/openpak-icon-512.png", "/static/press/openpak-icon-1024.png", "/static/icon.svg",
		"Pretendo Network", "WiiLink", "Kinnay", "nex-go",
		"not affiliated with, endorsed by or sponsored by Nintendo, Microsoft or Sony",
		"<tr><td>Switch</td><td>2</td><td>1</td><td>0</td><td>0</td></tr>",
		"<tr><td>Wii U</td><td>0</td><td>0</td><td>0</td><td>1</td></tr>",
		"<tr><td>3DS</td><td>0</td><td>0</td><td>1</td><td>0</td></tr>",
		"<tr><td>N-Gage</td><td>0</td><td>0</td><td>0</td><td>1</td></tr>",
		"<b>2</b></td><td><b>1</b></td><td><b>1</b></td><td><b>2</b>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/press: missing %q", want)
		}
	}
	if strings.Contains(body, "<tr><td>Xbox</td><td>0") {
		t.Error("/press: a console with no titles gets no row")
	}
	if strings.Contains(body, "{{") || strings.Contains(body, "SCREENSHOTS") {
		t.Error("/press: the screenshots placeholder must stay in the template")
	}
	if !strings.Contains(get(t, s, "/").Body.String(), `href="/press"`) {
		t.Error("the footer should link the press kit")
	}
	if de := getLang(t, s, "/press", "de"); !strings.Contains(de, "Pressemappe") || !strings.Contains(de, "nur auf Englisch") {
		t.Error("/press [de]: translated title and English-only note expected")
	}
	if strings.Contains(body, "nur auf Englisch") || strings.Contains(body, "English only") {
		t.Error("/press [en]: no English-only note on the English page")
	}
}

func TestPressCounts(t *testing.T) {
	rows, total := pressCounts([]store.Request{
		{Console: "DS", Status: "beta"}, {Console: "Switch", Status: "live"},
		{Console: "Switch", Status: "in-development"}, {Console: "PS3", Status: "broken"}, {Status: "live"},
	})
	if len(rows) != 2 || rows[0].Console != "Switch" || rows[1].Console != "DS" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Working != 1 || rows[0].Planned != 1 || total.Beta != 1 || total.Working != 1 {
		t.Errorf("counts: %+v total %+v", rows, total)
	}
}
