package pages

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
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
	// Game servers keyed by an id the titles catalogue does not carry: Mario
	// Golf's server by the update's id (the game's ends in 000), Splatoon 2's
	// by the European edition's.
	"0100C9C00E25C800": "Mario Golf: Super Rush",
	"0100F8F0000A2000": "Splatoon 2",
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
	tallies, err := s.DB.ListCompat(r.Context())
	if err != nil {
		s.log.Error("list compat", "err", err)
	}
	var plays []core.PlayTally
	if s.Playtime != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		if plays, err = s.Playtime.Playtime(ctx); err != nil {
			s.log.Warn("playtime", "err", err) // the page still has player reports
		}
		cancel()
	}
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
	s.render(w, http.StatusOK, "titles.html", titlesPage{d, titleRows(reqs, tallies, plays), requestable, consoles, statuses, allStatuses, byVotes, backPath("/titles", byVotes)})
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

// titleRow is one title on the titles page with what its details dialog shows:
// every way to play it and what players reported for each.
type titleRow struct {
	store.Request
	Compat []compatCell
}

// compatCell is one client and OS for a title: what players reported, and how long it has
// been played online there. No reports and not Verified is untested.
type compatCell struct {
	Client, OS    string
	Works, Broken int
	Played        time.Duration // online playtime, every player added up
	Verified      bool          // Played crossed a playtime threshold
}

// Played long enough online to count as tested: one session this long, or this much added up
// across every player.
const (
	verifySession = 15 * time.Minute
	verifyTotal   = 30 * time.Minute
)

// Minutes is Played for the tooltip.
func (c compatCell) Minutes() int { return int(c.Played / time.Minute) }

// ProblemURL opens a help ticket about the title, subject and category filled in.
func (t titleRow) ProblemURL() string {
	category := "other"
	switch t.Console {
	case "Switch":
		category = "switch"
	case "Wii U", "3DS":
		category = "wiiu-3ds"
	}
	msg := t.Name + " (" + t.Console
	if t.TitleID != "" {
		msg += ", " + t.TitleID
	}
	msg += ")\nPlayed on (console or emulator, and OS): \nWhat happened: \n"
	return "/help?" + url.Values{"subject": {t.Name + ": problem"}, "category": {category}, "message": {msg}}.Encode() + "#new-h"
}

// State is the cell's colour: ok when every report says it works, bad when every
// one says it does not, warn when they disagree, "" when untested.
// Playtime counts as a report that it works; with failure reports too it is amber, a
// conflict to investigate rather than one side outvoting the other.
func (c compatCell) State() string {
	switch {
	case c.Verified && c.Broken == 0:
		return "ok"
	case c.Verified:
		return "warn"
	case c.Works == 0 && c.Broken == 0:
		return ""
	case c.Broken == 0:
		return "ok"
	case c.Works == 0:
		return "bad"
	default:
		return "warn"
	}
}

// compatClient is one row of the compatibility grid: a console or an emulator
// with a cell per OS it runs on (a console has one, "hardware").
type compatClient struct {
	Client string
	Icon   string // an emulator's mark under /static/clients; "" for a console or one without a mark
	Cells  []compatCell
}

// osColumns are the grid's columns after the client, the same on every row.
var osColumns = []string{"Windows", "macOS", "Linux", "Android", "iOS"}

// OSColumns is the template's view of osColumns.
func (titleRow) OSColumns() []string { return osColumns }

// Cols lines the row's cells up under osColumns; nil where the client does not run.
func (c compatClient) Cols() []*compatCell {
	out := make([]*compatCell, len(osColumns))
	for i := range c.Cells {
		if j := slices.Index(osColumns, c.Cells[i].OS); j >= 0 {
			out[j] = &c.Cells[i]
		}
	}
	return out
}

// Hardware is a console row: the console's mark stands for the one cell.
func (c compatClient) Hardware() bool { return len(c.Cells) == 1 && c.Cells[0].OS == hardware }

// State is the row's colour: the best any of its cells reached.
func (c compatClient) State() string {
	for _, want := range []string{"ok", "warn", "bad"} {
		for _, cell := range c.Cells {
			if cell.State() == want {
				return want
			}
		}
	}
	return ""
}

