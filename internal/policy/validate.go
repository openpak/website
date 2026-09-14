package policy

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/openpak/website/internal/netprofile"
)

// bootstrapHost is the apex the distribution API itself is served from
// (POL-005: keep it outside the forwarding path). An exact rule for it would
// teach clients to send the registry's own traffic through the redirect.
const bootstrapHost = "openpak.org"

var (
	rePolicyID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	reChannel  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	reCeiling  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	reNameID   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	reRuleID   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	reScope    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	reIPv4     = regexp.MustCompile(`^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$`)
	// Full eight-group IPv6 only: the canonical form. Shorthand like ::1 is
	// spelled out at generation, never accepted back.
	reIPv6 = regexp.MustCompile(`^([0-9a-f]{1,4})(:[0-9a-f]{1,4}){7}$`)
)

// validHostname enforces the lexical hostname rules the schema states:
// lowercase RFC 1123 labels, at least two labels, 253 bytes at most.
func validHostname(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if n := len(l); n < 1 || n > 63 {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			alnum := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
			if !alnum && c != '-' {
				return false
			}
			if c == '-' && (i == 0 || i == len(l)-1) {
				return false
			}
		}
	}
	return true
}

// validSuffix is a leading-dot family of valid hostnames.
func validSuffix(suffix string) bool {
	if !strings.HasPrefix(suffix, ".") || len(suffix) > 254 {
		return false
	}
	return validHostname(strings.TrimPrefix(suffix, "."))
}

// validArchivePath enforces the archive rules of PRD 01: relative,
// normalized, unique (uniqueness is the caller's set), no traversal, no
// backslashes, no nested empties — the shape a safe unpacker may expect.
func validArchivePath(path string) bool {
	if path == "" || len(path) > 255 {
		return false
	}
	if path[0] == '/' || path[0] == '\\' || strings.Contains(path, "\\") {
		return false
	}
	if strings.HasSuffix(path, "/") {
		return false
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == '/':
		default:
			return false
		}
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		if c := seg[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// validateAddress enforces POL-005's destination classes. IPv4-mapped and
// non-canonical forms are rejected in every environment; in production the
// address must be public unicast — loopback, unspecified, multicast,
// private and link-local destinations never ship.
func validateAddress(what, s string, env Environment) error {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return fmt.Errorf("%s %q is not an IP address", what, s)
	}
	if addr.Zone() != "" {
		return fmt.Errorf("%s %q carries a zone: addresses are bare literals", what, s)
	}
	if addr.Is4In6() {
		return fmt.Errorf("%s %q is an IPv4-mapped form; write the plain IPv4 instead", what, s)
	}
	// Shape comes from the regexes: dotted-quad IPv4 without leading zeros,
	// and full eight-group IPv6 without :: compression. Both spellings are
	// byte-exact, so two reviewers see the same address (POL-005).
	if !reIPv4.MatchString(s) && !reIPv6.MatchString(s) {
		return fmt.Errorf("%s %q is not in the canonical form the schema requires", what, s)
	}
	if addr.IsUnspecified() {
		return fmt.Errorf("%s %q is the unspecified address", what, s)
	}
	if env == EnvProduction {
		if addr.IsLoopback() {
			return fmt.Errorf("%s %q is a loopback address", what, s)
		}
		if addr.IsPrivate() {
			return fmt.Errorf("%s %q is a private address", what, s)
		}
		if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
			return fmt.Errorf("%s %q is a link-local address", what, s)
		}
		if addr.IsMulticast() {
			return fmt.Errorf("%s %q is a multicast address", what, s)
		}
		if addr.Is4() && addr.As4() == [4]byte{255, 255, 255, 255} {
			return fmt.Errorf("%s %q is the broadcast address", what, s)
		}
	}
	return nil
}

func validatePort(what string, p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s %d is outside 1-65535", what, p)
	}
	return nil
}

