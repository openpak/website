package netmap

import (
	"fmt"
	"sort"
	"strings"
)

// Tr translates a phrase of the public map; nil means English. The format
// arguments are filled in by the implementation (fmt.Sprintf on the catalogue text).
type Tr func(key string, args ...any) string

var english = map[string]string{
	"stage.live": "Live", "stage.beta": "Beta", "stage.building": "In development", "stage.planned": "Planned",
	"d.allup": "All %d working", "d.alldown": "None of %d responding", "d.some": "%d of %d not responding",
	"d.planned": "Planned: OpenPak is not running here yet", "d.building": "Being built: not online yet",
	"d.beta": "%s (beta)", "d.dev": "%s (in development)",
	"g.services": "%s services", "g.games": "%s games", "g.partners": "Game partner services",
	"gd.services": "The services a %s uses to sign in, find friends and go online.",
	"gd.games":    "The online game servers for %d %s games.",
	"gd.partners": "Servers that stand in for the studios' own online services.",
	"c.desc":      "Everything a %s uses when it goes online with OpenPak.",
	"w.up":        "Working", "w.degraded": "Partly working", "w.down": "Not responding", "w.idle": "On demand", "w.planned": "Planned", "w.unknown": "Not monitored",
}

func (t Tr) f(key string, args ...any) string {
	if t != nil {
		return t(key, args...)
	}
	return fmt.Sprintf(english[key], args...)
}

// PublicGraph is the simple map on the status page: consoles, the services
// each one uses, and the shared core. It carries no address, port, repo,
// database or server, and no raw service id; it is built from the same Graph,
// so it can never disagree with the admin map.
type PublicGraph struct {
	Generated string       `json:"generated"`
	Nodes     []PublicNode `json:"nodes"`
	Edges     []PublicEdge `json:"edges"`
	Networks  []Network    `json:"networks"`
}

type PublicNode struct {
	ID         string         `json:"id"`
	StageLabel string         `json:"stageLabel,omitempty"`
	Label      string         `json:"label"`
	Kind       string         `json:"kind"` // console | group | core | site
	Net        string         `json:"net,omitempty"`
	Desc       string         `json:"desc"`
	State      string         `json:"state"`
	Detail     string         `json:"detail,omitempty"`
	Stage      string         `json:"stage,omitempty"`
	Members    []PublicMember `json:"members,omitempty"`
}

type PublicMember struct {
	Label string `json:"label"`
	State string `json:"state"`
}

type PublicEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// publicCore is the plain-language name and description of each shared
// service. A shared service not in the table still appears, with its
// title-cased id and a generic line, so a new one is never silently missing.
var publicCore = map[string][2]string{
	"account":         {"Accounts", "Your OpenPak account: one sign-in for every console, with your profile and your friends."},
	"chat":            {"Chat", "Messages between friends, across consoles."},
	"saves":           {"Cloud saves", "Keeps your game saves safe online and lets you carry them between devices."},
	"mods":            {"Mods", "The catalogue of community mods."},
	"leaderboards":    {"Leaderboards", "Rankings for games, kept in one place for every console."},
	"rewards":         {"Rewards", "Points, missions and rewards you earn by playing."},
	"achievements":    {"Achievements", "Trophies and achievements that follow you from one network to another."},
	"title-storage":   {"Game data storage", "Online storage that games use for their own data."},
	"matchmaking":     {"Game matchmaking", "Finds and fills the game rooms you join with friends and other players."},
	"parental":        {"Parental controls", "Family supervision settings and play limits."},
	"news-experiment": {"News", "News and announcements delivered to your console and the website."},
	"bcat":            {"Game news and data", "Event and in-game news data that games download."},
	"voice":           {"Voice chat", "Voice between players in a game."},
	"turn":            {"Connection relay", "Helps players connect when a direct connection is not possible."},
	"pakka":           {"Pakka", "OpenPak's assistant, for questions and alerts."},
	"registry":        {"Network directory", "The signed directory that tells your console where OpenPak lives."},
	"connectivity":    {"Connection helper", "Shared tools that make online play work behind home routers."},
	"website":         {"Website", "openpak.org: sign-up, account, titles, rankings and this page."},
	"openverse":       {"OpenVerse", "The community site for sharing posts and screenshots."},
	"mktv":            {"OpenKart", "The Mario Kart site: highlights, ghosts, rankings and tournaments."},
	"splatnet":        {"OpenSplat", "The Splatoon site: stage rotations and gatherings."},
	"mario-maker":     {"OpenMaker", "The Super Mario Maker course site."},
	"app-api":         {"App and API", "What the OpenPak app and your console use to reach your account."},
}