// clientIcons are the emulators with a mark in web/static/clients.
var clientIcons = map[string]string{
	"Ryujinx": "ryujinx.png", "Eden": "eden.png", "Citron": "citron.png", "Cemu": "cemu.png",
	"Azahar": "azahar.png", "Dolphin": "dolphin.png", "melonDS": "melonds.png",
}

// Clients groups the title's cells by client, in the grid's order.
func (t titleRow) Clients() []compatClient {
	var out []compatClient
	for _, c := range t.Compat {
		if n := len(out); n > 0 && out[n-1].Client == c.Client {
			out[n-1].Cells = append(out[n-1].Cells, c)
			continue
		}
		icon := ""
		if f, ok := clientIcons[c.Client]; ok {
			icon = "/static/clients/" + f
		}
		out = append(out, compatClient{c.Client, icon, []compatCell{c}})
	}
	return out
}

// Combo is the report form's value for the cell, parsed back by parseCombo.
func (c compatCell) Combo() string { return c.Client + "|" + c.OS }

// hardware is the OS of a console: the game runs on the machine itself.
const hardware = "hardware"

var (
	desktop    = []string{"Windows", "Linux", "macOS"}
	allOSes    = []string{"Windows", "Linux", "macOS", "Android"}
	onHardware = []string{hardware}
)

// playTarget is one way to run a console's games: the console itself, a console
// that plays them backwards-compatibly, or an emulator on the OSes it ships for.
type playTarget struct {
	Client string
	OSes   []string
}

// playTargets lists, per console, every client a player can report on. OpenPak's
// own emulator forks come first; a console with none here offers only itself.
// Keep in step with the emulators OpenPak ships (see /downloads).
var playTargets = map[string][]playTarget{
	"Switch":        {{"Switch", onHardware}, {"Ryujinx", desktop}, {"Eden", allOSes}, {"Citron", []string{"Windows", "Linux", "Android", "iOS"}}},
	"Wii U":         {{"Wii U", onHardware}, {"Cemu", desktop}},
	"3DS":           {{"3DS", onHardware}, {"Azahar", []string{"Windows", "macOS", "Linux", "Android", "iOS"}}},
	"Wii":           {{"Wii", onHardware}, {"Wii U", onHardware}, {"Dolphin", allOSes}},
	"DS":            {{"DS", onHardware}, {"3DS", onHardware}, {"melonDS", desktop}},
	"Xbox":          {{"Xbox", onHardware}, {"Xbox 360", onHardware}, {"xemu", desktop}},
	"Xbox 360":      {{"Xbox 360", onHardware}, {"Xbox One", onHardware}, {"Xenia", []string{"Windows", "Linux"}}},
	"PS2":           {{"PS2", onHardware}, {"PCSX2", desktop}},
	"PS3":           {{"PS3", onHardware}, {"RPCS3", desktop}},
	"PlayStation 3": {{"PS3", onHardware}, {"RPCS3", desktop}},
	"PSP":           {{"PSP", onHardware}, {"PPSSPP", []string{"Windows", "macOS", "Linux", "Android", "iOS"}}},
	"Vita":          {{"Vita", onHardware}, {"Vita3K", allOSes}},
}

// playTargetsOf is a console's playTargets; a console missing from the table still
// has the console itself, so its dialog is never empty.
func playTargetsOf(console string) []playTarget {
	if p, ok := playTargets[console]; ok {
		return p
	}
	return []playTarget{{console, onHardware}}
}