func validateDestination(what string, d Destination, env Environment) error {
	if err := validateAddress(what+" ipv4", d.IPv4, env); err != nil {
		return err
	}
	if d.IPv6 != "" {
		if err := validateAddress(what+" ipv6", d.IPv6, env); err != nil {
			return err
		}
	}
	if err := validatePort(what+" https_port", d.HTTPSPort); err != nil {
		return err
	}
	return validatePort(what+" http_port", d.HTTPPort)
}

// ruleCtx carries the cross-reference targets a bundle's rules may point at.
type ruleCtx struct {
	env        Environment
	trustIDs   map[string]bool
	serviceIDs map[string]bool
}

// validateRules validates each rule and rejects duplicates: two rules with
// the same ID, and two rules of equal priority claiming the same name. A
// redirect names one transport; a passthrough claims all of them. An exact
// name and a covering suffix are different priorities, so a `never` inside
// a family stays expressible.
func validateRules(rules []Rule, ctx *ruleCtx) error {
	ids := make(map[string]bool, len(rules))
	type claim struct{ kind, value, transport string }
	claims := make(map[claim]bool, len(rules))
	passthrough := make(map[claim]bool, len(rules))
	for i := range rules {
		r := &rules[i]
		if !reRuleID.MatchString(r.ID) || len(r.ID) > 128 {
			return fmt.Errorf("rule %d: id %q is not a rule id", i, r.ID)
		}
		if ids[r.ID] {
			return fmt.Errorf("rule id %q appears twice", r.ID)
		}
		ids[r.ID] = true

		m := r.Match
		switch {
		case m.Exact != nil && m.Suffix != nil:
			return fmt.Errorf("rule %q matches both exact and suffix", r.ID)
		case m.Exact != nil:
			if !validHostname(*m.Exact) {
				return fmt.Errorf("rule %q exact %q is not a hostname", r.ID, *m.Exact)
			}
			if m.IncludeApex {
				return fmt.Errorf("rule %q sets include_apex on an exact match", r.ID)
			}
			if ctx.env == EnvProduction && *m.Exact == bootstrapHost {
				return fmt.Errorf("rule %q claims the bootstrap host %q", r.ID, bootstrapHost)
			}
		case m.Suffix != nil:
			if !validSuffix(*m.Suffix) {
				return fmt.Errorf("rule %q suffix %q is not a leading-dot family", r.ID, *m.Suffix)
			}
		default:
			return fmt.Errorf("rule %q matches nothing", r.ID)
		}

		transport := r.Transport
		switch transport {
		case "", TransportTCP, TransportUDP:
		default:
			return fmt.Errorf("rule %q transport %q is neither tcp nor udp", r.ID, r.Transport)
		}
		if transport == "" {
			if r.Action == ActionPassthrough {
				transport = "*"
			} else {
				transport = TransportTCP
			}
		}
		c := claim{kind: "exact", value: "*", transport: transport}
		if m.Exact != nil {
			c.kind, c.value = "exact", *m.Exact
		} else {
			c.kind, c.value = "suffix", *m.Suffix
		}
		if r.Action == ActionPassthrough {
			// A passthrough conflicts with every earlier claim on the name:
			// a name left alone cannot also be moved on any transport.
			if claims[claim{c.kind, c.value, TransportTCP}] ||
				claims[claim{c.kind, c.value, TransportUDP}] ||
				passthrough[claim{c.kind, c.value, "*"}] {
				return fmt.Errorf("rule %q conflicts with an earlier rule claiming the same name and transport", r.ID)
			}
			passthrough[claim{c.kind, c.value, "*"}] = true
		} else {
			if claims[c] || passthrough[claim{c.kind, c.value, "*"}] {
				return fmt.Errorf("rule %q conflicts with an earlier rule claiming the same name and transport", r.ID)
			}
			claims[c] = true
		}

		switch r.Action {
		case ActionRedirect:
			transport := r.Transport
			if transport == "" {
				transport = TransportTCP
			}
			if transport != TransportTCP && transport != TransportUDP {
				return fmt.Errorf("rule %q transport %q is neither tcp nor udp", r.ID, r.Transport)
			}
			if r.Destination != nil {
				if r.Destination.IPv4 == "" && r.Destination.IPv6 == "" {
					return fmt.Errorf("rule %q carries an empty destination", r.ID)
				}
				if r.Destination.IPv4 != "" {
					if err := validateAddress("rule "+r.ID+" destination ipv4", r.Destination.IPv4, ctx.env); err != nil {
						return err
					}
				}
				if r.Destination.IPv6 != "" {
					if err := validateAddress("rule "+r.ID+" destination ipv6", r.Destination.IPv6, ctx.env); err != nil {
						return err
					}
				}
			}
			if len(r.Ports) > 32 {
				return fmt.Errorf("rule %q carries more than 32 ports", r.ID)
			}
			seenPorts := map[int]bool{}
			for _, p := range r.Ports {
				if err := validatePort("rule "+r.ID+" port", p); err != nil {
					return err
				}
				if seenPorts[p] {
					return fmt.Errorf("rule %q lists port %d twice", r.ID, p)
				}
				seenPorts[p] = true
			}
			if r.TLS != nil {
				if r.TLS.SNI != "" && !validHostname(r.TLS.SNI) {
					return fmt.Errorf("rule %q tls sni %q is not a hostname", r.ID, r.TLS.SNI)
				}
				if r.TLS.Trust != "" && !ctx.trustIDs[r.TLS.Trust] {
					return fmt.Errorf("rule %q tls trust %q is not a declared trust entry", r.ID, r.TLS.Trust)
				}
				if r.TLS.SNI == "" && r.TLS.Trust == "" {
					return fmt.Errorf("rule %q carries an empty tls block", r.ID)
				}
			}
			if r.ServiceID != "" && !ctx.serviceIDs[r.ServiceID] {
				return fmt.Errorf("rule %q service_id %q is not a declared service", r.ID, r.ServiceID)
			}
			if len(r.Readiness) > 256 {
				return fmt.Errorf("rule %q readiness reference is longer than 256 bytes", r.ID)
			}
		case ActionPassthrough:
			if r.Destination != nil || r.Transport != "" || len(r.Ports) > 0 || r.TLS != nil || r.ServiceID != "" {
				return fmt.Errorf("passthrough rule %q carries redirect-only fields", r.ID)
			}
		default:
			return fmt.Errorf("rule %q has unknown action %q", r.ID, r.Action)
		}
	}
	return nil
}

