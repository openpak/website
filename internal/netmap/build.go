package netmap

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

var (
	idleHint  = regexp.MustCompile(`(?i)oneshot|start manually|capture only|no \[Install\]`)
	ghcrImage = regexp.MustCompile(`^ghcr\.io/openpak/([a-z0-9-]+)(?::|@|$)`)
)

// local names a service uses for "this box".
func isLocalHost(h string) bool {
	switch h {
	case "127.0.0.1", "localhost", "::1", "host.containers.internal", "host.docker.internal":
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsPrivate() && !strings.HasPrefix(h, "10.89.") {
		return true // the box's LAN address, the way the website reaches opsd
	}
	return false
}

type built struct {
	n     Node
	raws  []RawNode
	hosts []string
}

// Build turns the hosts' reports into the graph.
func Build(in Input) Graph {
	g := Graph{Generated: in.Now}
	byID := map[string]*built{}
	var order []string
	byName := map[string]string{} // container name, unit id, openpak-<unit id> -> id
	byIP := map[string]string{}
	byPort := map[string]map[int][]string{} // host name -> port -> ids
	hostIP := map[string]string{}

	for _, h := range in.Hosts {
		hostIP[h.IP] = h.Name
		row := HostRow{Name: h.Name, IP: h.IP, Up: h.Err == "", Error: h.Err, Count: len(h.Raw.Services)}
		g.Hosts = append(g.Hosts, row)
		if h.Err != "" {
			g.Notes = append(g.Notes, fmt.Sprintf("Host %s did not answer (%s): its services are missing from the map.", h.Name, h.Err))
			continue
		}
		byPort[h.Name] = map[int][]string{}
		for _, r := range h.Raw.Services {
			id := idOf(r.Unit)
			b := byID[id]
			if b == nil {
				b = &built{}
				byID[id] = b
				order = append(order, id)
			}
			b.raws = append(b.raws, r)
			b.hosts = append(b.hosts, h.Name)
			byName[r.Name], byName[id], byName["openpak-"+id] = id, id, id
			if r.IP != "" {
				byIP[r.IP] = id
			}
			for _, p := range r.Ports {
				if p.IP == "" || p.IP == "0.0.0.0" || p.IP == "127.0.0.1" {
					byPort[h.Name][p.Port] = appendOnce(byPort[h.Name][p.Port], id)
				}
			}
		}
	}

	// Nodes.
	for _, id := range order {
		b := byID[id]
		first := b.raws[0]
		hostNets := first.Networks
		nets := networksOf(id, hostNets)
		if len(nets) == 0 && vendorIDs[id] {
			nets = []string{"switch"} // the vendor hosts exist for Switch titles
		}
		kind := kindOf(id, first.Image, nets)
		if kind == "core" || kind == "site" {
			nets = nil
		}
		n := Node{ID: id, Kind: kind, Networks: nets, Hosts: b.hosts, Logs: strings.TrimSuffix(first.Unit, ".service")}
		n.Label = label(id, kind)
		n.Desc = describe(first.Description)
		n.Version, n.Repo = first.Version, repoOf(first)
		if n.Version == "" && strings.Contains(first.Image, "@sha256:") {
			n.Version = "pinned by digest"
		}
		n.State, n.Detail = Up, ""
		for i, r := range b.raws {
			st, d := stateOf(r)
			if i == 0 || worse(st, n.State) {
				n.State, n.Detail = st, d
			}
			if r.Labels["update"] == "available" {
				n.Update = true
			}
			if r.StartedAt != nil && n.Since == "" {
				n.Since = *r.StartedAt
			}
		}
		if len(b.hosts) > 1 {
			n.Detail += " (on " + strings.Join(b.hosts, " and ") + ")"
		}
		if kind == "game" && len(nets) > 0 {
			n.Group = "games:" + nets[0]
		}
		b.n = n
	}

	// A helper named for a game ("pikmin-3-histgrm") lives on that game's network.
	for _, id := range order {
		n := &byID[id].n
		if len(n.Networks) > 0 || n.Kind == "core" || n.Kind == "site" || n.Kind == "infra" {
			continue
		}
		for _, other := range order {
			if o := byID[other].n; len(o.Networks) > 0 && o.Kind == "game" && strings.HasPrefix(id, other+"-") {
				n.Networks, n.Kind = o.Networks, "game"
				n.Group = "games:" + o.Networks[0]
				break
			}
		}
	}

	// Lines.
	type ek struct{ from, to, kind string }
	edges := map[ek]*Edge{}
	add := func(from, to, kind, via string) {
		if from == to || from == "" || to == "" {
			return
		}
		k := ek{from, to, kind}
		e := edges[k]
		if e == nil {
			e = &Edge{From: from, To: to, Kind: kind}
			edges[k] = e
		}
		if via != "" && len(e.Via) < 6 && !contains(e.Via, via) {
			e.Via = append(e.Via, via)
		}
	}
	dbs := map[string]map[DB]bool{}
	for _, id := range order {
		b := byID[id]
		for i, r := range b.raws {
			host := b.hosts[i]
			for _, ref := range r.Refs {
				to := resolve(ref, host, byName, byIP, byPort, hostIP)
				if to == "" || to == id {
					continue
				}
				kind := "calls"
				if storeIDs[to] {
					kind = "store"
					if dbs[id] == nil {
						dbs[id] = map[DB]bool{}
					}
					dbs[id][DB{Engine: label(to, ""), Name: ref.Path}] = true
				}
				add(id, to, kind, ref.Var)
			}
			for _, u := range r.Needs {
				if to := idOf(u); byID[to] != nil {
					add(id, to, "needs", "")
				}
			}
		}
	}
	for id, m := range dbs {
		n := &byID[id].n
		for d := range m {
			n.DB = append(n.DB, d)
		}
		sort.Slice(n.DB, func(i, j int) bool { return fmt.Sprint(n.DB[i]) < fmt.Sprint(n.DB[j]) })
	}

	var nodes []Node
	for _, id := range order {
		nodes = append(nodes, byID[id].n)
	}

	// Virtual nodes: things the map should show that are not containers.
	have := func(id string) bool { return byID[id] != nil }
	if have("account") {
		nodes = append(nodes, Node{ID: "app-api", Label: "App API", Kind: "site", Virtual: true, State: Unknown,
			Desc: "The public API at api.openpak.org that the OpenPak app and the console clients call; it is served by the account core.", Hosts: nil})
		add("app-api", "account", "calls", "")
	}
	logsState, logsDetail := Unknown, "Log storage is not configured on the website"
	if in.LogsOn {
		logsState, logsDetail = Up, "Log database configured; openpak-logd ships every unit's output"
	}
	nodes = append(nodes, Node{ID: "logs", Label: "Logs", Kind: "core", Virtual: true, State: logsState, Detail: logsDetail, Logs: "",
		Desc: "Every unit's output, kept 90 days per service by openpak-logd on each box and read at /admin/logs."})
	if have("website") {
		add("website", "logs", "calls", "")
	}
	if have("postgres") {
		add("logs", "postgres", "store", "")
	}
	nodes = append(nodes, Node{ID: "zerotier", Label: "ZeroTier", Kind: "infra", Virtual: true, State: Unknown, Detail: "Not monitored",
		Desc: "The private mesh network between the boxes and the test consoles."})
	for _, id := range order {
		if strings.Contains(strings.ToLower(byID[id].n.Desc), "zerotier") {
			add(id, "zerotier", "network", "")
		}
	}
	for _, h := range g.Hosts {
		st, d := Up, fmt.Sprintf("Agent answered; %d services", h.Count)
		if !h.Up {
			st, d = Down, "The host agent did not answer: "+h.Error
		}
		nodes = append(nodes, Node{ID: "host-" + h.Name, Label: hostLabel(h), Kind: "host", Virtual: true, State: st, Detail: d,
			Desc: fmt.Sprintf("A server. It runs %d of the services on this map.", h.Count), Hosts: []string{h.Name}})
	}

	applyStatus(nodes, in.Status)

	// Console networks.
	for _, c := range consoleSpec {
		stage := stageWord(in.Stages[c.Feature])
		g.Networks = append(g.Networks, Network{ID: c.ID, Label: c.Label, Family: c.Family, Stage: stage})
		cn := Node{ID: "console-" + c.ID, Label: c.Label, Kind: "console", Networks: []string{c.ID}, Stage: stage, Virtual: true}
		var total, running int
		for _, n := range nodes {
			if !contains(n.Networks, c.ID) || n.Kind == "console" {
				continue
			}
			if n.Kind == "infra" {
				add("console-"+c.ID, n.ID, "network", "")
				continue
			}
			total++
			if n.State == Up || n.State == Idle {
				running++
			}
		}
		switch {
		case total == 0:
			cn.State, cn.Detail = Planned, "Nothing is deployed for this console yet"
		case running == total:
			cn.State, cn.Detail = Up, fmt.Sprintf("All %d services running", total)
		case running == 0:
			cn.State, cn.Detail = Down, fmt.Sprintf("None of %d services running", total)
		default:
			cn.State, cn.Detail = Degraded, fmt.Sprintf("%d of %d services running", running, total)
		}
		cn.Desc = fmt.Sprintf("The %s network: the services a %s talks to when it goes online with OpenPak.", c.Label, c.Label)
		if gc := gatusConsole(in.Status, c.ID); gc.total > 0 {
			cn.Detail += fmt.Sprintf("; %d of %d availability checks passing", gc.total-gc.failing, gc.total)
			if gc.failing > 0 && cn.State == Up {
				cn.State = Degraded
			}
		}
		nodes = append(nodes, cn)
	}

	g.Nodes = nodes
	for _, e := range edges {
		g.Edges = append(g.Edges, *e)
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		return a.From+"\x00"+a.To+"\x00"+a.Kind < b.From+"\x00"+b.To+"\x00"+b.Kind
	})
	// Lines to a node the graph does not have (a console that is not deployed
	// anywhere) are dropped, not drawn into nothing.
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	kept := g.Edges[:0]
	for _, e := range g.Edges {
		if ids[e.From] && ids[e.To] {
			kept = append(kept, e)
		}
	}
	g.Edges = kept
	for _, n := range nodes {
		if n.Kind != "console" && n.Kind != "host" && len(n.Networks) == 0 && n.Kind != "core" && n.Kind != "site" && n.Kind != "infra" {
			g.Notes = append(g.Notes, fmt.Sprintf("%s has no console network: it is in the Unassigned lane. Give its quadlet Label=org.openpak.network=… or add it to opsd's network map.", n.ID))
		}
	}
	return g
}

