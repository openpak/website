package pages

import (
	"context"
	"strings"

	"github.com/openpak/website/internal/store"
)

// The console networks OpenPak replaces, and how far each one actually is.
//
// How far a console is comes from one place: the feature rows on /features,
// which the operator keeps in the database. The status page, the landing
// shelf and /downloads all read it through consoleStates, so they cannot
// disagree with /features or with each other. The State written below is
// only the fallback for a console with no feature rows (a fresh database), and
// it is deliberately the honest one: a status page that showed planned work as
// green would be worthless.
type network struct {
	Name string
	// Namespaces are the adapter namespaces the core knows this network by,
	// summed for the count. None means nothing is deployed, so nothing to count.
	Namespaces []string
	// Consoles are the /features console names this network covers.
	Consoles []string
	State    string // fallback: live | beta | building (shown "In development") | planned
}

// networks reads top to bottom on the status page, most finished first.
var networks = []network{
	{Name: "Switch", Namespaces: []string{"switch"}, Consoles: []string{"Switch"}, State: "live"},
	{Name: "Wii U / 3DS", Namespaces: []string{"wiiu", "3ds"}, Consoles: []string{"Wii U", "3DS"}, State: "beta"},
	{Name: "Wii / DS", Namespaces: []string{"wfc"}, Consoles: []string{"Wii", "DS"}, State: "beta"},
	{Name: "Xbox", Namespaces: []string{"xbox"}, Consoles: []string{"Xbox"}, State: "building"},
	{Name: "Xbox 360", Namespaces: []string{"x360"}, Consoles: []string{"Xbox 360"}, State: "building"},
	{Name: "PlayStation 3", Namespaces: []string{"np"}, Consoles: []string{"PS3"}, State: "building"},
	{Name: "PSP", Consoles: []string{"PSP"}, State: "planned"},
}

// platform is one console family and how far OpenPak is with it, in the titles
// list's words (live, beta, alpha, in-development, planned). Status is the
// fallback, as above.
type platform struct{ Name, Console, Status string }

var platforms = []platform{
	{"Nintendo Switch", "Switch", "live"},
	{"Wii U", "Wii U", "beta"},
	{"Nintendo 3DS", "3DS", "beta"},
	{"Wii", "Wii", "beta"},
	{"Nintendo DS", "DS", "beta"},
	{"Xbox", "Xbox", "in-development"},
	{"PlayStation 3", "PS3", "in-development"},
	{"Xbox 360", "Xbox 360", "in-development"},
	{"PlayStation Portable", "PSP", "planned"},
	{"PlayStation Vita", "Vita", "planned"},
	{"PlayStation 4", "PS4", "planned"},
	{"PlayStation 5", "PS5", "planned"},
	{"Xbox One", "Xbox One", "planned"},
}

// stageRank orders the platform words; a console is as far as its furthest feature.
var stageRank = map[string]int{"live": 4, "beta": 3, "alpha": 2, "in-development": 1, "planned": 0}

// featureStage maps a /features status to a platform word; "" is a status that
// says nothing about progress (broken, not-feasible).
func featureStage(status string) string {
	switch status {
	case "working", "live":
		return "live"
	case "beta":
		return "beta"
	case "alpha", "partial", "scaffold":
		return "alpha"
	case "in-development":
		return "in-development"
	case "planned", "requestable":
		return "planned"
	}
	return ""
}

// consoleStages is, per console, the furthest stage any feature row reaches. A
// row for "Wii U / 3DS" counts for both.
func consoleStages(feats []store.Request) map[string]string {
	out := map[string]string{}
	for _, f := range feats {
		st := featureStage(f.Status)
		if st == "" {
			continue
		}
		for _, c := range strings.Split(f.Console, "/") {
			c = strings.TrimSpace(c)
			if cur, ok := out[c]; c != "" && (!ok || stageRank[st] > stageRank[cur]) {
				out[c] = st
			}
		}
	}
	return out
}

// consoleStages reads the feature rows; on a database error every console
// keeps its fallback.
func (s *Server) consoleStages(ctx context.Context) map[string]string {
	feats, err := s.DB.ListRequests(ctx, "feature", "")
	if err != nil {
		s.log.Error("list features", "err", err)
		return nil
	}
	return consoleStages(feats)
}

// platformsAt is the platform list with each status taken from stages.
func platformsAt(stages map[string]string) []platform {
	out := make([]platform, len(platforms))
	for i, p := range platforms {
		if st, ok := stages[p.Console]; ok {
			p.Status = st
		}
		out[i] = p
	}
	return out
}

// stateAt is a network's status-page state: its furthest console's stage, in
// the status page's words, or the fallback when no console has a feature row.
func (n network) stateAt(stages map[string]string) string {
	best, found := "", false
	for _, c := range n.Consoles {
		if st, ok := stages[c]; ok && (!found || stageRank[st] > stageRank[best]) {
			best, found = st, true
		}
	}
	if !found {
		return n.State
	}
	if best == "alpha" || best == "in-development" {
		return "building"
	}
	return best
}
