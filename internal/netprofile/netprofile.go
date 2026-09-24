// Package netprofile generates the OpenPak network profile: the one answer to
// "what is OpenPak, and what should I send you?" that a client asks at launch
// (PRD network-profile-service-prd).
//
// The profile is generated, never authored. Three sources feed it:
//
//  1. Traefik's dynamic configuration — the authoritative list of names this
//     server answers on. Mounted read-only; re-read on every cache expiry.
//  2. The signed redirect ceiling (docs/signed-ceiling.md) — per platform,
//     the families a client may redirect at all. Signed off the box, served
//     verbatim, and enforced on the map below: a family outside it is dropped
//     from the profile, individually and logged. Without a verified ceiling
//     file the compiled families.Universe stands in.
//  3. platforms.yml — the per-platform map. The only authored input, small
//     and reviewed like code.
//
// The server may narrow, never widen: the profile only ever names domains
// inside the families a client already compiles in. Anything routed but
// claimed by no platform is omitted and logged loudly — an unrouteable name
// is a bug worth seeing, not worth shipping.
package netprofile

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed platforms.yml
var embeddedMap []byte

// PlatformAll is the empty-platform key: every platform's families united.
// It is what a diagnostic or the website itself wants and no emulator does.
const PlatformAll = ""

// PlatformsAllowed is the platform set the PRD names. The map may not invent
// another one; a new console family is a change here first. It is exported
// because it is the platform vocabulary, not a host list: the v2 policy
// package (PRD emulators/prds/universal-tls-forwarding/01) validates against the same set
// instead of carrying a second copy.
var PlatformsAllowed = []string{"switch", "wiiu", "3ds", "wii", "ds"}

// ServerInfo is where a redirected name lands.
type ServerInfo struct {
	Address   string `json:"address" yaml:"address"`
	HTTPSPort int    `json:"https_port" yaml:"https_port"`
	HTTPPort  int    `json:"http_port" yaml:"http_port"`
}

// CAInfo describes the console CA. The digest is the trust anchor's pin: a
// profile cannot swap the CA without the hash changing.
type CAInfo struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	NotAfter int64  `json:"not_after"`
}

