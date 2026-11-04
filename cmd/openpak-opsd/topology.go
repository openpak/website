package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The network map needs to know which service talks to which. That fact lives
// in the quadlets: the Requires/Wants lines, the published ports, and the
// Environment values that point at another service (REWARDS_EVENTS_URL,
// NEX_ACCOUNT_GRPC_ADDR, a database URL). /v1/topology reads them on the host
// and answers with the part that is safe to show an admin:
//
//   - never a value: for every reference only the scheme, host and port the
//     variable points at, plus the variable's name;
//   - never a secret: a variable whose name says KEY, TOKEN, SECRET or PASSWORD
//     is skipped unless it is plainly an address (…_URL, …_ADDR), and then the
//     user-info of the URL is dropped before anything is read from it.
//
// New services appear on the map by being deployed: nothing here is a list.

// topoRef is one "this service points at host:port" fact.
type topoRef struct {
	Var    string `json:"var"`
	Scheme string `json:"scheme,omitempty"`
	Host   string `json:"host"`
	Port   int    `json:"port,omitempty"`
	// Path is the database name of a postgres/mongodb/redis URL: a name, never a credential.
	Path string `json:"path,omitempty"`
}

// topoPort is one port a service answers on from outside its container.
type topoPort struct {
	IP    string `json:"ip,omitempty"`
	Port  int    `json:"port"`
	Proto string `json:"proto,omitempty"`
}

type topoNode struct {
	Name        string            `json:"name"` // container name
	Unit        string            `json:"unit"` // systemd unit, the key the logs use
	Description string            `json:"description"`
	Image       string            `json:"image"`
	Version     string            `json:"version,omitempty"`
	Source      string            `json:"source,omitempty"` // image.source label: the repo URL
	State       string            `json:"state"`
	Health      string            `json:"health"`
	StartedAt   *string           `json:"started_at"`
	Networks    []string          `json:"networks"`
	Network     string            `json:"container_network,omitempty"` // podman network, "host" for host networking
	IP          string            `json:"ip,omitempty"`
	Ports       []topoPort        `json:"ports"`
	Needs       []string          `json:"needs"` // units from Requires/Wants/BindsTo
	Refs        []topoRef         `json:"refs"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type quadlet struct {
	unit        string
	description string
	container   string
	image       string
	network     string
	ip          string
	ports       []topoPort
	needs       []string
	refs        []topoRef
}

var (
	secretName = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)`)
	addrName   = regexp.MustCompile(`(?i)(_URL|_URI|_ADDR|_ADDRESS|_HOST|_ENDPOINT|_DSN)$`)
	hostPort   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*):(\d{2,5})$`)
	portsNote  = regexp.MustCompile(`^#\s*openpak-ports=(.*)$`)
)

