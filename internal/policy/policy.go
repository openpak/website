// Package policy is the v2 half of the network platform (PRD
// emulators/prds/universal-tls-forwarding/01): the machine-readable bundle schema, its
// validation, and the deterministic generation of one canonical policy
// revision plus per-platform projections.
//
// It owns no host data. Every hostname, family and destination comes from
// website/internal/netprofile — the routing configuration and the platform
// map remain the only authored inputs — or from explicit, reviewed inputs
// carried by the caller. This package turns those pinned inputs into bytes
// that can be signed, published and verified; it is not itself the registry.
//
// Determinism rule: identical pinned inputs produce byte-identical output.
// Nothing derived from wall-clock time, map iteration order or the registry's
// sequence counter enters the canonical policy bytes; sequence and issue time
// are publication fields a projection takes as arguments.
package policy

import "time"

// SchemaVersion is the major version these types and the embedded schemas
// describe. A consumer that does not know a major version rejects the whole
// document; it never guesses.
const SchemaVersion = 2

// Publication limits from PRD 01. They are conformance inputs: generation,
// publication and verification all enforce them, and they may change only
// with a declared compatibility revision.
const (
	// MaxPolicyBytes bounds the serialized policy bundle (1 MiB).
	MaxPolicyBytes = 1 << 20
	// MaxRules bounds rules per bundle.
	MaxRules = 10000
	// MaxServices bounds service entries per bundle.
	MaxServices = 256
	// MaxTrustArtifacts bounds trust entries per bundle.
	MaxTrustArtifacts = 32
	// MaxPatchSets bounds patch sets per bundle.
	MaxPatchSets = 256
	// MaxPatchArtifactBytes bounds one patch artifact (64 MiB).
	MaxPatchArtifactBytes = 64 << 20
	// MaxPatchTotalBytes bounds all patch artifacts of one bundle (256 MiB).
	MaxPatchTotalBytes = 256 << 20
	// MaxPayloadBytes bounds one manifest payload of any kind (1 GiB).
	MaxPayloadBytes = 1 << 30
	// MaxManifestBytes bounds the serialized manifest.
	MaxManifestBytes = 1 << 20
)

// PolicyPayloadPath is the archive-relative path every bundle's policy
// document is published under.
const PolicyPayloadPath = "policy.json"

// TrustArtifactPath is where the scoped CA artifact lands in a bundle.
const TrustArtifactPath = "trust/ca.pem"

// Environment selects how strictly destination address classes are enforced
// (POL-005). Production rejects loopback, unspecified, multicast, private and
// link-local destinations, including IPv4-mapped forms; a private deployment
// runs under a separately registered nonproduction environment.
type Environment string

const (
	// EnvProduction is the only environment that may issue certified bundles.
	EnvProduction Environment = "production"
	// EnvNonproduction is a registered private/staging deployment.
	EnvNonproduction Environment = "nonproduction"
)

// Capabilities declare what a consumer must implement to apply a bundle. A
// client adapter may not ignore a declared required capability.
const (
	CapDNSSuffix   = "dns.suffix.v1"
	CapDNSExact    = "dns.exact.v1"
	CapDNSOverride = "dns.override.v1"
	CapTLSScopedCA = "tls.scoped-ca.v1"
	CapServiceURL  = "service.url.v1"
)

// Rule actions.
const (
	ActionRedirect    = "redirect"
	ActionPassthrough = "passthrough"
)

// Transports.
const (
	TransportTCP = "tcp"
	TransportUDP = "udp"
)

// Media types the manifest may bind.
const (
	MediaTypeJSON        = "application/json"
	MediaTypePEM         = "application/x-pem-file"
	MediaTypeOctetStream = "application/octet-stream"
)

// ManifestKind is the manifest's self-declared document kind.
const ManifestKind = "openpak-bundle-manifest"