// Redirect is what a client sends here. Suffixes are domain families; exact
// names catch hosts outside every listed family; never must reach the real
// internet — a redirect that is too broad is its own outage.
type Redirect struct {
	Suffixes []string `json:"suffixes"`
	Exact    []string `json:"exact"`
	Never    []string `json:"never"`
	// Overrides are names with an address of their own, applied before the
	// suffixes collapse them onto the server. The NAT check is why this
	// exists: it compares what two addresses observe of one console, so its
	// second probe must not land on the first.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// ServiceEntry is a full URL for clients that address a URL rather than
// resolve a name (Cemu today). Clients that redirect DNS ignore it.
type ServiceEntry struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Profile is the document a client fetches, validates and applies.
type Profile struct {
	Version      int            `json:"version"`
	GeneratedAt  int64          `json:"generated_at"`
	Platform     string         `json:"platform"`
	Server       ServerInfo     `json:"server"`
	CA           *CAInfo        `json:"ca,omitempty"`
	Redirect     Redirect       `json:"redirect"`
	Services     []ServiceEntry `json:"services"`
	RecheckAfter int            `json:"recheck_after"`
}

// PlatformMap is the authored half (platforms.yml). Everything else about a
// profile is derived from the routing that is actually live.
type PlatformMap struct {
	PublicBaseURL string              `yaml:"public_base_url"`
	Server        ServerInfo          `yaml:"server"`
	RecheckAfter  int                 `yaml:"recheck_after"`
	Platforms     map[string]Platform `yaml:"platforms"`
}

// Platform is one console family's slice of the map.
type Platform struct {
	Families  []string          `yaml:"families"`
	Exact     []string          `yaml:"exact"`
	Never     []string          `yaml:"never"`
	Overrides map[string]string `yaml:"overrides"`
	Services  map[string]string `yaml:"services"`
}

// ErrUnknownPlatform means the query asked for a platform this map does not
// carry. The profile for a platform nobody mapped is not an empty document;
// it is a 400.
var ErrUnknownPlatform = errors.New("netprofile: unknown platform")

// Options configures the service. TraefikFile is required: without the live
// routing there is no profile to generate and the endpoints answer 503.
type Options struct {
	TraefikFile string                                // live Traefik dynamic config, read-only
	PlatformMap string                                // optional file overriding the embedded platforms.yml
	CA          func(context.Context) ([]byte, error) // console CA source; nil omits the ca block
	Log         *slog.Logger
	CacheTTL    time.Duration // profile rebuild cadence; default 30s
	CACacheTTL  time.Duration // CA refetch cadence; default 1h
	// Ceiling is the signed ceiling file; nil (or no verified file) means the
	// compiled Universe bounds every platform.
	Ceiling *CeilingStore
}

// Service generates profiles on demand and keeps the last good set in memory.
type Service struct {
	opts Options
	m    *PlatformMap

	mu    sync.Mutex
	built map[string]built
	ca    *caCache
}

type built struct {
	body      []byte // the exact bytes the ETag is taken over
	etag      string
	canonical []byte // body with version and generated_at zeroed: the content identity
	version   int
	builtAt   time.Time
}

type caCache struct {
	body    []byte
	info    *CAInfo
	fetched time.Time
}

// New loads the platform map and validates it. A bad map is a startup error:
// the profile is infrastructure, and a map that would widen a client's
// redirect must never come up silently.
func New(opts Options) (*Service, error) {
	data := embeddedMap
	if opts.PlatformMap != "" {
		b, err := os.ReadFile(opts.PlatformMap)
		if err != nil {
			return nil, fmt.Errorf("netprofile: platform map: %w", err)
		}
		data = b
	}
	m, err := parseMap(data)
	if err != nil {
		return nil, err
	}
	if opts.TraefikFile == "" {
		return nil, errors.New("netprofile: TraefikFile is required")
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = 30 * time.Second
	}
	if opts.CACacheTTL <= 0 {
		opts.CACacheTTL = time.Hour
	}
	// A family outside the ceiling is not a startup error: the ceiling can
	// change under a running server (scp), and one family out of bounds must
	// not take every other redirect down with it. Say so now; generation
	// drops it every time.
	for _, d := range m.outside(opts.Ceiling.Effective()) {
		opts.Log.Warn("netprofile: platform map entry outside the redirect ceiling; it will be dropped from profiles", "entry", d)
	}
	return &Service{opts: opts, m: m, built: map[string]built{}}, nil
}

// LoadMap parses and validates a platform map strictly against the compiled
// fallback ceiling (Universe): the check the v2 policy tooling and the tests
// use. LoadMapWithin is the same against a signed ceiling.
func LoadMap(data []byte) (*PlatformMap, error) {
	return LoadMapWithin(data, FallbackCeiling())
}

// LoadMapWithin parses a platform map and fails if any family lies outside
// that platform's ceiling (the server may narrow, never widen).
func LoadMapWithin(data []byte, c *Ceiling) (*PlatformMap, error) {
	m, err := parseMap(data)
	if err != nil {
		return nil, err
	}
	if out := m.outside(c); len(out) > 0 {
		return nil, fmt.Errorf("netprofile: outside the redirect ceiling; the server may narrow, never widen: %s", strings.Join(out, ", "))
	}
	return m, nil
}

// outside lists every family, exact name and override of the map that lies
// outside its platform's ceiling, as "platform family" strings.
func (m *PlatformMap) outside(c *Ceiling) []string {
	var out []string
	for _, name := range sortedKeys(m.Platforms) {
		p := m.Platforms[name]
		for _, f := range p.Families {
			if !c.Covers(name, f) {
				out = append(out, name+" family "+f)
			}
		}
		for _, e := range p.Exact {
			if !c.Covers(name, e) {
				out = append(out, name+" exact "+e)
			}
		}
		for _, h := range sortedKeys(p.Overrides) {
			if !c.Covers(name, h) {
				out = append(out, name+" override "+h)
			}
		}
	}
	return out
}

// parseMap parses and validates a platform map without a ceiling. The checks
// are the PRD's security rules turned into load errors:
//
//   - every family is well formed (docs/signed-ceiling.md's family rule)
//   - every platform is one the PRD names
//   - server.address is a literal IP, since a redirected name resolves to it
//   - every service URL's host is inside its platform's families
func parseMap(data []byte) (*PlatformMap, error) {
	var m PlatformMap
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("netprofile: platform map: %w", err)
	}
	if u, err := url.Parse(m.PublicBaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("netprofile: public_base_url must be an https URL, got %q", m.PublicBaseURL)
	}
	if net.ParseIP(m.Server.Address) == nil {
		return nil, fmt.Errorf("netprofile: server.address must be a literal IP, got %q", m.Server.Address)
	}
	if m.RecheckAfter <= 0 {
		return nil, errors.New("netprofile: recheck_after must be positive")
	}
	if len(m.Platforms) == 0 {
		return nil, errors.New("netprofile: platform map carries no platforms")
	}
	names := make([]string, 0, len(m.Platforms))
	for name := range m.Platforms {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := m.Platforms[name]
		if !slices.Contains(PlatformsAllowed, name) {
			return nil, fmt.Errorf("netprofile: platform %q is not one the PRD names (%s)",
				name, strings.Join(PlatformsAllowed, ", "))
		}
		if len(p.Families) == 0 {
			return nil, fmt.Errorf("netprofile: platform %q lists no families", name)
		}
		for _, f := range p.Families {
			if err := ValidFamily(f); err != nil {
				return nil, fmt.Errorf("netprofile: platform %q: %w", name, err)
			}
		}
		for _, e := range append(slices.Clone(p.Exact), p.Never...) {
			if e == "" || strings.ContainsAny(e, "*?") || !strings.Contains(e, ".") {
				return nil, fmt.Errorf("netprofile: platform %q entry %q is not a plain hostname", name, e)
			}
		}
		// An override redirects a name somewhere other than the server, so it
		// must still be a name this platform may redirect at all, and the
		// where must be an address rather than another name to resolve.
		for _, host := range sortedKeys(p.Overrides) {
			if host == "" || strings.ContainsAny(host, "*?") || !strings.Contains(host, ".") {
				return nil, fmt.Errorf("netprofile: platform %q override %q is not a plain hostname", name, host)
			}
			if !coveredByAny(host, p.Families) && !slices.Contains(p.Exact, host) {
				return nil, fmt.Errorf("netprofile: platform %q override %q is outside the names it may redirect", name, host)
			}
			if net.ParseIP(p.Overrides[host]) == nil {
				return nil, fmt.Errorf("netprofile: platform %q override %q address %q is not an IP", name, host, p.Overrides[host])
			}
		}
		ids := make([]string, 0, len(p.Services))
		for id := range p.Services {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			u, err := url.Parse(p.Services[id])
			if err != nil || u.Host == "" {
				return nil, fmt.Errorf("netprofile: platform %q service %q is not a URL", name, id)
			}
			if !coveredByAny(u.Hostname(), p.Families) {
				return nil, fmt.Errorf("netprofile: platform %q service %s points at %q, outside its own families", name, id, u.Hostname())
			}
		}
	}
	return &m, nil
}

