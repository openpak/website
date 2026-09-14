package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

// Canonical is one reviewed policy revision: the platform-agnostic master
// every bundle projects from (POL-004). It carries no publication fields —
// no sequence, no channel, no timestamp — so its digest is the pure content
// identity shared by every projection, and re-issuing a projection with a
// new timestamp alone cannot change it.
type Canonical struct {
	SchemaVersion int                        `json:"schema_version"`
	PolicyID      string                     `json:"policy_id"`
	Platforms     map[string]*PlatformPolicy `json:"platforms"`
	Trust         []Trust                    `json:"trust"`
	PatchSets     []PatchSet                 `json:"patch_sets"`
}

// PlatformPolicy is one platform's slice of the canonical revision.
type PlatformPolicy struct {
	CeilingRevision      string      `json:"ceiling_revision"`
	DefaultDestination   Destination `json:"default_destination"`
	RequiredCapabilities []string    `json:"required_capabilities"`
	Rules                []Rule      `json:"rules"`
	Services             []Service   `json:"services"`
}

// CA is the trust anchor a TLS-enabled revision distributes. Like v1, TLS is
// enabled exactly when a CA is present (POL-008).
type CA struct {
	ID       string
	PEM      []byte
	NotAfter time.Time
}

// TransportSpec declares the transport facts for one name that no Traefik
// hostname can supply: UDP services like Photon and the NAT responders
// (POL-002). It rides in the build inputs until the registry owns the
// reviewed declarations; the platform map stays the single authored source.
type TransportSpec struct {
	Transport string
	Ports     []int
}

// Inputs are the pinned inputs of one canonical revision. Nothing here is
// derived at request time: a revision is built from reviewed values, and the
// same values must build the same revision again.
type Inputs struct {
	PolicyID string
	Map      *netprofile.PlatformMap
	CA       *CA
	// CeilingRevisions names the approved ceiling each platform's projection
	// stays inside (POL-004). A platform without a revision fails generation.
	CeilingRevisions map[string]string
	// TransportMeta carries the reviewed transport declarations for exact
	// names and overrides, keyed platform → name. Names without one are
	// plain TCP DNS redirects.
	TransportMeta map[string]map[string]TransportSpec
}

// Build assembles the canonical revision from pinned inputs and validates
// it. Determinism rule: every iteration runs over sorted keys, so map order
// cannot reach the bytes; the result is byte-identical for identical inputs.
func Build(in Inputs, env Environment) (*Canonical, error) {
	if in.Map == nil {
		return nil, fmt.Errorf("policy: build inputs carry no platform map")
	}
	c := &Canonical{
		SchemaVersion: SchemaVersion,
		PolicyID:      in.PolicyID,
		Platforms:     make(map[string]*PlatformPolicy, len(in.Map.Platforms)),
	}
	if in.CA != nil {
		if !reNameID.MatchString(in.CA.ID) {
			return nil, fmt.Errorf("policy: CA id %q is not a trust id", in.CA.ID)
		}
		if len(in.CA.PEM) == 0 {
			return nil, fmt.Errorf("policy: CA %q carries no PEM bytes", in.CA.ID)
		}
		c.Trust = []Trust{{
			ID:       in.CA.ID,
			Artifact: TrustArtifactPath,
			Scope:    "redirected-services",
		}}
	}

	for _, name := range sortedPlatformNames(in.Map) {
		p := in.Map.Platforms[name]
		ceiling, ok := in.CeilingRevisions[name]
		if !ok || ceiling == "" {
			return nil, fmt.Errorf("policy: platform %q has no ceiling revision; generation would leave the approved ceiling undefined", name)
		}
		slice, err := buildPlatformSlice(name, p, in, c.Trust)
		if err != nil {
			return nil, err
		}
		slice.CeilingRevision = ceiling
		c.Platforms[name] = slice
	}

	if err := c.Validate(env); err != nil {
		return nil, fmt.Errorf("policy: generated revision does not validate: %w", err)
	}
	return c, nil
}