// Match selects the names a rule applies to: exactly one of Exact or Suffix.
// A suffix is a leading-dot family; IncludeApex adds the family's own apex
// name. There are no wildcards: specificity is exact over suffix, and among
// suffixes the longest wins.
type Match struct {
	Exact       *string `json:"exact,omitempty"`
	Suffix      *string `json:"suffix,omitempty"`
	IncludeApex bool    `json:"include_apex,omitempty"`
}

// TLSRef names the SNI a redirected TLS client must present and the trust
// entry whose certificate chain covers the connection.
type TLSRef struct {
	SNI   string `json:"sni,omitempty"`
	Trust string `json:"trust,omitempty"`
}

// Rule is one reviewed routing decision. A passthrough rule (a v1 `never`)
// carries none of the redirect-only fields: the name must reach the real
// internet untouched.
type Rule struct {
	ID          string          `json:"id"`
	Match       Match           `json:"match"`
	Action      string          `json:"action"`
	Destination *RedirectTarget `json:"destination,omitempty"`
	Transport   string          `json:"transport,omitempty"`
	Ports       []int           `json:"ports,omitempty"`
	TLS         *TLSRef         `json:"tls,omitempty"`
	ServiceID   string          `json:"service_id,omitempty"`
	Readiness   string          `json:"readiness,omitempty"`
}

// RedirectTarget moves one rule off the default destination. Ports ride with
// the default destination; a rule cannot move ports.
type RedirectTarget struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// Destination is where redirected traffic lands.
type Destination struct {
	IPv4      string `json:"ipv4"`
	IPv6      string `json:"ipv6,omitempty"`
	HTTPSPort int    `json:"https_port"`
	HTTPPort  int    `json:"http_port"`
}

// Service is a complete URL for clients that address a service rather than
// resolve a name (Cemu today). Trust names the trust entry that verifies the
// service's TLS; plain http carries none.
type Service struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Transport string `json:"transport,omitempty"`
	Trust     string `json:"trust,omitempty"`
}

// Trust is one scoped trust artifact. A CA for redirected services is never
// a general-purpose anchor.
type Trust struct {
	ID       string `json:"id"`
	Artifact string `json:"artifact"`
	Scope    string `json:"scope"`
}

// PatchSet is one console patch artifact and the firmware window it applies
// to. Patch manifests own firmware compatibility; dependencies must resolve
// inside the same bundle.
type PatchSet struct {
	ID          string   `json:"id"`
	Artifact    string   `json:"artifact"`
	MinFirmware string   `json:"min_firmware"`
	MaxFirmware string   `json:"max_firmware,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// Bundle is one signed platform projection of the canonical policy: the
// document a certified client fetches, verifies and applies.
type Bundle struct {
	SchemaVersion         int         `json:"schema_version"`
	PolicyID              string      `json:"policy_id"`
	Sequence              int64       `json:"sequence"`
	Platform              string      `json:"platform"`
	Channel               string      `json:"channel"`
	IssuedAt              time.Time   `json:"issued_at"`
	CanonicalPolicySHA256 string      `json:"canonical_policy_sha256"`
	RequiredCapabilities  []string    `json:"required_capabilities"`
	CeilingRevision       string      `json:"ceiling_revision"`
	DefaultDestination    Destination `json:"default_destination"`
	Rules                 []Rule      `json:"rules"`
	Services              []Service   `json:"services"`
	Trust                 []Trust     `json:"trust"`
	PatchSets             []PatchSet  `json:"patch_sets"`
}

// Payload is one manifest-bound artifact.
type Payload struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Length    int64  `json:"length"`
	SHA256    string `json:"sha256"`
}

// Manifest is the signed target manifest binding every payload of one
// published bundle.
type Manifest struct {
	SchemaVersion        int       `json:"schema_version"`
	Kind                 string    `json:"kind"`
	PolicyID             string    `json:"policy_id"`
	Platform             string    `json:"platform"`
	Channel              string    `json:"channel"`
	Sequence             int64     `json:"sequence"`
	RequiredCapabilities []string  `json:"required_capabilities"`
	Payloads             []Payload `json:"payloads"`
}