// Drift returns every routed name and pattern that no platform claims. An
// empty result means the live routing is fully covered by the map: a client
// of every console family is told about everything it could reach. Both the
// CI drift test and generation itself go through this one rule.
func Drift(m *PlatformMap, routes Routes) []string {
	var bad []string
	for _, name := range routes.Names {
		if !claimedByAny(m, name) {
			bad = append(bad, name)
		}
	}
	for _, pat := range routes.Patterns {
		if !patternClaimedByAny(m, pat) {
			bad = append(bad, "regexp "+pat)
		}
	}
	return bad
}

func claimedByAny(m *PlatformMap, name string) bool {
	for _, p := range m.Platforms {
		if coveredByAny(name, p.Families) || slices.Contains(p.Exact, name) {
			return true
		}
	}
	return false
}

// patternClaimedByAny approximates whether a HostRegexp-shaped rule names
// something inside a mapped family: the regex metacharacters are stripped and
// the family is looked for in what remains. Good enough to notice a rule that
// points somewhere no map entry covers; generation omits regardless, and the
// omission is logged.
func patternClaimedByAny(m *PlatformMap, pattern string) bool {
	var residue strings.Builder
	for _, r := range pattern {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			residue.WriteRune(r)
		}
	}
	res := residue.String()
	for _, p := range m.Platforms {
		for _, f := range p.Families {
			if strings.Contains(res, strings.TrimPrefix(f, ".")) {
				return true
			}
		}
	}
	return false
}