var storeIDs = set("postgres", "redis", "mongo", "minio")

func appendOnce(s []string, v string) []string {
	if contains(s, v) {
		return s
	}
	return append(s, v)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func resolve(ref RawRef, host string, byName, byIP map[string]string, byPort map[string]map[int][]string, hostIP map[string]string) string {
	h := strings.ToLower(ref.Host)
	if id, ok := byName[h]; ok {
		return id
	}
	if id, ok := byIP[h]; ok {
		return id
	}
	target := ""
	switch {
	case isLocalHost(h):
		target = host
	case hostIP[h] != "":
		target = hostIP[h]
	case h == "openpak.org" || h == "www.openpak.org":
		if _, ok := byName["website"]; ok {
			return "website"
		}
	}
	if target == "" || ref.Port == 0 {
		return ""
	}
	if ids := byPort[target][ref.Port]; len(ids) == 1 {
		return ids[0]
	}
	return ""
}

func describe(d string) string {
	d = strings.TrimSpace(strings.TrimPrefix(d, "OpenPak "))
	if d == "" {
		return ""
	}
	return strings.ToUpper(d[:1]) + d[1:]
}

func repoOf(r RawNode) string {
	if strings.HasPrefix(r.Source, "https://github.com/openpak/") {
		return strings.TrimSuffix(r.Source, ".git")
	}
	if m := ghcrImage.FindStringSubmatch(r.Image); m != nil {
		return "https://github.com/openpak/" + m[1]
	}
	return ""
}

func stateOf(r RawNode) (string, string) {
	idle := idleHint.MatchString(r.Description)
	switch r.State {
	case "running":
		switch r.Health {
		case "unhealthy":
			return Degraded, "Running, but its health check is failing"
		case "starting":
			return Up, "Running, health check starting"
		}
		return Up, "Running"
	case "exited", "stopped", "created", "missing", "dead", "":
		if idle {
			return Idle, "Runs on demand; not running now"
		}
		if r.State == "missing" {
			return Down, "The unit exists but has no container"
		}
		return Down, "Not running (" + r.State + ")"
	}
	return Degraded, "Container is " + r.State
}

var rank = map[string]int{Up: 0, Idle: 0, Planned: 1, Unknown: 1, Degraded: 2, Down: 3}

func worse(a, b string) bool { return rank[a] > rank[b] }

func hostLabel(h HostRow) string {
	name := "Secondary server"
	if h.Name == "prod" {
		name = "Primary server"
	}
	if h.IP != "" {
		return name + " " + h.IP
	}
	return name
}

func stageWord(s string) string {
	switch s {
	case "live", "beta", "planned":
		return s
	case "alpha", "in-development":
		return "building"
	}
	return "planned"
}

type gatusCount struct{ total, failing int }

func gatusConsole(sp *StatusPage, console string) gatusCount {
	var c gatusCount
	if sp == nil {
		return c
	}
	for _, grp := range sp.Groups {
		if gatusGroupConsole(grp.Title) != console {
			continue
		}
		for _, s := range grp.Services {
			c.total++
			if !s.Up {
				c.failing++
			}
		}
	}
	return c
}

// applyStatus lays the external availability checks over the container state:
// a container that is running while its public check fails is degraded, and
// the node says which check.
func applyStatus(nodes []Node, sp *StatusPage) {
	if sp == nil {
		return
	}
	check := map[string]bool{}
	for _, grp := range sp.Groups {
		for _, s := range grp.Services {
			check[s.Name] = s.Up
		}
	}
	for i := range nodes {
		names := gatusFor[nodes[i].ID]
		var failing, seen []string
		for _, nm := range names {
			up, ok := check[nm]
			if !ok {
				continue
			}
			seen = append(seen, nm)
			if !up {
				failing = append(failing, nm)
			}
		}
		if len(seen) == 0 {
			continue
		}
		if len(failing) > 0 {
			nodes[i].Detail += "; availability check failing: " + strings.Join(failing, ", ")
			if nodes[i].State == Up || nodes[i].State == Unknown {
				nodes[i].State = Degraded
			}
		} else {
			nodes[i].Detail += "; availability checks passing (" + strings.Join(seen, ", ") + ")"
			if nodes[i].State == Unknown {
				nodes[i].State = Up
			}
		}
	}
}
