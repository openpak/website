package netmap

import (
	"sort"
	"strings"

	"github.com/openpak/website/internal/netorder"
)

// consoleSpec lists the console networks, in the shared network order (internal/netorder).
// Family is the palette slot: consoles that share a stack share a hue, and the
// node always carries its name as well, so colour is never the only code.
var consoleSpec = ordered([]consoleDef{
	{"switch", "Switch", "Switch", 1},
	{"wiiu", "Wii U", "Wii U", 2},
	{"3ds", "3DS", "3DS", 2},
	{"wii", "Wii", "Wii", 3},
	{"ds", "DS", "DS", 3},
	{"ps3", "PS3", "PS3", 4},
	{"ps4", "PS4", "PS4", 4},
	{"ps5", "PS5", "PS5", 4},
	{"xbox360", "Xbox 360", "Xbox 360", 5},
	{"xbox", "Original Xbox", "Xbox", 5},
})

type consoleDef struct {
	ID, Label, Feature string
	Family             int
}

// ordered puts the consoles in the one network order (family, then generation, oldest first), so
// the map's lanes agree with the status pages.
func ordered(in []consoleDef) []consoleDef {
	sort.SliceStable(in, func(i, j int) bool { return netorder.ConsoleRank(in[i].ID) < netorder.ConsoleRank(in[j].ID) })
	return in
}

var consoleIDs = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range consoleSpec {
		m[c.ID] = true
	}
	return m
}()

// idOf is the node id: the unit without "openpak-" and ".service".
func idOf(unit string) string {
	return strings.TrimPrefix(strings.TrimSuffix(unit, ".service"), "openpak-")
}

var (
	infraIDs = set("postgres", "redis", "mongo", "minio", "mailpit", "traefik", "edge", "switch-bridge", "status",
		"nn-sssl-dns", "x360-dns", "xb-dns")
	coreIDs = set("account", "chat", "saves", "mods", "leaderboards", "rewards", "achievements", "title-storage",
		"parental", "pokemon-legality", "news-experiment", "bcat", "voice", "turn", "pakka", "registry", "connectivity")
	siteIDs   = set("website", "openverse", "mktv", "splatnet", "mario-maker")
	vendorIDs = set("eos", "demonware", "photon", "playfab", "klei", "shiro", "t17", "ugs", "battlenet")
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// adapterPrefixes name the per-console adapter stacks.
var adapterPrefixes = []struct{ prefix, nets string }{
	{"nx-", "switch"}, {"nn-", "wiiu,3ds"}, {"wfc-", "wii,ds"}, {"np-", "ps3"},
	{"ps4-", "ps4"}, {"ps5-", "ps5"}, {"x360-", "xbox360"}, {"xb-", "xbox"},
}

// networkOverride places the services that are not named after their console.
// It is the one hand-kept table, and only for names that carry no hint; a
// quadlet's org.openpak.network label or the opsd network map always wins, and
// anything not covered lands in the Unassigned lane rather than being guessed.
var networkOverride = map[string][]string{
	"xbl": {"xbox"}, "xb-gateway": {"xbox"},
	"nn-nncs":     {"wiiu", "3ds", "switch"},
	"judd":        {"wiiu"},
	"bayonetta-2": {"wiiu"}, "monster-hunter-3-ultimate": {"wiiu", "3ds"},
	"sonic-all-stars-racing-transformed-wiiu": {"wiiu"}, "terraria-wiiu": {"wiiu"},
	"pokemon-home": {"switch"}, "pokemon-global-link": {"ds"},
	"among-us": {"switch"}, "astroneer": {"switch"}, "fall-guys": {"switch"}, "fall-guys-round": {"switch"},
	"stumble-guys": {"switch"}, "unrailed": {"switch"}, "minecraft-dungeons": {"switch"},
	"spelunky2":    {"switch"},
	"rotationator": {"wiiu"},
	"core-keeper":  {"switch"}, "go-go-town": {"switch"}, "metal-gear-solid-peace-walker": {"switch"},
	"plateup": {"switch"}, "terraria": {"switch"},
}

// kindOf decides what a service is from its name and its networks.
func kindOf(id, image string, nets []string) string {
	switch {
	case infraIDs[id]:
		return "infra"
	case coreIDs[id]:
		return "core"
	case siteIDs[id]:
		return "site"
	case vendorIDs[id]:
		return "vendor"
	}
	for _, p := range adapterPrefixes {
		if strings.HasPrefix(id, p.prefix) {
			return "adapter"
		}
	}
	if id == "xbl" {
		return "adapter"
	}
	if len(nets) > 0 {
		return "game"
	}
	return "service"
}

// networksOf resolves the console networks of a service, first from what the
// host said (label or map), then from its name.
func networksOf(id string, hostNets []string) []string {
	var out []string
	for _, n := range hostNets {
		if consoleIDs[n] {
			out = append(out, n)
		}
	}
	if len(out) > 0 {
		if o, ok := networkOverride[id]; ok && len(o) > len(out) {
			return o
		}
		return out
	}
	for _, n := range hostNets {
		if n == "shared" {
			return nil
		}
	}
	if o, ok := networkOverride[id]; ok {
		return o
	}
	for _, p := range adapterPrefixes {
		if strings.HasPrefix(id, p.prefix) {
			return strings.Split(p.nets, ",")
		}
	}
	if strings.HasPrefix(id, "switch-") {
		return []string{"switch"}
	}
	if strings.HasSuffix(id, "-wiiu") {
		return []string{"wiiu"}
	}
	return nil
}

// gatusFor says which availability checks speak for which service. The checks
// are named for what a person does ("Cloud saves"), not for the unit, so this
// is the bridge; a check name missing from the page simply contributes nothing.
var gatusFor = map[string][]string{
	"account":       {"Accounts"},
	"saves":         {"Cloud saves"},
	"chat":          {"Messages"},
	"leaderboards":  {"Game leaderboards"},
	"achievements":  {"Game achievements"},
	"title-storage": {"Game title storage"},
	"website":       {"Website", "Rankings", "Rankings API", "Console link page"},
	"postgres":      {"Database connections"},
	"app-api":       {"Accounts"},
}

// gatusGroupConsole maps an availability group to the console it speaks for.
func gatusGroupConsole(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "360"):
		return "xbox360"
	case strings.Contains(t, "original xbox"):
		return "xbox"
	case strings.Contains(t, "ps3"):
		return "ps3"
	case strings.Contains(t, "ps4"):
		return "ps4"
	case strings.Contains(t, "ps5"):
		return "ps5"
	case strings.Contains(t, "on a console"):
		return "switch"
	}
	return ""
}

