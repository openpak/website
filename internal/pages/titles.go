package pages

import (
	"net/http"
	"sort"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// titleNames maps title ids to names, for the titles OpenPak actually serves.
//
// Deliberately two entries. An earlier version of this file listed half a dozen
// popular games copied from a competitor's status page: none of them run
// against this network, and one of the ids was not even a game. A status page
// that lists titles it does not serve is advertising, and a wrong id is a lie
// with a number next to it.
//
// An id with no name here is printed as the id. That is honest, and it is how
// a new title announces itself the first time somebody plays it.
var titleNames = map[string]string{
	"0100ED9024EB8000": "Outbound",
	"0100E65002BB8000": "Stardew Valley",
}

func titleName(id string) string {
	if n, ok := titleNames[id]; ok {
		return n
	}
	return id
}

// popularNames is the handful shown on the landing page. Only titles that are
// actually in the catalogue appear — this list just decides the order and which
// few to feature, never invents a game the network does not serve.
var popularNames = []string{
	"Mario Kart 8 Deluxe",
	"Splatoon 3",
	"Super Smash Bros. Ultimate",
	"Animal Crossing: New Horizons",
	"Super Mario Odyssey",
	"Minecraft",
}

// popularTitles picks the featured few from the full catalogue, in popularNames
// order, skipping any not currently served. Caps at len(popularNames).
func popularTitles(all []store.Title) []store.Title {
	byName := make(map[string]store.Title, len(all))
	for _, t := range all {
		byName[t.Name] = t
	}
	var out []store.Title
	for _, n := range popularNames {
		if t, ok := byName[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// handleTitles is the full, filterable catalogue: every served title, filtered
// client-side by console and status and searched by name (the set is small, so
// there is no reason to round-trip the server for a filter).
func (s *Server) handleTitles(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(d0Lang(r), "titles.title"))
	titles, err := s.DB.ListTitles(r.Context())
	if err != nil {
		s.log.Error("list titles", "err", err)
	}
	// Distinct consoles and statuses, for the filter dropdowns, in stable order.
	seenC, seenS := map[string]bool{}, map[string]bool{}
	var consoles, statuses []string
	for _, t := range titles {
		if t.Platform != "" && !seenC[t.Platform] {
			seenC[t.Platform] = true
			consoles = append(consoles, t.Platform)
		}
		if t.Status != "" && !seenS[t.Status] {
			seenS[t.Status] = true
			statuses = append(statuses, t.Status)
		}
	}
	sort.Strings(consoles)
	sort.Strings(statuses)
	type titlesPage struct {
		baseData
		Titles   []store.Title
		Consoles []string
		Statuses []string
	}
	s.render(w, http.StatusOK, "titles.html", titlesPage{d, titles, consoles, statuses})
}

// d0Lang reads the request language the same way s.data does, so the page title
// is localised without threading baseData through.
func d0Lang(r *http.Request) string { return i18n.Lang(r.Header.Get("Accept-Language")) }
