package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestSitesDirectory(t *testing.T) {
	s, _, db := newTestServer(t)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Splatoon", Console: "Wii U", Status: "beta"},
		{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "beta"},
		{Kind: "title", Name: "Super Mario Maker 2", Console: "Switch", Status: "beta"},
	})
	get := func(path string) string {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, w.Code)
		}
		return w.Body.String()
	}
	page := get("/sites")
	for _, want := range []string{"https://splat.openpak.org", "https://kart.openpak.org", "https://maker.openpak.org", "https://verse.openpak.org",
		"/static/sites/opensplat.svg", `href="/sites"`} {
		if !strings.Contains(page, want) {
			t.Errorf("/sites lacks %q", want)
		}
	}
	for _, bad := range []string{"sites.", "opensplat.openpak", "openkart.openpak", "openmaker.openpak", "openverse.openpak", "pretendo"} {
		if strings.Contains(strings.ToLower(page), bad) {
			t.Errorf("/sites contains %q", bad)
		}
	}
	titles := get("/titles")
	if got := strings.Count(titles, "Open OpenSplat"); got != 1 {
		t.Errorf("Splatoon (Wii U) should have one OpenSplat button, got %d", got)
	}
	if got := strings.Count(titles, "Open OpenMaker"); got != 1 {
		t.Errorf("Super Mario Maker 2 should have one OpenMaker button, got %d", got)
	}
}

func TestSiteFor(t *testing.T) {
	for _, c := range []struct{ name, console, want string }{
		{"Splatoon", "Wii U", "opensplat"}, {"Mario Kart 8", "Wii U", "openkart"},
		{"Super Mario Maker", "Wii U", "openmaker"}, {"Super Mario Maker", "3DS", "openmaker"},
		{"Super Mario Maker 2", "Switch", "openmaker"}, {"Splatoon 2", "Switch", ""}, {"Mario Kart 8 Deluxe", "Switch", "openkart"},
		{"Mario Kart 7", "3DS", "openkart"}, {"Mario Kart Wii", "Wii", "openkart"}, {"Mario Kart DS", "DS", ""},
	} {
		got := ""
		if o := siteFor(c.name, c.console); o != nil {
			got = o.Key
		}
		if got != c.want {
			t.Errorf("siteFor(%q, %q) = %q, want %q", c.name, c.console, got, c.want)
		}
	}
}
