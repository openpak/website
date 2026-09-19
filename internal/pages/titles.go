package pages

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// titleNames maps title ids to names, for the titles OpenPak actually serves.
// Used by the account page's "being played" list, which knows only the id.
// An id with no name here is printed as the id — honest, and how a new title
// announces itself the first time somebody plays it.
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
func popularTitles(all []store.Request) []store.Request {
	byName := make(map[string]store.Request, len(all))
	for _, t := range all {
		byName[t.Name] = t
	}
	var out []store.Request
	for _, n := range popularNames {
		if t, ok := byName[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// mostRequested returns a copy ordered by vote count desc, ties broken by the
// catalog sort. This is the scoreboard: how the project reads what to prioritise.
func mostRequested(reqs []store.Request) []store.Request {
	out := append([]store.Request(nil), reqs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Votes != out[j].Votes {
			return out[i].Votes > out[j].Votes
		}
		return out[i].Sort < out[j].Sort
	})
	return out
}

// handleTitles is the full, filterable catalogue: every title, filtered
// client-side by console and status and searched by name. `?sort=votes` turns it
// into the scoreboard — most requested first — which is the point of the
// requestable long tail.
func (s *Server) handleTitles(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(d0Lang(r), "titles.title"))
	var accountID string
	if v != nil {
		accountID = v.AccountID
	}
	reqs, err := s.DB.ListRequests(r.Context(), "title", accountID)
	if err != nil {
		s.log.Error("list requests", "err", err)
	}
	// The scoreboard (vote ranking) is internal: only admins get the sorted view.
	byVotes := d.Admin && r.URL.Query().Get("sort") == "votes"
	// The list is what has a server; the long tail sits behind "Request a game". The admin
	// scoreboard view keeps everything, ranked by votes.
	var requestable []store.Request
	if byVotes {
		reqs = mostRequested(reqs)
	} else {
		reqs = store.CatalogOrder(reqs)
		var served []store.Request
		for _, t := range reqs {
			if t.Votable() {
				requestable = append(requestable, t)
			} else {
				served = append(served, t)
			}
		}
		reqs = served
	}
	// Distinct consoles and statuses, for the filter dropdowns, in stable order.
	seenC, seenS := map[string]bool{}, map[string]bool{}
	var consoles, statuses []string
	for _, t := range reqs {
		if t.Console != "" && !seenC[t.Console] {
			seenC[t.Console] = true
			consoles = append(consoles, t.Console)
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
		Titles      []store.Request
		Requestable []store.Request
		Consoles    []string
		Statuses    []string
		AllStatuses []string
		ByVotes     bool
		Back        string
	}
	s.render(w, http.StatusOK, "titles.html", titlesPage{d, reqs, requestable, consoles, statuses, allStatuses, byVotes, backPath("/titles", byVotes)})
}

// allStatuses is every status a title can be set to, in rank order. The word means the same
// thing everywhere: live works on hardware, beta is deployed and not console-verified, alpha
// is partial, scaffold is code waiting on a fact, requestable is listed for votes.
var allStatuses = []string{"live", "beta", "alpha", "scaffold", "requestable", "not-feasible"}

// TitleStatuses is allStatuses for the admin pages, as a copy so no caller can edit the rule.
func TitleStatuses() []string { return slices.Clone(allStatuses) }

// ErrBadTitleStatus is a change that names no title, or a status outside allStatuses.
var ErrBadTitleStatus = errors.New("a title id and one of the known statuses are required")

// SetTitleStatus is the one way a title's status changes, from the public titles page or
// the admin table: validate, write the row, and leave an audit entry saying what it was
// before. Features are refused: their status follows the catalog, and a hand edit would be
// overwritten on the next refresh anyway.
func SetTitleStatus(ctx context.Context, db store.Store, actorID string, id int64, status, notes string) error {
	if !slices.Contains(allStatuses, status) {
		return ErrBadTitleStatus
	}
	titles, err := db.ListRequests(ctx, "title", "")
	if err != nil {
		return err
	}
	i := slices.IndexFunc(titles, func(t store.Request) bool { return t.ID == id })
	if i < 0 {
		return ErrBadTitleStatus
	}
	notes = strings.TrimSpace(notes)
	if err := db.SetRequestStatus(ctx, id, status, notes); err != nil {
		return err
	}
	old := titles[i]
	detail := fmt.Sprintf("%d %s: %s -> %s", id, old.Name, old.Status, status)
	if notes != old.Notes {
		detail += fmt.Sprintf(" notes=%q", notes)
	}
	return db.AddAudit(ctx, actorID, "title.status", detail)
}

// handleSetTitleStatus is how a title changes status: an admin picks it on the titles page
// and the database row changes. The site and the app read that row, so nothing is rebuilt.
func (s *Server) handleSetTitleStatus(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !slices.Contains(s.AdminAccountIDs, v.AccountID) {
		http.Error(w, "admins only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	err := SetTitleStatus(r.Context(), s.DB, v.AccountID, id, r.PostFormValue("status"), r.PostFormValue("notes"))
	if errors.Is(err, ErrBadTitleStatus) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		s.log.Error("set title status", "id", id, "err", err)
		http.Error(w, "could not change the status", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/titles", http.StatusSeeOther)
}

// d0Lang reads the request language the same way s.data does, so the page title
// is localised without threading baseData through.
func d0Lang(r *http.Request) string { return i18n.FromRequest(r) }

// backPath is where a vote returns the viewer, preserving the sort view.
func backPath(base string, byVotes bool) string {
	if byVotes {
		return base + "?sort=votes"
	}
	return base
}

// consoleSlug maps a console name to the basename of its logo under
// /static/consoles. Unknown consoles fall back to a generic gamepad glyph so a
// row is never a broken image. Keep in step with the app's consoleAsset and
// with the svg files actually present.
func consoleSlug(console string) string {
	switch console {
	case "Switch":
		return "switch"
	case "Wii":
		return "wii"
	case "Wii U":
		return "wii-u"
	case "DS":
		return "ds"
	case "3DS":
		return "3ds"
	case "Xbox":
		return "xbox"
	case "Xbox 360":
		return "xbox-360"
	case "PS2":
		return "ps2"
	case "PS3":
		return "ps3"
	case "PSP":
		return "psp"
	case "Vita":
		return "vita"
	default:
		return "generic"
	}
}

// badgeClass maps a status to the badge colour modifier, so a pill reads the
// same as the app: green for done, amber for in-flight, red for blocked, and
// neutral (no class) for the not-yet-started long tail.
func badgeClass(status string) string {
	switch status {
	case "live", "working", "active":
		return "ok"
	case "beta", "alpha", "partial", "open", "deletion-pending":
		return "warn"
	case "banned":
		return "bad"
	case "scaffold": // code in the tree, waiting on a fact a console has to reveal
		return "warn"
	case "not-feasible", "broken":
		return "bad"
	default:
		return ""
	}
}