func validateServices(services []Service, ctx *ruleCtx) error {
	ids := make(map[string]bool, len(services))
	for i := range services {
		s := &services[i]
		if !reNameID.MatchString(s.ID) {
			return fmt.Errorf("service %d: id %q is not a service id", i, s.ID)
		}
		if ids[s.ID] {
			return fmt.Errorf("service id %q appears twice", s.ID)
		}
		ids[s.ID] = true
		u, err := url.Parse(s.URL)
		if err != nil {
			return fmt.Errorf("service %q url does not parse: %v", s.ID, err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("service %q url scheme %q is neither https nor http", s.ID, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("service %q url %q has no host", s.ID, s.URL)
		}
		host := u.Hostname()
		if net.ParseIP(host) != nil {
			if err := validateAddress("service "+s.ID+" host", host, ctx.env); err != nil {
				return err
			}
		} else if !validHostname(host) {
			return fmt.Errorf("service %q url host %q is not a hostname", s.ID, host)
		}
		if p := u.Port(); p != "" {
			n := 0
			for _, c := range p {
				if c < '0' || c > '9' {
					return fmt.Errorf("service %q url port %q is not numeric", s.ID, p)
				}
				n = n*10 + int(c-'0')
				if n > 65535 {
					return fmt.Errorf("service %q url port %d is outside 1-65535", s.ID, n)
				}
			}
			if n < 1 {
				return fmt.Errorf("service %q url port %q is outside 1-65535", s.ID, p)
			}
		}
		switch s.Transport {
		case "", TransportTCP, TransportUDP:
		default:
			return fmt.Errorf("service %q transport %q is neither tcp nor udp", s.ID, s.Transport)
		}
		if s.Trust != "" && !ctx.trustIDs[s.Trust] {
			return fmt.Errorf("service %q trust %q is not a declared trust entry", s.ID, s.Trust)
		}
		// POL-008 completeness: an https service without a trust reference
		// cannot be verified by a scoped-CA client.
		if u.Scheme == "https" && s.Trust == "" {
			return fmt.Errorf("service %q speaks https but names no trust entry", s.ID)
		}
	}
	return nil
}

func validateTrust(trust []Trust) (map[string]bool, error) {
	ids := make(map[string]bool, len(trust))
	paths := make(map[string]bool, len(trust))
	for i := range trust {
		t := &trust[i]
		if !reNameID.MatchString(t.ID) {
			return nil, fmt.Errorf("trust %d: id %q is not a trust id", i, t.ID)
		}
		if ids[t.ID] {
			return nil, fmt.Errorf("trust id %q appears twice", t.ID)
		}
		ids[t.ID] = true
		if !validArchivePath(t.Artifact) {
			return nil, fmt.Errorf("trust %q artifact path %q is not a safe archive path", t.ID, t.Artifact)
		}
		if paths[t.Artifact] {
			return nil, fmt.Errorf("trust %q reuses artifact path %q", t.ID, t.Artifact)
		}
		paths[t.Artifact] = true
		if !reScope.MatchString(t.Scope) {
			return nil, fmt.Errorf("trust %q scope %q is not a scope", t.ID, t.Scope)
		}
	}
	return ids, nil
}

func validatePatchSets(sets []PatchSet) (map[string]bool, error) {
	ids := make(map[string]bool, len(sets))
	for i := range sets {
		p := &sets[i]
		if !reNameID.MatchString(p.ID) {
			return nil, fmt.Errorf("patch set %d: id %q is not a patch set id", i, p.ID)
		}
		if ids[p.ID] {
			return nil, fmt.Errorf("patch set id %q appears twice", p.ID)
		}
		ids[p.ID] = true
		if !validArchivePath(p.Artifact) {
			return nil, fmt.Errorf("patch set %q artifact path %q is not a safe archive path", p.ID, p.Artifact)
		}
		if p.MinFirmware == "" || len(p.MinFirmware) > 32 {
			return nil, fmt.Errorf("patch set %q carries no usable min_firmware", p.ID)
		}
		if len(p.MaxFirmware) > 32 {
			return nil, fmt.Errorf("patch set %q max_firmware is longer than 32 bytes", p.ID)
		}
	}
	// Dependencies resolve inside the same set, and never cycle (POL-008).
	const white, grey, black = 0, 1, 2
	color := make(map[string]int, len(sets))
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch color[id] {
		case black:
			return nil
		case grey:
			return fmt.Errorf("patch sets form a dependency cycle at %q: %v", id, append(path, id))
		}
		color[id] = grey
		for _, set := range sets {
			if set.ID != id {
				continue
			}
			for _, dep := range set.DependsOn {
				if !ids[dep] {
					return fmt.Errorf("patch set %q depends on undeclared patch set %q", id, dep)
				}
				if err := visit(dep, append(path, id)); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, set := range sets {
		if err := visit(set.ID, nil); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// checkCapabilities keeps the declared capability list honest: it must name
// exactly what the content requires, because a client that does not
// implement a declared capability must reject the document, and a client
// must never be surprised by content the capability list stayed silent about.
func checkCapabilities(declared []string, rules []Rule, services []Service, trust []Trust) error {
	known := map[string]bool{
		CapDNSSuffix:   false,
		CapDNSExact:    false,
		CapDNSOverride: false,
		CapTLSScopedCA: false,
		CapServiceURL:  false,
	}
	for _, c := range declared {
		if _, ok := known[c]; !ok {
			return fmt.Errorf("required capability %q is not one this schema knows", c)
		}
		if known[c] {
			return fmt.Errorf("required capability %q is declared twice", c)
		}
		known[c] = true
	}
	var hasExact, hasSuffix, hasOverride bool
	for i := range rules {
		switch {
		case rules[i].Match.Exact != nil:
			hasExact = true
		case rules[i].Match.Suffix != nil:
			hasSuffix = true
		}
		if rules[i].Action == ActionRedirect && rules[i].Destination != nil {
			hasOverride = true
		}
	}
	content := map[string]bool{
		CapDNSExact:    hasExact,
		CapDNSSuffix:   hasSuffix,
		CapDNSOverride: hasOverride,
		CapTLSScopedCA: len(trust) > 0,
		CapServiceURL:  len(services) > 0,
	}
	for c, wanted := range content {
		if known[c] != wanted {
			return fmt.Errorf("capability %s is declared=%v but the content says %v", c, known[c], wanted)
		}
	}
	return nil
}

func validateLimits(b *Bundle) error {
	if len(b.Rules) > MaxRules {
		return fmt.Errorf("%d rules is over the %d limit", len(b.Rules), MaxRules)
	}
	if len(b.Services) > MaxServices {
		return fmt.Errorf("%d services is over the %d limit", len(b.Services), MaxServices)
	}
	if len(b.Trust) > MaxTrustArtifacts {
		return fmt.Errorf("%d trust entries is over the %d limit", len(b.Trust), MaxTrustArtifacts)
	}
	if len(b.PatchSets) > MaxPatchSets {
		return fmt.Errorf("%d patch sets is over the %d limit", len(b.PatchSets), MaxPatchSets)
	}
	return nil
}

// Validate checks a decoded bundle against PRD 01: structure, address
// classes per environment, cross-references, completeness and limits. The
// environment decides how strict destination classes are; production is the
// default a certified bundle faces.
func (b *Bundle) Validate(env Environment) error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema version %d is not %d", b.SchemaVersion, SchemaVersion)
	}
	if !rePolicyID.MatchString(b.PolicyID) {
		return fmt.Errorf("policy id %q is not a policy id", b.PolicyID)
	}
	if b.Sequence < 1 {
		return fmt.Errorf("sequence %d is not positive", b.Sequence)
	}
	if !slices.Contains(netprofile.PlatformsAllowed, b.Platform) {
		return fmt.Errorf("platform %q is not one the PRD names", b.Platform)
	}
	if !reChannel.MatchString(b.Channel) {
		return fmt.Errorf("channel %q is not a channel", b.Channel)
	}
	if !reCeiling.MatchString(b.CeilingRevision) {
		return fmt.Errorf("ceiling revision %q is not a ceiling revision", b.CeilingRevision)
	}
	if b.IssuedAt.IsZero() {
		return fmt.Errorf("issued_at is missing")
	}
	if !isSHA256(b.CanonicalPolicySHA256) {
		return fmt.Errorf("canonical_policy_sha256 is not a lowercase sha-256")
	}
	if err := validateLimits(b); err != nil {
		return err
	}
	if err := validateDestination("default destination", b.DefaultDestination, env); err != nil {
		return err
	}
	trustIDs, err := validateTrust(b.Trust)
	if err != nil {
		return err
	}
	if _, err := validatePatchSets(b.PatchSets); err != nil {
		return err
	}
	svcCtx := &ruleCtx{env: env, trustIDs: trustIDs}
	if err := validateServices(b.Services, svcCtx); err != nil {
		return err
	}
	serviceIDs := make(map[string]bool, len(b.Services))
	for i := range b.Services {
		serviceIDs[b.Services[i].ID] = true
	}
	if err := validateRules(b.Rules, &ruleCtx{env: env, trustIDs: trustIDs, serviceIDs: serviceIDs}); err != nil {
		return err
	}
	return checkCapabilities(b.RequiredCapabilities, b.Rules, b.Services, b.Trust)
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
