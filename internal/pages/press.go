package pages

import (
	"net/http"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// pressConsoles is the fact sheet's row order: the networks OpenPak serves, then the ones in
// development, then the planned ones. A console the catalogue has and this list lacks is
// appended after it, so a new console shows up without an edit here.
var pressConsoles = []string{"Switch", "Wii U", "3DS", "Wii", "DS", "Xbox", "Xbox 360", "PS3", "PS2", "PSP", "Vita"}

// pressCount is one fact-sheet row: the catalogue's titles for a console by state.
type pressCount struct {
	Console                       string
	Working, Beta, Alpha, Planned int
}

// pressCounts tallies the title catalogue the way /api/v1/titles reports it: "live" is
// working, "beta" and "alpha" are themselves, and everything still waiting for a server
// (requestable, planned, in development) is planned.
func pressCounts(titles []store.Request) (rows []pressCount, total pressCount) {
	by := map[string]*pressCount{}
	var order []string
	for _, c := range pressConsoles {
		by[c] = &pressCount{Console: c}
		order = append(order, c)
	}
	for _, t := range titles {
		if t.Console == "" {
			continue
		}
		pc, ok := by[t.Console]
		if !ok {
			pc = &pressCount{Console: t.Console}
			by[t.Console] = pc
			order = append(order, t.Console)
		}
		switch t.Status {
		case "live":
			pc.Working++
		case "beta":
			pc.Beta++
		case "alpha":
			pc.Alpha++
		case "requestable", "planned", "in-development":
			pc.Planned++
		default:
			continue
		}
	}
	for _, c := range order {
		pc := by[c]
		if pc.Working+pc.Beta+pc.Alpha+pc.Planned == 0 {
			continue
		}
		rows = append(rows, *pc)
		total.Working += pc.Working
		total.Beta += pc.Beta
		total.Alpha += pc.Alpha
		total.Planned += pc.Planned
	}
	return rows, total
}

// handlePress: GET /press, the press kit. The text is English; the title counts are read
// live from the catalogue so the fact sheet never goes stale.
func (s *Server) handlePress(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "press.title"))
	titles, err := s.DB.ListRequests(r.Context(), "title", "")
	if err != nil {
		s.log.Error("press: list titles", "err", err)
	}
	rows, total := pressCounts(titles)
	type pressPage struct {
		baseData
		Counts []pressCount
		Total  pressCount
	}
	s.render(w, http.StatusOK, "press.html", pressPage{d, rows, total})
}
