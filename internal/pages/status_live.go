package pages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
)

// /status is a live overview for players: is each network working, and who is playing what
// right now. Per-service detail lives on status.openpak.org, which this page links to. The
// page renders the current state itself (so it works without scripting) and status-live.js
// then polls /status/live.json and updates it in place.

// statusNetworks are the cards, in reading order. Name picks the entry in `networks` (stage and
// player namespaces); ID is the id status.openpak.org rolls its checks up under.
var statusNetworks = []struct{ ID, Name string }{
	{"switch", "Switch"}, {"wiiu-3ds", "Wii U / 3DS"}, {"wii-ds", "Wii / DS"}, {"xbox360", "Xbox 360"},
	{"xbox", "Xbox"}, {"ps3", "PlayStation 3"}, {"ps4", "PlayStation 4"},
	{"shared", ""}, {"websites", ""},
}

type liveNet struct {
	ID, Name string
	Stage    string // "" when live; else the word for beta / in development / planned
	State    string // ok | partial | down | idle
	Label    string
	Players  int
	Counted  bool   // Players is a real number
	Text     string // "12 online"
}

type liveTitle struct {
	ID, Name, Console, Slug, Art string
	Players                      int
	Text                         string
}

type liveView struct {
	State    string // ok | warn | bad
	Headline string
	Sub      string
	Problems []string
	Online   int
	Playing  int
	CountsOK bool
	Networks []liveNet
	Titles   []liveTitle
	Quiet    string
	Updated  string
	Ext      bool
}

// checkGameServers is a variable so a preview can show a failing server.
var checkGameServers = CheckGameServers

// liveState is the overview's short-lived cache, one per server.
type liveState struct {
	mu   sync.Mutex
	at   time.Time
	lang string
	v    *liveView
}

type catEntry struct{ Name, Console string }

// titleEntries indexes the catalogue by title id with each title's console, for the cover art.
func (s *Server) titleEntries(ctx context.Context) map[string]catEntry {
	reqs, err := s.DB.ListRequests(ctx, "title", "")
	if err != nil {
		return nil
	}
	out := map[string]catEntry{}
	for _, r := range reqs {
		if r.Name == "" {
			continue
		}
		for _, id := range append([]string{r.TitleID}, r.TitleIDs...) {
			if id != "" {
				out[strings.ToUpper(id)] = catEntry{r.Name, r.Console}
			}
		}
	}
	return out
}

// statusLive builds the overview. Cached for a few seconds per language: a poll every 30 s from
// every open tab should not turn into a health sweep each time.
func (s *Server) statusLive(ctx context.Context, lang string) *liveView {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.v != nil && s.live.lang == lang && time.Since(s.live.at) < 8*time.Second {
		return s.live.v
	}
	v := s.buildLive(ctx, lang)
	s.live.at, s.live.lang, s.live.v = time.Now(), lang, v
	return v
}