// titleRows pairs every title with its full client×OS grid, reports filled in.
// ponytail: every title's dialog is rendered into the page; fetch it per title if the list grows into the thousands.
func titleRows(reqs []store.Request, tallies []store.CompatTally, plays []core.PlayTally) []titleRow {
	type key struct {
		id         int64
		client, os string
	}
	byKey := make(map[key]store.CompatTally, len(tallies))
	for _, t := range tallies {
		byKey[key{t.RequestID, t.Client, t.OS}] = t
	}
	// Playtime arrives keyed by title id and the core's tokens; one game can have several
	// ids (regions, an update's), so it is added up per row.
	type played struct{ total, longest time.Duration }
	byID := map[string]int64{}
	for _, r := range reqs {
		for _, id := range append([]string{r.TitleID}, r.TitleIDs...) {
			if id != "" {
				byID[strings.ToUpper(id)] = r.ID
			}
		}
	}
	playedBy := map[key]played{}
	for _, p := range plays {
		id, ok := byID[baseTitleID(p.TitleID)]
		client, os := playClient(p.Client, p.OS)
		if !ok || client == "" {
			continue
		}
		k := key{id, client, os}
		cur := playedBy[k]
		cur.total += p.Total
		cur.longest = max(cur.longest, p.Longest)
		playedBy[k] = cur
	}
	out := make([]titleRow, len(reqs))
	for i, r := range reqs {
		out[i].Request = r
		for _, p := range playTargetsOf(r.Console) {
			for _, os := range p.OSes {
				k := key{r.ID, p.Client, os}
				t, pl := byKey[k], playedBy[k]
				out[i].Compat = append(out[i].Compat, compatCell{p.Client, os, t.Works, t.Broken,
					pl.total, pl.longest >= verifySession || pl.total >= verifyTotal})
			}
		}
	}
	return out
}

// playClient maps the core's client and OS tokens onto the grid's names. A console
// session names no OS; an emulator that did not say which OS it ran on cannot be
// placed in a column, so it is left out ("" client).
func playClient(client, os string) (string, string) {
	consoles := map[string]string{"switch": "Switch", "wiiu": "Wii U", "3ds": "3DS"}
	emulators := map[string]string{"ryujinx": "Ryujinx", "eden": "Eden", "citron": "Citron",
		"cemu": "Cemu", "azahar": "Azahar", "dolphin": "Dolphin", "melonds": "melonDS"}
	oses := map[string]string{"windows": "Windows", "macos": "macOS", "linux": "Linux", "android": "Android", "ios": "iOS"}
	if c, ok := consoles[client]; ok && os == "" {
		return c, hardware
	}
	if c, ok := emulators[client]; ok && oses[os] != "" {
		return c, oses[os]
	}
	return "", ""
}

// parseCombo checks a report's "client|os" against the title's console, so the
// ledger only ever holds combinations the page offers.
func parseCombo(console, combo string) (client, os string, ok bool) {
	client, os, found := strings.Cut(combo, "|")
	if !found {
		return "", "", false
	}
	for _, p := range playTargetsOf(console) {
		if p.Client == client && slices.Contains(p.OSes, os) {
			return client, os, true
		}
	}
	return "", "", false
}

// handleCompatReport records "it worked" or "it did not" for one title on one
// client and OS, then reopens that title's dialog. Only titles with a server
// take reports: a requestable one cannot be played online yet.
func (s *Server) handleCompatReport(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	reqs, err := s.DB.ListRequests(r.Context(), "title", "")
	if err != nil {
		s.log.Error("list requests", "err", err)
		http.Error(w, "could not save the report", http.StatusInternalServerError)
		return
	}
	i := slices.IndexFunc(reqs, func(t store.Request) bool { return t.ID == id })
	if i < 0 || reqs[i].Votable() {
		http.Error(w, "no such title", http.StatusBadRequest)
		return
	}
	client, os, ok := parseCombo(reqs[i].Console, r.PostFormValue("combo"))
	works := r.PostFormValue("works")
	if !ok || (works != "yes" && works != "no") {
		http.Error(w, "pick a client and whether it worked", http.StatusBadRequest)
		return
	}
	if err := s.DB.ReportCompat(r.Context(), id, v.AccountID, client, os, works == "yes"); err != nil {
		s.log.Error("report compat", "id", id, "err", err)
		http.Error(w, "could not save the report", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/titles#t-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// gamestopURL is a GameStop search. Only the US store: its other storefronts closed
// or became other shops (Italy is GameLife now).
func gamestopURL(name string) string {
	return "https://www.gamestop.com/search/?q=" + url.QueryEscape(name)
}

// cexURL is a CeX search for the game, where it can be bought second hand. webuy.com
// sends a visitor to their own country's store.
func cexURL(name string) string {
	return "https://webuy.com/search?stext=" + url.QueryEscape(name)
}
