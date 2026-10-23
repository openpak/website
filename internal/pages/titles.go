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
// Callers that need a fallback label can use the id when no name is known.
// Live playing lists use the database catalogue instead.
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

// CatalogueTitleName resolves only titles in the database. An unknown title
// counts as online, but does not belong in the website's playing lists.
func CatalogueTitleName(catalogue map[string]string, id string) string {
	if name := catalogue[strings.ToUpper(id)]; name != "" {
		return name
	}
	return catalogue[baseTitleID(id)]
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
// hasGameServer is true for a title OpenPak runs a game server for. Alpha is votable, but a
// title with a server has players on it (the landing page counts them), so it is listed.
func hasGameServer(t store.Request) bool {
	health := gameServerHealth()
	for _, id := range append([]string{t.TitleID}, t.TitleIDs...) {
		if _, ok := health[strings.ToUpper(id)]; ok {
			return true
		}
	}
	return false
}

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
			if t.Votable() && !hasGameServer(t) {
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
	// ponytail: player reports are switched off (no form, no route); the ledger and any
	// reports already in it stay, so turning them back on is the form and a handler.
	tallies, err := s.DB.ListCompat(r.Context())
	if err != nil {
		s.log.Error("list compat", "err", err)
	}
	links, err := s.DB.ListShopLinks(r.Context())
	if err != nil {
		s.log.Error("list shop links", "err", err)
	}
	playability, err := s.DB.ListPlayability(r.Context())
	if err != nil {
		s.log.Error("list playability", "err", err)
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
	s.render(w, http.StatusOK, "titles.html", titlesPage{d, titleRows(reqs, tallies, plays, links, playability), requestable, consoles, statuses, allStatuses, byVotes, backPath("/titles", byVotes)})
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
// consoleSlugs is the logos for a row: Wii / DS and Wii U / 3DS show both.
func consoleSlugs(name string) []string {
	if name == "Wii / DS" {
		return []string{"wii", "ds"}
	}
	if name == "Wii U / 3DS" {
		return []string{"wii-u", "3ds"}
	}
	return []string{consoleSlug(name)}
}

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
	case "Xbox One":
		return "xbox-one"
	case "PS2":
		return "ps2"
	case "PS3", "PlayStation 3":
		return "ps3"
	case "PS4", "PlayStation 4":
		return "ps4"
	case "PS5", "PlayStation 5":
		return "ps5"
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
// statusText is a status as a badge shows it. Only "in-development" differs
// from its token, because the hyphen would read as a code.
func statusText(status string) string {
	if status == "in-development" {
		return "in development"
	}
	return status
}

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
	case "scaffold", "in-development": // code in the tree, not yet playable
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
	links  map[string]string // shop key -> product page
	play   map[string]string // client -> playability
}

// playabilityPick is one line of the admin playability form: a client the title can run on,
// and what it is set to today ("" = unknown).
type playabilityPick struct{ Client, Status string }

// Playability lists the title's clients in the grid's order with their current state, so the
// admin form offers the same names the compatibility table shows.
func (t titleRow) Playability() []playabilityPick {
	var out []playabilityPick
	for _, p := range playTargetsOf(t.Console) {
		out = append(out, playabilityPick{p.Client, t.play[p.Client]})
	}
	return out
}

// PlayabilityStates is store.PlayabilityStates for the form's dropdown.
func (titleRow) PlayabilityStates() []string { return store.PlayabilityStates }

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

// clientIcons are the marks of the non-console rows: Harbor is OpenPak's own.
var clientIcons = map[string]string{"Harbor": "/static/clients/harbor.svg"}

// Clients groups the title's cells by client, in the grid's order.
func (t titleRow) Clients() []compatClient {
	var out []compatClient
	for _, c := range t.Compat {
		if n := len(out); n > 0 && out[n-1].Client == c.Client {
			out[n-1].Cells = append(out[n-1].Cells, c)
			continue
		}
		out = append(out, compatClient{c.Client, clientIcons[c.Client], []compatCell{c}})
	}
	return out
}

// hardware is the OS of a console: the game runs on the machine itself.
const hardware = "hardware"

var onHardware = []string{hardware}

// playTarget is one way to run a console's games: the console itself, a console
// that plays them backwards-compatibly, or Harbor on the OSes it ships for.
type playTarget struct {
	Client string
	OSes   []string
}

// playTargets lists, per console, every client a player can report on. OpenPak
// is a real console or Harbor, OpenPak's own Switch HOME emulator, which ships on
// every OS; the emulator forks are retired, so no other console has a software row.
var playTargets = map[string][]playTarget{
	"Switch":        {{"Switch", onHardware}, {"Harbor", osColumns}},
	"Wii U":         {{"Wii U", onHardware}},
	"3DS":           {{"3DS", onHardware}},
	"Wii":           {{"Wii", onHardware}, {"Wii U", onHardware}},
	"DS":            {{"DS", onHardware}, {"3DS", onHardware}},
	"Xbox":          {{"Xbox", onHardware}, {"Xbox 360", onHardware}},
	"Xbox 360":      {{"Xbox 360", onHardware}, {"Xbox One", onHardware}},
	"PS2":           {{"PS2", onHardware}},
	"PS3":           {{"PS3", onHardware}},
	"PlayStation 3": {{"PS3", onHardware}},
	"PSP":           {{"PSP", onHardware}},
	"Vita":          {{"Vita", onHardware}},
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
func titleRows(reqs []store.Request, tallies []store.CompatTally, plays []core.PlayTally, links []store.ShopLink, playability []store.Playability) []titleRow {
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
	linksOf := map[int64]map[string]string{}
	for _, l := range links {
		if linksOf[l.RequestID] == nil {
			linksOf[l.RequestID] = map[string]string{}
		}
		linksOf[l.RequestID][l.Shop] = l.URL
	}
	playOf := map[int64]map[string]string{}
	for _, p := range playability {
		if playOf[p.RequestID] == nil {
			playOf[p.RequestID] = map[string]string{}
		}
		playOf[p.RequestID][p.Client] = p.Status
	}
	out := make([]titleRow, len(reqs))
	for i, r := range reqs {
		out[i].Request = r
		out[i].links = linksOf[r.ID]
		out[i].play = playOf[r.ID]
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
	emulators := map[string]string{"harbor": "Harbor"}
	oses := map[string]string{"windows": "Windows", "macos": "macOS", "linux": "Linux", "android": "Android", "ios": "iOS"}
	if c, ok := consoles[client]; ok && os == "" {
		return c, hardware
	}
	if c, ok := emulators[client]; ok && oses[os] != "" {
		return c, oses[os]
	}
	return "", ""
}

// shop is a used-game retailer the titles dialog links. A title's own product page
// there (title_shop_links: found with a web search when the title was added, or
// pasted by an admin in the dialog) wins; until one exists the button runs the
// shop's own search. The shops sit behind Cloudflare, so the links are not looked
// up live.
type shop struct {
	Key, Name, Domain string
	search            func(name, console string) string
}

var shops = []shop{
	// webuy.com sends a visitor to their own country's CeX.
	{"cex", "CeX", "webuy.com", func(name, _ string) string {
		return "https://webuy.com/search?stext=" + url.QueryEscape(name)
	}},
	{"gamestop", "GameStop", "gamestop.com", func(name, _ string) string {
		return "https://www.gamestop.com/search/?q=" + url.QueryEscape(name)
	}},
	// Micromania is GameStop's French chain.
	{"micromania", "Micromania", "micromania.fr", func(name, _ string) string {
		return "https://www.micromania.fr/search?q=" + url.QueryEscape(name)
	}},
}

// shopLink is one buy button: the product page when known, else the search.
type shopLink struct {
	Key, Name, URL string
	Product        bool // URL is the game's own page, not a search
}

// ShopsFor is the buy buttons a reader sees: Micromania stands in for GameStop
// for a French reader (it is GameStop's French chain), GameStop for everyone else.
// ponytail: by page language, not country; a French speaker in Canada gets Micromania.
func (t titleRow) ShopsFor(lang string) []shopLink {
	skip := "micromania"
	if lang == "fr" {
		skip = "gamestop"
	}
	return slices.DeleteFunc(t.Shops(), func(l shopLink) bool { return l.Key == skip })
}

// Shops is every shop's link for the title, in shops order (the admin form edits all).
func (t titleRow) Shops() []shopLink {
	out := make([]shopLink, len(shops))
	for i, sh := range shops {
		if u := t.links[sh.Key]; u != "" {
			out[i] = shopLink{sh.Key, sh.Name, u, true}
		} else {
			out[i] = shopLink{sh.Key, sh.Name, sh.search(t.Name, t.Console), false}
		}
	}
	return out
}

// shopURL accepts a product link for a shop only on that shop's own site, over
// https, so an admin form cannot put an arbitrary link on a public page.
func shopURL(key, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	i := slices.IndexFunc(shops, func(sh shop) bool { return sh.Key == key })
	u, err := url.Parse(raw)
	if i < 0 || err != nil || u.Scheme != "https" || u.User != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != shops[i].Domain && !strings.HasSuffix(host, "."+shops[i].Domain) {
		return "", false
	}
	return u.String(), true
}

// handleSetPlayability is the admin form in a title's dialog: how far the game itself runs in
// each client. It goes through store.UpdateRequest like the status form, so the change log and
// last_updated come from one place; a client left blank deletes its row.
func (s *Server) handleSetPlayability(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !slices.Contains(s.AdminAccountIDs, v.AccountID) {
		http.Error(w, "admins only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	patch := map[string]string{}
	for _, client := range r.PostForm["client"] {
		patch[client] = r.PostFormValue("s:" + client)
	}
	if _, err := s.DB.UpdateRequest(r.Context(), id, store.RequestPatch{Playability: patch}, v.AccountID); err != nil {
		if BadTitleEdit(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.log.Error("set playability", "id", id, "err", err)
		http.Error(w, "could not save the playability", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/titles#t-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// handleSetShopLinks is the admin form in a title's dialog: one product link per
// shop, blank to fall back to the search.
func (s *Server) handleSetShopLinks(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !slices.Contains(s.AdminAccountIDs, v.AccountID) {
		http.Error(w, "admins only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	links := map[string]string{}
	for _, sh := range shops {
		u, ok := shopURL(sh.Key, r.PostFormValue(sh.Key))
		if !ok {
			http.Error(w, sh.Name+": a link on "+sh.Domain+" over https, or blank", http.StatusBadRequest)
			return
		}
		links[sh.Key] = u
	}
	for _, sh := range shops {
		if err := s.DB.SetShopLink(r.Context(), id, sh.Key, links[sh.Key]); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "no such title", http.StatusBadRequest)
				return
			}
			s.log.Error("set shop link", "id", id, "err", err)
			http.Error(w, "could not save the links", http.StatusInternalServerError)
			return
		}
	}
	_ = s.DB.AddAudit(r.Context(), v.AccountID, "title.shops", strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/titles#t-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