// Names shown for the services whose id is not a good label.
var coreLabels = map[string]string{
	"account": "Account", "chat": "Chat", "saves": "Cloud saves", "mods": "Mods", "leaderboards": "Leaderboards",
	"rewards": "Rewards", "achievements": "Achievements", "title-storage": "Title storage", "parental": "Parental controls",
	"news-experiment": "News", "bcat": "BCAT", "voice": "Voice", "turn": "TURN relay", "pakka": "Pakka",
	"registry": "Network registry", "connectivity": "Connectivity", "website": "Website", "openverse": "OpenVerse",
	"mktv": "OpenKart", "splatnet": "OpenSplat", "mario-maker": "OpenMaker", "postgres": "Postgres", "redis": "Redis",
	"mongo": "Mongo", "pokemon-legality": "Pokemon legality check", "minio": "MinIO", "mailpit": "Mailpit", "traefik": "Traefik", "edge": "Edge", "switch-bridge": "Switch bridge",
	"status": "Status page", "app-api": "App API", "logs": "Logs", "zerotier": "ZeroTier",
	"nn-sssl-dns": "Console DNS (Nintendo)", "x360-dns": "Console DNS (Xbox 360)", "xb-dns": "Console DNS (Xbox)",
}

// label is the admin label: nice for core, the id itself for adapters (the
// admin knows them by it), title case for games.
func label(id, kind string) string {
	if l, ok := coreLabels[id]; ok {
		return l
	}
	if kind == "game" {
		return titleCase(strings.TrimPrefix(id, "switch-"))
	}
	return id
}

var smallWords = set("of", "the", "and", "vs", "for", "in", "a")

func titleCase(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		switch {
		case p == "":
		case i > 0 && smallWords[p]:
		case len(p) <= 2 && (p == "ii" || p == "iv" || p == "3d" || p == "xx"):
			parts[i] = strings.ToUpper(p)
		default:
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
