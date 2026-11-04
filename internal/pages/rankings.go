package pages

import (
	"net/http"
	"sort"
	"strings"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/i18n"
)

// rankedTitle is one entry of the rankings hub.
type rankedTitle struct {
	companion.Title
	Boards int
	// Scored is true when at least one board has a score. Names lists the board names shown for a
	// game with no scores yet, and More is how many further boards there are.
	Scored bool
	Names  string
	More   int
}

// rankedGroup is the hub's titles on one console.
type rankedGroup struct {
	Console string
	Titles  []rankedTitle
}

type rankingsHub struct {
	baseData
	Groups   []rankedGroup
	Statuses []string
	Consoles []string
}

// maxBoardNames is how many board names a game with no scores lists on the hub.
const maxBoardNames = 4

// consoleOrder is the order the site lists consoles in.
var consoleOrder = map[string]int{"Switch": 0, "Wii U": 1, "3DS": 2, "Wii": 3, "DS": 4, "Xbox": 5, "Xbox 360": 6, "PS3": 7}

// handleRankingsHub is /rankings: every game that has rankings, by console, each linking to its
// Rankings tab.
func (s *Server) handleRankingsHub(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "rankings.title"))
	page := rankingsHub{baseData: d}
	if s.Companion != nil {
		titles, err := s.Companion.Ranked(r.Context())
		if err != nil {
			s.log.Warn("rankings hub", "err", err)
		}
		by := map[string][]rankedTitle{}
		seen := map[string]bool{}
		for _, t := range titles {
			boards, err := s.Companion.BoardsOf(r.Context(), t)
			if err != nil {
				continue
			}
			rt := rankedTitle{Title: t, Boards: len(boards)}
			for _, b := range boards {
				if !b.NoScores {
					rt.Scored = true
				}
			}
			if !rt.Scored {
				var names []string
				for i, b := range boards {
					if i < maxBoardNames {
						names = append(names, b.Label)
					}
				}
				rt.Names = strings.Join(names, ", ")
				rt.More = max(len(boards)-maxBoardNames, 0)
			}
			by[t.Console] = append(by[t.Console], rt)
			if t.Status != "" && !seen[t.Status] {
				seen[t.Status] = true
				page.Statuses = append(page.Statuses, t.Status)
			}
		}
		for c, ts := range by {
			// Games with scores first, then by name (Ranked already gave the names in order).
			sort.SliceStable(ts, func(i, j int) bool { return ts[i].Scored && !ts[j].Scored })
			page.Groups = append(page.Groups, rankedGroup{c, ts})
		}
		sort.Slice(page.Groups, func(i, j int) bool {
			a, aok := consoleOrder[page.Groups[i].Console]
			b, bok := consoleOrder[page.Groups[j].Console]
			if aok != bok {
				return aok
			}
			if aok && a != b {
				return a < b
			}
			return page.Groups[i].Console < page.Groups[j].Console
		})
		sort.Strings(page.Statuses)
		for _, g := range page.Groups {
			page.Consoles = append(page.Consoles, g.Console)
		}
	}
	s.render(w, http.StatusOK, "rankings.html", page)
}
