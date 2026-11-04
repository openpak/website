// Package netmap builds the picture of the whole OpenPak network: every console
// network, the services each one runs, the core services they share and the
// infrastructure under them, with the lines between them and live status on
// every node.
//
// Nothing here is a hand-kept list of services. The nodes and the lines come
// from what is actually deployed: openpak-opsd on each box reads the quadlets
// and reports each unit's Description, image, ports, Requires/Wants and every
// Environment value that points at another service. This package only
// classifies and resolves that. A service deployed tomorrow is on the map
// tomorrow; one with an unrecognised name lands in the "Unassigned" lane so
// somebody notices, instead of vanishing.
package netmap

import "time"

// Raw is what openpak-opsd's GET /v1/topology answers, per host.
type Raw struct {
	Host     string    `json:"host"`
	Services []RawNode `json:"services"`
}

type RawNode struct {
	Name        string            `json:"name"`
	Unit        string            `json:"unit"`
	Description string            `json:"description"`
	Image       string            `json:"image"`
	Version     string            `json:"version"`
	Source      string            `json:"source"`
	State       string            `json:"state"`
	Health      string            `json:"health"`
	StartedAt   *string           `json:"started_at"`
	Networks    []string          `json:"networks"`
	Network     string            `json:"container_network"`
	IP          string            `json:"ip"`
	Ports       []RawPort         `json:"ports"`
	Needs       []string          `json:"needs"`
	Refs        []RawRef          `json:"refs"`
	Labels      map[string]string `json:"labels"`
}

type RawPort struct {
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

type RawRef struct {
	Var    string `json:"var"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Path   string `json:"path"`
}

// HostTopo is one box: the agent's answer, or why there is none.
type HostTopo struct {
	Name string // the key in WEBSITE_OPSD_HOSTS (prod, draughts)
	IP   string // the box's public address, to resolve references that use it
	Raw  Raw
	Err  string
}

// StatusPage is the part of status.openpak.org's /api/status this reads.
type StatusPage struct {
	Groups []struct {
		Title    string
		Services []struct {
			Name   string
			Up     bool
			Uptime float64
		}
	}
}

// Input is everything Build needs; the Service gathers it.
type Input struct {
	Hosts  []HostTopo
	Status *StatusPage       // nil when the availability page is unreachable
	Stages map[string]string // per console name ("Wii U"): live | beta | alpha | in-development | planned
	LogsOn bool
	Now    time.Time
}

// Node states. up, degraded and down are what the checks say; idle is a unit
// that only runs on demand; planned is a console with nothing deployed yet;
// unknown is a unit nobody is checking.
const (
	Up       = "up"
	Degraded = "degraded"
	Down     = "down"
	Idle     = "idle"
	Planned  = "planned"
	Unknown  = "unknown"
)

type DB struct {
	Engine string `json:"engine"`
	Name   string `json:"name,omitempty"`
}

type Node struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // console | adapter | game | vendor | core | site | infra | host
	Networks []string `json:"networks,omitempty"`
	Group    string   `json:"group,omitempty"` // collapse key, e.g. games:switch
	Desc     string   `json:"desc,omitempty"`
	Repo     string   `json:"repo,omitempty"`
	Version  string   `json:"version,omitempty"`
	Hosts    []string `json:"hosts,omitempty"`
	State    string   `json:"state"`
	Detail   string   `json:"detail,omitempty"`
	Since    string   `json:"since,omitempty"`
	Logs     string   `json:"logs,omitempty"` // the unit name /admin/logs?service= takes
	DB       []DB     `json:"db,omitempty"`
	Stage    string   `json:"stage,omitempty"` // consoles: live | beta | building | planned
	Update   bool     `json:"update,omitempty"`
	Virtual  bool     `json:"virtual,omitempty"` // not a container: drawn from knowledge, not from a quadlet
}

type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind string   `json:"kind"`          // calls | store | needs | network
	Via  []string `json:"via,omitempty"` // the variables (names only) that make the line
}

type Network struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Family int    `json:"family"` // palette slot 1..5
	Stage  string `json:"stage"`
}

type Graph struct {
	Generated time.Time `json:"generated"`
	Networks  []Network `json:"networks"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	Hosts     []HostRow `json:"hosts"`
	Notes     []string  `json:"notes,omitempty"` // things that could not be wired, shown to the admin
}

type HostRow struct {
	Name  string `json:"name"`
	IP    string `json:"ip,omitempty"`
	Up    bool   `json:"up"`
	Error string `json:"error,omitempty"`
	Count int    `json:"count"`
}