func sortedPlatformNames(m *netprofile.PlatformMap) []string {
	names := make([]string, 0, len(m.Platforms))
	for name := range m.Platforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildPlatformSlice mirrors v1's per-platform semantics exactly: families
// become suffix rules (the apex included, as v1's coverage check did),
// authored exacts become exact rules, `never` becomes passthrough, and an
// override keeps its own address. Routed names ride the families; a routed
// name no platform claims is drift netprofile already reports, and drift
// never enters a bundle.
func buildPlatformSlice(name string, p netprofile.Platform, in Inputs, trust []Trust) (*PlatformPolicy, error) {
	meta := in.TransportMeta[name]
	trustID := ""
	if len(trust) > 0 {
		trustID = trust[0].ID
	}
	slice := &PlatformPolicy{
		DefaultDestination: Destination{
			IPv4:      in.Map.Server.Address,
			HTTPSPort: in.Map.Server.HTTPSPort,
			HTTPPort:  in.Map.Server.HTTPPort,
		},
		Rules:    []Rule{},
		Services: []Service{},
	}
	for _, family := range slicesSorted(p.Families) {
		f := family
		slice.Rules = append(slice.Rules, Rule{
			ID:     "suffix." + strings.TrimPrefix(f, "."),
			Match:  Match{Suffix: &f, IncludeApex: true},
			Action: ActionRedirect,
		})
	}
	for _, e := range slicesSorted(p.Exact) {
		slice.Rules = append(slice.Rules, namedRule("exact."+e, e, ActionRedirect, "", meta[e]))
	}
	for _, n := range slicesSorted(p.Never) {
		slice.Rules = append(slice.Rules, Rule{
			ID:     "passthrough." + n,
			Match:  Match{Exact: &n},
			Action: ActionPassthrough,
		})
	}
	for _, host := range sortedOverrideHosts(p.Overrides) {
		slice.Rules = append(slice.Rules, namedRule("override."+host, host, ActionRedirect, p.Overrides[host], meta[host]))
	}
	sort.Slice(slice.Rules, func(i, j int) bool { return slice.Rules[i].ID < slice.Rules[j].ID })

	for _, id := range sortedServiceIDs(p.Services) {
		s := Service{ID: id, URL: p.Services[id]}
		if strings.HasPrefix(p.Services[id], "https://") {
			s.Trust = trustID
		}
		slice.Services = append(slice.Services, s)
	}
	slice.RequiredCapabilities = deriveCapabilities(slice.Rules, slice.Services, len(trust) > 0)
	return slice, nil
}

// namedRule builds one redirect rule for a single name, carrying the
// reviewed transport declaration when one exists (POL-002).
func namedRule(id, host, action, destIP string, meta TransportSpec) Rule {
	r := Rule{
		ID:     id,
		Action: action,
	}
	name := host
	r.Match = Match{Exact: &name}
	if destIP != "" {
		r.Destination = &RedirectTarget{IPv4: destIP}
	}
	if meta.Transport != "" {
		r.Transport = meta.Transport
	}
	if len(meta.Ports) > 0 {
		r.Ports = append([]int(nil), meta.Ports...)
		sort.Ints(r.Ports)
	}
	return r
}

func slicesSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sortedOverrideHosts(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for host := range m {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func sortedServiceIDs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// deriveCapabilities reads the required capability list off the content, so
// it can never drift from what the bundle actually asks of a client.
func deriveCapabilities(rules []Rule, services []Service, hasTrust bool) []string {
	var caps []string
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
	if hasSuffix {
		caps = append(caps, CapDNSSuffix)
	}
	if hasExact {
		caps = append(caps, CapDNSExact)
	}
	if hasOverride {
		caps = append(caps, CapDNSOverride)
	}
	if hasTrust {
		caps = append(caps, CapTLSScopedCA)
	}
	if len(services) > 0 {
		caps = append(caps, CapServiceURL)
	}
	return caps
}

// Validate checks the whole revision: identity, per-platform slices against
// their ceilings and destinations, shared trust and patch sets, and the
// cross-platform invariants (no duplicate artifact paths, ceilings present).
func (c *Canonical) Validate(env Environment) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema version %d is not %d", c.SchemaVersion, SchemaVersion)
	}
	if !rePolicyID.MatchString(c.PolicyID) {
		return fmt.Errorf("policy id %q is not a policy id", c.PolicyID)
	}
	if len(c.Platforms) == 0 {
		return fmt.Errorf("canonical revision carries no platforms")
	}
	for _, name := range sortedPlatformNamesIn(c.Platforms) {
		if !containsString(netprofile.PlatformsAllowed, name) {
			return fmt.Errorf("platform %q is not one the PRD names", name)
		}
		slice := c.Platforms[name]
		if !reCeiling.MatchString(slice.CeilingRevision) {
			return fmt.Errorf("platform %q ceiling revision %q is not a ceiling revision", name, slice.CeilingRevision)
		}
		if err := validateDestination("platform "+name+" default destination", slice.DefaultDestination, env); err != nil {
			return err
		}
		trustIDs := make(map[string]bool, len(c.Trust))
		for _, t := range c.Trust {
			trustIDs[t.ID] = true
		}
		if err := validateServices(slice.Services, &ruleCtx{env: env, trustIDs: trustIDs}); err != nil {
			return fmt.Errorf("platform %s: %w", name, err)
		}
		serviceIDs := make(map[string]bool, len(slice.Services))
		for _, s := range slice.Services {
			serviceIDs[s.ID] = true
		}
		if err := validateRules(slice.Rules, &ruleCtx{env: env, trustIDs: trustIDs, serviceIDs: serviceIDs}); err != nil {
			return fmt.Errorf("platform %s: %w", name, err)
		}
		if err := checkCapabilities(slice.RequiredCapabilities, slice.Rules, slice.Services, c.Trust); err != nil {
			return fmt.Errorf("platform %s: %w", name, err)
		}
	}
	if _, err := validateTrust(c.Trust); err != nil {
		return err
	}
	if _, err := validatePatchSets(c.PatchSets); err != nil {
		return err
	}
	return nil
}

func sortedPlatformNamesIn(m map[string]*PlatformPolicy) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Digest is the canonical policy identity: SHA-256 over the canonical JSON
// bytes of the revision. Because the canonical document carries no
// publication fields, re-issuing with a new timestamp or sequence cannot
// move it (POL-M1's semantic identity, at the generation layer).
func (c *Canonical) Digest() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("policy: canonical form: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Project produces one platform/channel bundle from the revision. Sequence
// and issue time are publication facts the registry owns; they take part in
// the bundle bytes but never in the canonical digest.
func (c *Canonical) Project(platform, channel string, sequence int64, issuedAt time.Time) (*Bundle, error) {
	if sequence < 1 {
		return nil, fmt.Errorf("policy: sequence %d is not positive", sequence)
	}
	if issuedAt.IsZero() {
		return nil, fmt.Errorf("policy: issued_at is missing")
	}
	if !reChannel.MatchString(channel) {
		return nil, fmt.Errorf("policy: channel %q is not a channel", channel)
	}
	slice, ok := c.Platforms[platform]
	if !ok {
		return nil, fmt.Errorf("policy: revision carries no platform %q", platform)
	}
	digest, err := c.Digest()
	if err != nil {
		return nil, err
	}
	b := &Bundle{
		SchemaVersion:         SchemaVersion,
		PolicyID:              c.PolicyID,
		Sequence:              sequence,
		Platform:              platform,
		Channel:               channel,
		IssuedAt:              issuedAt.UTC(),
		CanonicalPolicySHA256: digest,
		RequiredCapabilities:  append([]string(nil), slice.RequiredCapabilities...),
		CeilingRevision:       slice.CeilingRevision,
		DefaultDestination:    slice.DefaultDestination,
		Rules:                 copyRules(slice.Rules),
		Services:              copyServices(slice.Services),
		Trust:                 copyTrust(c.Trust),
		PatchSets:             copyPatchSets(c.PatchSets),
	}
	return b, nil
}

func copyRules(in []Rule) []Rule {
	out := make([]Rule, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Match.Exact != nil {
			v := *in[i].Match.Exact
			out[i].Match.Exact = &v
		}
		if in[i].Match.Suffix != nil {
			v := *in[i].Match.Suffix
			out[i].Match.Suffix = &v
		}
		if in[i].Destination != nil {
			v := *in[i].Destination
			out[i].Destination = &v
		}
		if in[i].TLS != nil {
			v := *in[i].TLS
			out[i].TLS = &v
		}
		if len(in[i].Ports) > 0 {
			out[i].Ports = append([]int(nil), in[i].Ports...)
		}
	}
	return out
}

func copyServices(in []Service) []Service {
	out := make([]Service, 0, len(in))
	return append(out, in...)
}

// copyTrust keeps an empty list an empty list: a published bundle carries
// arrays, never nulls, for every required field.
func copyTrust(in []Trust) []Trust {
	out := make([]Trust, 0, len(in))
	return append(out, in...)
}

func copyPatchSets(in []PatchSet) []PatchSet {
	out := make([]PatchSet, len(in))
	for i := range in {
		out[i] = in[i]
		if len(in[i].DependsOn) > 0 {
			out[i].DependsOn = append([]string(nil), in[i].DependsOn...)
		}
	}
	return out
}

// MarshalBundle serializes a bundle for publication, enforcing the 1 MiB
// policy limit on the way out (POL-008: an incomplete or oversized bundle
// never ships).
func MarshalBundle(b *Bundle) ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("policy: bundle: %w", err)
	}
	if len(data) > MaxPolicyBytes {
		return nil, fmt.Errorf("policy: bundle is %d bytes, over the %d byte limit", len(data), MaxPolicyBytes)
	}
	return data, nil
}