func (s *Server) buildLive(ctx context.Context, lang string) *liveView {
	t := func(k string) string { return i18n.T(lang, k) }
	v := &liveView{Updated: time.Now().UTC().Format(time.RFC3339)}

	coreOK := s.Core.Healthy(ctx) == nil
	dbOK := s.DB.Ping(ctx) == nil
	storeOK := s.Media == nil || s.Media.Healthy(ctx) == nil
	ext := s.externalStatus(ctx)
	v.Ext = ext.Reached

	// Who is playing, privately: counts only, never anyone's name.
	var titlesRaw []core.TitlePlayers
	var perNetwork map[string]int
	if s.Invites != nil {
		if titles, nets, total, err := s.Invites.PlayerCounts(ctx); err == nil {
			titlesRaw, perNetwork = titles, nets
			v.CountsOK, v.Online = true, total
		} else {
			s.log.Error("player counts", "err", err)
		}
	}
	entries := s.titleEntries(ctx)
	byTitle := map[string]int{}
	for _, tp := range titlesRaw {
		byTitle[strings.ToUpper(tp.TitleID)] += tp.Players
	}
	seen := map[string]bool{}
	for _, tp := range titlesRaw {
		if tp.Players <= 0 {
			continue
		}
		e, ok := entries[strings.ToUpper(tp.TitleID)]
		if !ok {
			e, ok = entries[baseTitleID(tp.TitleID)]
		}
		if !ok || seen[e.Name+"|"+e.Console] {
			continue
		}
		seen[e.Name+"|"+e.Console] = true
		lt := liveTitle{ID: strings.ToUpper(tp.TitleID), Name: e.Name, Console: e.Console, Slug: consoleSlug(e.Console), Players: tp.Players}
		if e.Console == "Switch" {
			lt.Art = "/titles/icon/" + lt.ID
		} else {
			lt.Art = artPath(e.Console, e.Name)
		}
		v.Titles = append(v.Titles, lt)
	}
	sort.SliceStable(v.Titles, func(i, j int) bool {
		if v.Titles[i].Players != v.Titles[j].Players {
			return v.Titles[i].Players > v.Titles[j].Players
		}
		return v.Titles[i].Name < v.Titles[j].Name
	})
	for i := range v.Titles {
		v.Titles[i].Text = fmt.Sprintf("%d %s", v.Titles[i].Players, t("status.v.playing"))
	}
	v.Playing = len(v.Titles)
	v.Quiet = t("status.v.quiet")

	// What this site can see for itself: its own core, database and storage, and the Switch game
	// servers it health-checks. They roll into the same cards as the outside checks.
	local := map[string]string{} // network id -> worst local state
	var problems []string
	worse := func(id, st string) {
		if st == "down" || local[id] == "" {
			local[id] = st
		} else if local[id] != "down" {
			local[id] = "partial"
		}
	}
	catalogue := TitleCatalogue(ctx, s.DB)
	for _, g := range checkGameServers(ctx, catalogue) {
		if g.Checkable && !g.Up {
			worse("switch", "partial")
			problems = append(problems, "Switch: "+g.Name)
		}
	}
	for _, c := range []struct {
		ok   bool
		name string
	}{{coreOK, t("status.core")}, {dbOK, t("status.db")}, {storeOK, t("status.storage")}} {
		if !c.ok {
			worse("shared", "down")
			problems = append(problems, t("status.v.shared")+": "+c.name)
		}
	}
	problems = append(problems, ext.Problems...)

	stages := s.consoleStages(ctx)
	for _, sn := range statusNetworks {
		n := liveNet{ID: sn.ID, Name: sn.Name, State: "idle", Label: t("status.unchecked")}
		var def *network
		for i := range networks {
			if networks[i].Name == sn.Name {
				def = &networks[i]
			}
		}
		if sn.ID == "shared" {
			n.Name = t("status.v.shared")
		} else if sn.ID == "websites" {
			n.Name = t("status.v.websites")
		}
		if x, ok := ext.Networks[sn.ID]; ok {
			n.State = x.State
		}
		if l := local[sn.ID]; l != "" && (n.State == "idle" || n.State == "ok" || l == "down") {
			n.State = l
		}
		if def != nil {
			st := def.stateAt(stages)
			if st == "planned" && n.State == "idle" {
				continue // nothing deployed and nothing checked: not a card yet
			}
			if st != "live" {
				n.Stage = t("status.net." + st)
			}
			for _, ns := range def.Namespaces {
				n.Players += perNetwork[ns]
			}
			n.Counted = v.CountsOK && len(def.Namespaces) > 0
		}
		switch n.State {
		case "ok":
			n.Label = t("status.ok")
		case "partial":
			n.Label = t("status.v.partial")
		case "down":
			n.Label = t("status.v.down")
		}
		if n.Counted {
			n.Text = fmt.Sprintf("%d %s", n.Players, t("status.pill.online.many"))
		}
		v.Networks = append(v.Networks, n)
	}

	v.Problems = problems
	switch {
	case len(problems) == 0:
		v.State, v.Headline = "ok", t("status.all.ok")
		v.Sub = t("status.v.sub")
	default:
		v.State, v.Headline = "warn", t("status.all.bad")
		if len(problems) > 3 {
			v.Sub = strings.Join(problems[:3], ", ") + " " + strings.ReplaceAll(t("status.v.more"), "{n}", fmt.Sprint(len(problems)-3))
		} else {
			v.Sub = strings.Join(problems, ", ")
		}
	}
	return v
}

// handleStatus is the public status page.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	lang := i18n.FromRequest(r)
	type statusView struct {
		baseData
		*liveView
		MapOn bool
	}
	view := statusView{
		baseData: s.data(r, s.viewerOf(w, r), i18n.T(lang, "status.title")),
		liveView: s.statusLive(r.Context(), lang),
		MapOn:    s.NetMap != nil,
	}
	s.render(w, http.StatusOK, "status.html", view)
}

// handleStatusLive is the page's poll: the same numbers, as JSON, in the visitor's language.
func (s *Server) handleStatusLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	_ = json.NewEncoder(w).Encode(s.statusLive(r.Context(), i18n.FromRequest(r)))
}
