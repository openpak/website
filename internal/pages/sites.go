package pages

import (
	"net/http"

	"github.com/openpak/website/internal/i18n"
)

// openSite is one of the Open sites: a companion web service on its own openpak.org host.
// Key selects the copy (sites.<key>.line, sites.<key>.games) and the mark under /static/sites.
type openSite struct {
	Key, Name, URL string
}

// Mark is the site's mark, copied from research/brand/site-marks.
func (o openSite) Mark() string { return "/static/sites/" + o.Key + ".svg" }

// openSites is the directory in the order /sites shows it.
var openSites = []openSite{
	{"opensplat", "OpenSplat", "https://splat.openpak.org"},
	{"openkart", "OpenKart", "https://kart.openpak.org"},
	{"openmaker", "OpenMaker", "https://maker.openpak.org"},
	{"openverse", "OpenVerse", "https://verse.openpak.org"},
}

// titleSites says which Open site covers a title, keyed by catalogue name and console. It is
// code, not data: a new game page needs a line here and nothing in the database.
var titleSites = map[[2]string]string{
	{"Splatoon", "Wii U"}:                         "opensplat",
	{"Mario Kart 8", "Wii U"}:                     "openkart",
	{"Super Mario Maker", "Wii U"}:                "openmaker",
	{"Super Mario Maker for Nintendo 3DS", "3DS"}: "openmaker",
	{"Super Mario Maker", "3DS"}:                  "openmaker",
	{"Super Mario Maker 2", "Switch"}:             "openmaker",
}

// siteFor is the Open site for a title, nil when it has none.
func siteFor(name, console string) *openSite {
	key, ok := titleSites[[2]string{name, console}]
	if !ok {
		return nil
	}
	for i := range openSites {
		if openSites[i].Key == key {
			return &openSites[i]
		}
	}
	return nil
}

// handleSites is /sites, the directory of the Open sites.
func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "sites.title"))
	s.render(w, http.StatusOK, "sites.html", struct {
		baseData
		Sites []openSite
	}{d, openSites})
}