// coveredByAny reports whether name sits inside one of the families (a family
// is a leading-dot suffix; a bare family name is itself covered).
func coveredByAny(name string, families []string) bool {
	for _, f := range families {
		if strings.HasSuffix(name, f) || name == strings.TrimPrefix(f, ".") {
			return true
		}
	}
	return false
}

// Profile generates (or fetches from the short cache) the profile for one
// platform key, or PlatformAll for every platform united. The second return
// is the ETag the handler answers If-None-Match with.
func (s *Service) Profile(ctx context.Context, platform string) (*Profile, string, error) {
	if platform != PlatformAll {
		if _, ok := s.m.Platforms[platform]; !ok {
			return nil, "", ErrUnknownPlatform
		}
	}
	b, err := s.build(ctx, platform)
	if err != nil {
		return nil, "", err
	}
	var p Profile
	if err := json.Unmarshal(b.body, &p); err != nil {
		return nil, "", err
	}
	return &p, b.etag, nil
}

// Bytes is Profile without the decode: the exact body and ETag to serve.
func (s *Service) Bytes(ctx context.Context, platform string) ([]byte, string, error) {
	if platform != PlatformAll {
		if _, ok := s.m.Platforms[platform]; !ok {
			return nil, "", ErrUnknownPlatform
		}
	}
	b, err := s.build(ctx, platform)
	if err != nil {
		return nil, "", err
	}
	return b.body, b.etag, nil
}

// build generates on cache expiry, re-reading the live Traefik file every
// time — the same file the file provider watches, so a routing change is
// picked up without a restart.
func (s *Service) build(ctx context.Context, platform string) (built, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.built[platform]; ok && time.Since(prev.builtAt) < s.opts.CacheTTL {
		return prev, nil
	}

	data, err := os.ReadFile(s.opts.TraefikFile)
	if err != nil {
		// Stale beats broken: a routing file that cannot be read leaves the
		// last good profile serving rather than a 5xx.
		if prev, ok := s.built[platform]; ok {
			s.opts.Log.Error("netprofile: traefik config unreadable; serving the last good profile", "err", err)
			return prev, nil
		}
		return built{}, fmt.Errorf("netprofile: read traefik config: %w", err)
	}
	routes, err := ParseRoutes(data)
	if err != nil {
		if prev, ok := s.built[platform]; ok {
			s.opts.Log.Error("netprofile: traefik config unparsable; serving the last good profile", "err", err)
			return prev, nil
		}
		return built{}, fmt.Errorf("netprofile: parse traefik config: %w", err)
	}
	for _, name := range Drift(s.m, routes) {
		s.opts.Log.Error("netprofile: routed name no platform claims; omitting it from every profile", "name", name)
	}

	prof, dropped := s.m.generate(platform, routes, s.opts.Ceiling.Effective())
	for _, d := range dropped {
		s.opts.Log.Warn("netprofile: outside the redirect ceiling; dropped from the profile", "platform", prof.Platform, "entry", d)
	}
	prof.RecheckAfter = s.m.RecheckAfter
	// Best-effort and cached: a CA that cannot be fetched right now leaves
	// the block out, and the client keeps the CA it already pinned.
	if s.opts.CA != nil {
		if _, info, err := s.fetchCA(ctx); err == nil && info != nil {
			prof.CA = info
		} else if err != nil {
			s.opts.Log.Warn("netprofile: CA unavailable; profile carries no ca block", "err", err)
		}
	}

	// Version and generated_at ride alongside the content. The content
	// identity is the profile with both zeroed: generated_at changes every
	// second and must not bump a version that has not changed. Within a
	// process the version is monotonic; across restarts it resets, which is
	// why it is for humans and logs and the ETag is the machine's answer.
	prev := s.built[platform]
	zeroed := prof
	zeroed.Version, zeroed.GeneratedAt = 0, 0
	canonical, err := json.Marshal(&zeroed)
	if err != nil {
		return built{}, err
	}
	if prev.canonical != nil && bytesEqual(prev.canonical, canonical) {
		prev.builtAt = time.Now()
		s.built[platform] = prev
		return prev, nil
	}
	prof.Version = prev.version + 1
	prof.GeneratedAt = time.Now().Unix()
	body, err := json.Marshal(&prof)
	if err != nil {
		return built{}, err
	}
	sum := sha256.Sum256(body)
	b := built{
		body:      body,
		etag:      `"` + hex.EncodeToString(sum[:]) + `"`,
		canonical: canonical,
		version:   prof.Version,
		builtAt:   time.Now(),
	}
	s.built[platform] = b
	return b, nil
}