// publicHidden core services say nothing to a player.
var publicHidden = set("logs", "pokemon-legality")

// adapterNames are the plain names of the per-console services, by id.
var adapterNames = map[string]string{
	"nx-baas": "Sign-in and accounts", "nx-penne": "Notifications", "nx-shop": "Shop", "nx-pctl": "Parental controls",
	"nx-bcat": "News and game data", "nx-share": "Album sharing",
	"nn-account": "Accounts", "nn-boss": "SpotPass", "nn-friends": "Friends", "nn-juxtaposition-api": "Miiverse",
	"nn-juxtaposition-ui": "Miiverse", "nn-nncs": "Connection test", "nn-soap": "Shop and updates", "nn-sssl-dns": "Console connection",
	"wfc-nas": "Sign-in", "wfc-dls": "Downloads", "wfc-gpcm": "Friends and chat", "wfc-gpsp": "Friend search",
	"wfc-serverbrowser": "Game lists", "wfc-qr2": "Matchmaking", "wfc-natneg": "Connection setup", "wfc-gamestats": "Game stats",
	"wfc-sake": "Game records", "wfc-race": "Mario Kart Wii", "wfc-api": "Website link",
	"np-account": "Sign-in", "np-community": "Online play", "np-score": "Rankings", "np-trophy": "Trophies", "np-tus": "Game data storage",
	"np-ubs": "Online saves", "np-update": "Game update checks", "np-uploader": "Message attachments", "np-matching2-recorder": "Online play tests",
	"ps4-update":     "System update checks",
	"x360-dashboard": "Dashboard home", "x360-liveinfo": "Live configuration", "x360-msgserver": "Messages", "x360-notice": "Connection test pages",
	"x360-profile": "Profiles", "x360-progress": "Achievements", "x360-som": "System modules", "x360-store": "Homebrew store",
	"x360-xarb": "Ranked match checks", "x360-xbos": "Licences", "x360-xedl": "Profile downloads", "x360-xmatch": "Matchmaking",
	"x360-xmessaging": "Message inbox", "x360-xpnfront": "Friends and presence", "x360-xsig": "Certificates", "x360-xstats": "Leaderboards",
	"x360-xsts": "Sign-in tokens", "x360-xuacs": "Accounts", "x360-dns": "Console connection", "x360-gateway": "Secure gateway",
	"x360-kdc": "Sign-in", "x360-xexds": "Connection probe",
	"xb-account": "Accounts", "xb-auth": "Sign-in", "xb-autoupd": "Auto-update", "xb-dns": "Console connection", "xb-enroll": "Console pairing",
	"xb-fbserver": "Feedback", "xb-msgserver": "Messages", "xb-social": "Friends and presence", "xb-stfd": "Online storage",
	"xb-tou": "Terms of use", "xb-voice": "Voice chat", "xb-xmessaging": "Messaging", "xb-xsig": "Certificates", "xb-gateway": "Gateway",
	"xbl": "Online services",
}

func plainService(n Node) string {
	if l, ok := adapterNames[n.ID]; ok {
		return l
	}
	switch n.Kind {
	case "game", "vendor":
		return titleCase(strings.TrimPrefix(n.ID, "switch-"))
	}
	return "Console service"
}