// parseQuadlet reads one .container file. env resolves EnvironmentFile paths.
func parseQuadlet(unit string, raw []byte, readEnv func(path string) []byte) quadlet {
	q := quadlet{unit: unit}
	section := ""
	var envs []string
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := portsNote.FindStringSubmatch(line); m != nil {
			for _, f := range strings.Fields(m[1]) {
				if p, err := strconv.Atoi(f); err == nil && p > 0 && p < 65536 {
					q.ports = append(q.ports, topoPort{Port: p})
				}
			}
			continue
		}
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch section + "/" + k {
		case "Unit/Description":
			q.description = v
		case "Unit/Requires", "Unit/Wants", "Unit/BindsTo":
			for _, u := range strings.Fields(v) {
				if strings.HasSuffix(u, ".service") {
					q.needs = append(q.needs, u)
				}
			}
		case "Container/ContainerName":
			q.container = v
		case "Container/Image":
			q.image = v
		case "Container/Network":
			q.network = v
		case "Container/IP":
			q.ip = v
		case "Container/PublishPort":
			if p, ok := parsePublish(v); ok {
				q.ports = append(q.ports, p)
			}
		case "Container/Environment":
			envs = append(envs, splitEnv(v)...)
		case "Container/EnvironmentFile":
			path := strings.TrimPrefix(v, "-")
			if b := readEnv(path); b != nil {
				for _, l := range strings.Split(string(b), "\n") {
					l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
					if l != "" && l[0] != '#' {
						envs = append(envs, l)
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range envs {
		name, val, _ := strings.Cut(e, "=")
		for _, r := range refsOf(name, val) {
			key := fmt.Sprint(r)
			if !seen[key] {
				seen[key] = true
				q.refs = append(q.refs, r)
			}
		}
	}
	return q
}

// splitEnv splits "A=1 B=2" and honours double quotes around a value.
func splitEnv(v string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range v {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t') && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// parsePublish reads [ip:]hostport:ctrport[/proto] or hostport[/proto].
func parsePublish(v string) (topoPort, bool) {
	proto := ""
	if i := strings.LastIndex(v, "/"); i >= 0 {
		v, proto = v[:i], v[i+1:]
	}
	parts := strings.Split(v, ":")
	var ip, host string
	switch len(parts) {
	case 1:
		host = parts[0]
	case 2:
		host = parts[0]
	case 3:
		ip, host = parts[0], parts[1]
	default:
		return topoPort{}, false
	}
	p, err := strconv.Atoi(host)
	if err != nil || p <= 0 || p > 65535 {
		return topoPort{}, false
	}
	return topoPort{IP: ip, Port: p, Proto: proto}, true
}

// refsOf turns one variable into zero or more references. Anything that is not
// an address of another service gives nothing, and a secret-named variable
// gives nothing unless it is plainly an address.
func refsOf(name, val string) []topoRef {
	if val == "" {
		return nil
	}
	if secretName.MatchString(name) && !addrName.MatchString(name) {
		return nil
	}
	var out []topoRef
	for _, one := range strings.Split(val, ",") {
		one = strings.TrimSpace(one)
		if r, ok := refOf(name, one); ok {
			out = append(out, r)
		}
	}
	return out
}

func refOf(name, val string) (topoRef, bool) {
	if strings.Contains(val, "://") {
		u, err := url.Parse(val)
		if err != nil || u.Hostname() == "" {
			return topoRef{}, false
		}
		r := topoRef{Var: name, Scheme: u.Scheme, Host: strings.ToLower(u.Hostname())}
		if p, err := strconv.Atoi(u.Port()); err == nil {
			r.Port = p
		}
		switch u.Scheme {
		case "postgres", "postgresql", "mongodb", "redis":
			r.Path = strings.Trim(strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)[0], " ")
		}
		return r, !listenable(r.Host)
	}
	if m := hostPort.FindStringSubmatch(val); m != nil {
		p, _ := strconv.Atoi(m[2])
		r := topoRef{Var: name, Host: strings.ToLower(m[1]), Port: p}
		return r, !listenable(r.Host)
	}
	return topoRef{}, false
}

// listenable hosts are bind addresses, not references.
func listenable(h string) bool { return h == "0.0.0.0" || h == "::" || h == "" }

// sharedEnvUsers is how many quadlets may include one env file before it counts as shared.
const sharedEnvUsers = 8

// --- reading the host ---

func (a *agent) quadletDir() string {
	if d := os.Getenv("OPSD_QUADLET_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "containers", "systemd")
}

func (a *agent) readEnvFile(path string) []byte {
	home, _ := os.UserHomeDir()
	path = strings.NewReplacer("%h", home, "%t", fmt.Sprintf("/run/user/%d", os.Getuid())).Replace(path)
	if !filepath.IsAbs(path) {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func (a *agent) topology(ctx context.Context) ([]topoNode, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := a.run(ctx, "podman", "ps", "-a", "--format", "json")
	if err != nil {
		return nil, err
	}
	var entries []psEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("podman ps: %w", err)
	}
	byUnit := map[string]psEntry{}
	for _, e := range entries {
		if u := e.Labels["PODMAN_SYSTEMD_UNIT"]; u != "" && len(e.Names) > 0 {
			byUnit[u] = e
		}
	}
	files, err := os.ReadDir(a.quadletDir())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	pending := a.pending
	a.mu.Unlock()

	// An env file that most quadlets include (the shared .env) holds every
	// service's settings; reading its addresses as "this service calls that"
	// would draw a line from everything to everything. Per-service wiring lives
	// in the quadlet's own Environment= lines and its own env files.
	use := map[string]int{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".container") {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(a.quadletDir(), f.Name())); err == nil {
			for _, l := range strings.Split(string(raw), "\n") {
				if p, ok := strings.CutPrefix(strings.TrimSpace(l), "EnvironmentFile="); ok {
					use[strings.TrimPrefix(p, "-")]++
				}
			}
		}
	}
	readEnv := func(p string) []byte {
		if use[p] > sharedEnvUsers {
			return nil
		}
		return a.readEnvFile(p)
	}

	var nodes []topoNode
	for _, f := range files {
		// Only live quadlets: the dir also holds "x.container.before-…" backups.
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".container") {
			continue
		}
		base := strings.TrimSuffix(f.Name(), ".container")
		if !strings.HasPrefix(base, a.prefix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.quadletDir(), f.Name()))
		if err != nil {
			continue
		}
		unit := base + ".service"
		q := parseQuadlet(unit, raw, readEnv)
		n := topoNode{Name: q.container, Unit: unit, Description: q.description, Image: q.image,
			Network: q.network, IP: q.ip, Ports: q.ports, Needs: q.needs, Refs: q.refs,
			State: "missing", Health: "none"}
		if n.Name == "" {
			n.Name = base
		}
		if e, ok := byUnit[unit]; ok {
			n.Name, n.State, n.Health = e.Names[0], e.State, health(e.Status)
			n.Image = e.Image
			n.Version = e.Labels["org.opencontainers.image.version"]
			n.Source = e.Labels["org.opencontainers.image.source"]
			if e.StartedAt > 0 {
				t := time.Unix(e.StartedAt, 0).UTC().Format(time.RFC3339)
				n.StartedAt = &t
			}
			n.Networks = a.networks(n.Name, e.Labels["org.openpak.network"])
			if pending[n.Name] {
				n.Labels = map[string]string{"update": "available"}
			}
		} else {
			n.Networks = a.networks(n.Name, "")
		}
		if n.Version == "" {
			n.Version = imageTag(n.Image)
		}
		if n.Ports == nil {
			n.Ports = []topoPort{}
		}
		if n.Needs == nil {
			n.Needs = []string{}
		}
		if n.Refs == nil {
			n.Refs = []topoRef{}
		}
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Unit < nodes[j].Unit })
	return nodes, nil
}

// imageTag is the tag of "registry/name:tag", or "" for a digest reference.
func imageTag(img string) string {
	if strings.Contains(img, "@") {
		return ""
	}
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		return img[i+1:]
	}
	return ""
}

func (a *agent) handleTopology(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.topology(r.Context())
	if err != nil {
		a.log.Error("topology", "err", err)
		reply(w, http.StatusBadGateway, false, err.Error())
		return
	}
	if nodes == nil {
		nodes = []topoNode{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": a.host, "services": nodes})
}