// expire forces the next build call to regenerate, instead of waiting out
// the cache TTL. Tests only.
func (s *Service) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, b := range s.built {
		b.builtAt = time.Time{}
		s.built[key] = b
	}
}

// expireCA drops the cached CA, so the next fetch sees a new one. Tests only.
func (s *Service) expireCA() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ca = nil
}

// CA returns the console CA the profile's ca block points at: the PEM, its
// digest and expiry. Fetched through the configured source and cached, since
// the CA changes roughly never.
func (s *Service) CA(ctx context.Context) ([]byte, *CAInfo, error) {
	if s.opts.CA == nil {
		return nil, nil, errors.New("netprofile: no CA source configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchCA(ctx)
}

// fetchCA is CA without the locking; build runs under the same mutex.
func (s *Service) fetchCA(ctx context.Context) ([]byte, *CAInfo, error) {
	if s.ca != nil && time.Since(s.ca.fetched) < s.opts.CACacheTTL {
		return s.ca.body, s.ca.info, nil
	}
	pemBytes, err := s.opts.CA(ctx)
	if err != nil {
		// Stale beats broken here too: the last good CA keeps serving.
		if s.ca != nil {
			s.opts.Log.Error("netprofile: CA fetch failed; serving the last good CA", "err", err)
			return s.ca.body, s.ca.info, nil
		}
		return nil, nil, err
	}
	info, err := caInfo(s.m.PublicBaseURL, pemBytes)
	if err != nil {
		if s.ca != nil {
			s.opts.Log.Error("netprofile: CA unparsable; serving the last good CA", "err", err)
			return s.ca.body, s.ca.info, nil
		}
		return nil, nil, err
	}
	s.ca = &caCache{body: pemBytes, info: info, fetched: time.Now()}
	return pemBytes, info, nil
}

func caInfo(base string, pemBytes []byte) (*CAInfo, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("netprofile: CA is not a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("netprofile: CA certificate: %w", err)
	}
	sum := sha256.Sum256(pemBytes)
	return &CAInfo{
		URL:      strings.TrimSuffix(base, "/") + "/api/v1/network/ca.pem",
		SHA256:   hex.EncodeToString(sum[:]),
		NotAfter: cert.NotAfter.Unix(),
	}, nil
}

// generate builds the document for one platform from the live routes, the
// map and the ceiling. Anything outside the platform's ceiling is dropped on
// its own and reported in the second return — never the whole platform (the
// failure a single new family once caused in a client). The rules, in order:
//
//   - suffixes: the platform's families that lie inside its ceiling.
//   - exact: names this platform claims that no family of its covers — the
//     authored entries plus routed names that would otherwise be lost to a
//     narrowed suffix list. A name a family covers is never repeated here.
//   - never: the platform's list, verbatim.
//   - services: the platform's URL table, for clients that address URLs.
//
// exact names, overrides and service hosts obey the ceiling too, and an
// override or service whose family was dropped goes with it.
func (m *PlatformMap) generate(platform string, routes Routes, c *Ceiling) (*Profile, []string) {
	var dropped []string
	prof := &Profile{
		Platform: platform,
		Server:   m.Server,
		Redirect: Redirect{Suffixes: []string{}, Exact: []string{}, Never: []string{}},
		Services: []ServiceEntry{},
	}
	if platform == PlatformAll {
		families := map[string]bool{}
		never := map[string]int{}
		for _, name := range sortedKeys(m.Platforms) {
			for _, f := range m.Platforms[name].Families {
				if c.Covers(name, f) {
					families[f] = true
				} else {
					dropped = append(dropped, name+" family "+f)
				}
			}
		}
		for _, p := range m.Platforms {
			for _, n := range p.Never {
				never[n]++
			}
		}
		for _, f := range sortedKeys(families) {
			prof.Redirect.Suffixes = append(prof.Redirect.Suffixes, f)
		}
		// A name must reach the real internet in every profile for the union
		// to say so: the union narrows, like every profile does.
		for _, n := range sortedKeys(never) {
			if never[n] == len(m.Platforms) {
				prof.Redirect.Never = append(prof.Redirect.Never, n)
			}
		}
		for _, name := range routes.Names {
			if coveredByAny(name, prof.Redirect.Suffixes) || slices.Contains(prof.Redirect.Exact, name) {
				continue
			}
			if !c.CoversAny(name) {
				dropped = append(dropped, "routed "+name)
				continue
			}
			prof.Redirect.Exact = append(prof.Redirect.Exact, name)
		}
		sort.Strings(prof.Redirect.Exact)
		prof.Platform = "all"
		return prof, dropped
	}

	p := m.Platforms[platform]
	for _, f := range p.Families {
		if c.Covers(platform, f) {
			prof.Redirect.Suffixes = append(prof.Redirect.Suffixes, f)
		} else {
			dropped = append(dropped, "family "+f)
		}
	}
	sort.Strings(prof.Redirect.Suffixes)
	prof.Redirect.Never = append(prof.Redirect.Never, p.Never...)
	sort.Strings(prof.Redirect.Never)

	exact := map[string]bool{}
	for _, e := range p.Exact {
		if c.Covers(platform, e) {
			exact[e] = true
		} else {
			dropped = append(dropped, "exact "+e)
		}
	}
	// A routed name the families already cover is never repeated as exact;
	// a routed name claimed by nobody is drift — omitted here, logged by the
	// caller. What remains is the authored list.
	prof.Redirect.Exact = sortedKeys(exact)

	for _, host := range sortedKeys(p.Overrides) {
		if !c.Covers(platform, host) || !coveredByAny(host, prof.Redirect.Suffixes) && !exact[host] {
			dropped = append(dropped, "override "+host)
			continue
		}
		if prof.Redirect.Overrides == nil {
			prof.Redirect.Overrides = map[string]string{}
		}
		prof.Redirect.Overrides[host] = p.Overrides[host]
	}

	ids := make([]string, 0, len(p.Services))
	for id := range p.Services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		// parseMap checked the host is inside the platform's own families;
		// it must also be inside one that survived the ceiling.
		if u, err := url.Parse(p.Services[id]); err != nil || !coveredByAny(u.Hostname(), prof.Redirect.Suffixes) {
			dropped = append(dropped, "service "+id)
			continue
		}
		prof.Services = append(prof.Services, ServiceEntry{ID: id, URL: p.Services[id]})
	}
	return prof, dropped
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }
