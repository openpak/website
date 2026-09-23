package pages

import (
	"context"
	"errors"
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

// titleName is case-insensitive: the core reports a title id in lower case
// and every table here is written in upper, which is how a game with a player
// on it read as nobody playing (2026-09-20, ARMS).
func titleName(id string) string {
	if n, ok := titleNames[strings.ToUpper(id)]; ok {
		return n
	}
	return id
}

// TitleCatalogue indexes the requests catalogue (the titles page's data, and
// the same table an admin edits) by title id, upper case, so a title id a
// friend is playing can be named even when it is not in the small static map
// above. Built once per page or API call, not once per friend row.
func TitleCatalogue(ctx context.Context, db store.Store) map[string]string {
	reqs, err := db.ListRequests(ctx, "title", "")
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(reqs))
	for _, r := range reqs {
		if r.Name == "" {
			continue
		}
		for _, id := range append([]string{r.TitleID}, r.TitleIDs...) {
			if id != "" {
				out[strings.ToUpper(id)] = r.Name
			}
		}
	}
	return out
}

// baseTitleID maps a Wii U or 3DS update's title id to its game's: an update shares the
// game's low word under its own high word (0005000E / 0004000E), and a console can report
// either. Everything else comes back upper case and unchanged.
func baseTitleID(id string) string {
	id = strings.ToUpper(id)
	if len(id) == 16 {
		switch id[:8] {
		case "0005000E":
			return "00050000" + id[8:]
		case "0004000E":
			return "00040000" + id[8:]
		}
	}
	return id
}

// FriendGame names what a friend's live presence says they are playing: the
// static map, then the requests catalogue, then — for a Switch title neither
// knows — a live nlib lookup. "" means none of those know it, so the caller
// shows the platform and "in a game" rather than a raw title id, as a friend
// row must never print one. icon reports whether /titles/icon/{id} can draw
// art for it, which today only a Switch title id can.
func FriendGame(ctx context.Context, catalogue map[string]string, namespace, titleID string) (name string, icon bool) {
	if titleID == "" {
		return "", false
	}
	icon = namespace == "switch" && switchID.MatchString(titleID)
	if n, ok := titleNames[strings.ToUpper(titleID)]; ok {
		return n, icon
	}
	if n, ok := catalogue[strings.ToUpper(titleID)]; ok {
		return n, icon
	}
	if n, ok := catalogue[baseTitleID(titleID)]; ok {
		return n, icon
	}
	if icon {
		if n := switchTitleName(ctx, titleID); n != "" {
			return n, icon
		}
	}
	return "", icon
}

// platformLabel names a presence namespace the way a person reads it. Unknown
// namespaces — a platform this site does not special-case yet — are shown
// capitalised rather than invented.
func platformLabel(namespace string) string {
	switch namespace {
	case "":
		return ""
	case "switch":
		return "Nintendo Switch"
	case "wiiu":
		return "Wii U"
	case "3ds":
		return "Nintendo 3DS"
	default:
		return strings.ToUpper(namespace[:1]) + namespace[1:]
	}
}

// namespaceConsole maps a presence namespace to the Console name consoleSlug
// knows, so a friend's platform gets the same logo as the titles and saves
// pages. An unmapped namespace falls through to consoleSlug's generic glyph.
func namespaceConsole(namespace string) string {
	switch namespace {
	case "switch":
		return "Switch"
	case "wiiu":
		return "Wii U"
	case "3ds":
		return "3DS"
	default:
		return ""
	}
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
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "titles.title"))
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

// allStatuses is every status the title forms offer, in rank order. The word means the same
// thing everywhere: live works on hardware, beta is deployed and not console-verified, alpha
// is partial, requestable is listed for votes. store.Statuses is what a write accepts.
var allStatuses = []string{"live", "beta", "alpha", "requestable", "not-feasible"}

// TitleStatuses is allStatuses for the admin pages, as a copy so no caller can edit the rule.
func TitleStatuses() []string { return slices.Clone(allStatuses) }

// UpdateTitleStatus is the admin forms' status change (the titles page and /admin/titles). It
// goes through store.UpdateRequest like the API does, so the change log, last_updated and the
// title.status audit row come from one place. A bad id or status is store.ErrNotFound or
// store.ErrInvalid.
func UpdateTitleStatus(ctx context.Context, db store.Store, actorID string, r *http.Request) (int64, error) {
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	status, notes := r.PostFormValue("status"), r.PostFormValue("notes")
	_, err := db.UpdateRequest(ctx, id, store.RequestPatch{Status: &status, Notes: &notes}, actorID)
	return id, err
}

// BadTitleEdit is an error the form's sender caused: answer 400, not 500.
func BadTitleEdit(err error) bool {
	return errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrNotFound)
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
	id, err := UpdateTitleStatus(r.Context(), s.DB, v.AccountID, r)
	if BadTitleEdit(err) {
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
	case "PS3", "PlayStation 3":
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
	case "beta": // it runs; only the console check is missing, so not amber
		return "beta"
	case "alpha", "partial", "open", "deletion-pending":
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
