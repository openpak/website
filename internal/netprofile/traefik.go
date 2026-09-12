package netprofile

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Routes is what this server answers on, extracted from Traefik's dynamic
// configuration — the authoritative list, and the first source of the
// profile. Names are the explicit Host/HostSNI values; patterns are the
// HostRegexp/HostSNIRegexp rules, which never enter a profile but must still
// be claimed by the map, since they name real hosts a console can reach.
type Routes struct {
	Names    []string
	Patterns []string
}

// Catch-all detection: HostSNI(`*`) routes everything the console resolves to
// the adapter, and claims nothing in particular. The catch-all is how an
// unhandled name still gets answered (and logged) by the adapter, so a name
// that relies on it is only ever routed *because* the map listed it first.
var catchAll = regexp.MustCompile("(HostSNI|HostSNIRegexp)\\(`\\*`\\)")

// ruleToken finds every Host-shaped predicate in a router rule. The
// alternation lists the longer names first: Go regexp alternation is
// leftmost-first, and `Host` would otherwise swallow `HostSNI`'s prefix and
// leave `SNI(...)` behind.
var ruleToken = regexp.MustCompile("(HostSNIRegexp|HostSNI|HostRegexp|Host)\\(`([^`]*)`\\)")

type dynamicFile struct {
	TCP struct {
		Routers map[string]router `yaml:"routers"`
	} `yaml:"tcp"`
	HTTP struct {
		Routers map[string]router `yaml:"routers"`
	} `yaml:"http"`
}

type router struct {
	Rule string `yaml:"rule"`
}

// LoadRoutes reads a Traefik dynamic configuration from disk.
func LoadRoutes(file string) (Routes, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Routes{}, err
	}
	return ParseRoutes(data)
}

// ParseRoutes extracts the routed names and patterns from a dynamic
// configuration. Both the tcp (SNI passthrough) and http (TLS-terminated)
// tables count: a console reaches both on its way in.
func ParseRoutes(data []byte) (Routes, error) {
	var doc dynamicFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Routes{}, fmt.Errorf("traefik dynamic config: %w", err)
	}
	names := map[string]bool{}
	patterns := map[string]bool{}
	for _, table := range []map[string]router{doc.TCP.Routers, doc.HTTP.Routers} {
		for name, r := range table {
			if catchAll.MatchString(r.Rule) {
				// A catch-all routes everything and claims nothing; skip the
				// catch-all token but keep any explicit names sharing the
				// rule with it.
				rule := catchAll.ReplaceAllString(r.Rule, "")
				collect(rule, names, patterns)
				continue
			}
			if !collect(r.Rule, names, patterns) {
				// A router with no Host-shaped predicate (PathPrefix catch-alls
				// and the like) names nothing and claims nothing.
				_ = name
			}
		}
	}
	out := Routes{Names: mapKeys(names), Patterns: mapKeys(patterns)}
	sort.Strings(out.Names)
	sort.Strings(out.Patterns)
	return out, nil
}

func collect(rule string, names, patterns map[string]bool) bool {
	found := false
	for _, m := range ruleToken.FindAllStringSubmatch(rule, -1) {
		kind, value := m[1], m[2]
		found = true
		if value == "*" {
			continue
		}
		if strings.Contains(kind, "Regexp") {
			patterns[value] = true
		} else {
			names[strings.ToLower(strings.TrimSuffix(value, "."))] = true
		}
	}
	return found
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