// Public derives the player-facing map.
func Public(g Graph, tr Tr) PublicGraph {
	pg := PublicGraph{Generated: g.Generated.UTC().Format("2006-01-02T15:04:05Z"), Networks: g.Networks}
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	pub := map[string]string{} // node id -> public node id
	netLabel := map[string]string{}
	for _, c := range g.Networks {
		netLabel[c.ID] = c.Label
	}

	// Shared services and sites.
	for _, n := range g.Nodes {
		if n.Kind != "core" && n.Kind != "site" || publicHidden[n.ID] {
			continue
		}
		label, desc := titleCase(n.ID), "A shared OpenPak service."
		if p, ok := publicCore[n.ID]; ok {
			label, desc = p[0], p[1]
		}
		id := "s-" + n.ID
		pub[n.ID] = id
		pg.Nodes = append(pg.Nodes, PublicNode{ID: id, Label: label, Kind: n.Kind, Desc: desc, State: n.State, Detail: tr.f("w." + wordKey(n.State))})
	}
	// Consoles and their groups.
	type gk struct{ net, kind string }
	members := map[gk][]Node{}
	for _, n := range g.Nodes {
		if n.Kind != "adapter" && n.Kind != "game" && n.Kind != "vendor" && !(n.Kind == "infra" && len(n.Networks) > 0) {
			continue
		}
		if len(n.Networks) == 0 {
			continue
		}
		kind := "services"
		switch n.Kind {
		case "game":
			kind = "games"
		case "vendor":
			kind = "partners"
		}
		members[gk{n.Networks[0], kind}] = append(members[gk{n.Networks[0], kind}], n)
		pub[n.ID] = "" // resolved below to the group
	}
	groupOf := map[string]string{}
	for _, c := range g.Networks {
		cn := byID["console-"+c.ID]
		cid := "c-" + c.ID
		pg.Nodes = append(pg.Nodes, PublicNode{ID: cid, Label: c.Label, Kind: "console", Net: c.ID, Stage: c.Stage,
			Desc: tr.f("c.desc", c.Label), State: cn.State, Detail: consoleDetail(cn, c.Stage, tr), StageLabel: tr.f("stage." + c.Stage)})
		for _, kind := range []string{"services", "games", "partners"} {
			ms := members[gk{c.ID, kind}]
			if len(ms) == 0 {
				continue
			}
			sort.Slice(ms, func(i, j int) bool { return plainService(ms[i]) < plainService(ms[j]) })
			gid := "g-" + c.ID + "-" + kind
			node := PublicNode{ID: gid, Kind: "group", Net: c.ID}
			down, upN := 0, 0
			for _, m := range ms {
				node.Members = append(node.Members, PublicMember{Label: plainService(m), State: m.State})
				groupOf[m.ID+"|"+c.ID] = gid
				if m.State == Down || m.State == Degraded {
					down++
				} else {
					upN++
				}
			}
			switch {
			case down == 0:
				node.State = Up
			case upN == 0:
				node.State = Down
			default:
				node.State = Degraded
			}
			switch kind {
			case "services":
				node.Label = tr.f("g.services", c.Label)
				node.Desc = tr.f("gd.services", c.Label)
			case "games":
				node.Label = tr.f("g.games", c.Label)
				node.Desc = tr.f("gd.games", len(ms), c.Label)
			case "partners":
				node.Label = tr.f("g.partners")
				node.Desc = tr.f("gd.partners")
			}
			node.Detail = groupDetail(node.State, down, len(ms), tr)
			pg.Nodes = append(pg.Nodes, node)
			pg.Edges = append(pg.Edges, PublicEdge{From: cid, To: gid})
		}
	}
	// Lines from groups and sites into the shared core.
	seen := map[PublicEdge]bool{}
	for _, e := range g.Edges {
		if e.Kind == "store" || e.Kind == "network" {
			continue
		}
		to := pub[e.To]
		if to == "" || to == "s-"+e.From {
			continue
		}
		var from string
		if f := byID[e.From]; f.Kind == "site" {
			from = pub[e.From]
		} else if len(f.Networks) > 0 {
			from = groupOf[e.From+"|"+f.Networks[0]]
		}
		if from == "" {
			continue
		}
		pe := PublicEdge{from, to}
		if !seen[pe] {
			seen[pe] = true
			pg.Edges = append(pg.Edges, pe)
		}
	}
	return pg
}

func wordKey(s string) string {
	switch s {
	case Up, Degraded, Down, Idle, Planned:
		return s
	}
	return "unknown"
}

func groupDetail(state string, bad, total int, tr Tr) string {
	switch state {
	case Up:
		return tr.f("d.allup", total)
	case Down:
		return tr.f("d.alldown", total)
	}
	return tr.f("d.some", bad, total)
}

func consoleDetail(n Node, stage string, tr Tr) string {
	if n.State == Planned {
		if stage == "planned" {
			return tr.f("d.planned")
		}
		return tr.f("d.building")
	}
	w := tr.f("w." + wordKey(n.State))
	switch stage {
	case "beta":
		return tr.f("d.beta", w)
	case "building":
		return tr.f("d.dev", w)
	}
	return w
}
