package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// PayloadInput is one artifact handed to the manifest builder.
type PayloadInput struct {
	Path      string
	MediaType string
	Bytes     []byte
}

// BuildManifest binds the payloads of one publication into the signed target
// manifest: path, byte length, digest, media type, plus the platform and
// capability requirements a consumer must satisfy before trusting any
// payload. Payloads are stored sorted by path, so the manifest bytes are
// deterministic for identical inputs.
func BuildManifest(policyID, platform, channel string, sequence int64, capabilities []string, ins []PayloadInput) (*Manifest, error) {
	if len(ins) < 1 {
		return nil, fmt.Errorf("manifest: a bundle binds at least one payload")
	}
	m := &Manifest{
		SchemaVersion:        SchemaVersion,
		Kind:                 ManifestKind,
		PolicyID:             policyID,
		Platform:             platform,
		Channel:              channel,
		Sequence:             sequence,
		RequiredCapabilities: append([]string(nil), capabilities...),
		Payloads:             make([]Payload, 0, len(ins)),
	}
	for _, in := range ins {
		if !validArchivePath(in.Path) {
			return nil, fmt.Errorf("manifest: payload path %q is not a safe archive path", in.Path)
		}
		switch in.MediaType {
		case MediaTypeJSON, MediaTypePEM, MediaTypeOctetStream:
		default:
			return nil, fmt.Errorf("manifest: payload %q has unknown media type %q", in.Path, in.MediaType)
		}
		if l := int64(len(in.Bytes)); l < 1 || l > MaxPayloadBytes {
			return nil, fmt.Errorf("manifest: payload %q is %d bytes, outside 1-%d", in.Path, l, MaxPayloadBytes)
		}
		sum := sha256.Sum256(in.Bytes)
		m.Payloads = append(m.Payloads, Payload{
			Path:      in.Path,
			MediaType: in.MediaType,
			Length:    int64(len(in.Bytes)),
			SHA256:    hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(m.Payloads, func(i, j int) bool { return m.Payloads[i].Path < m.Payloads[j].Path })
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Validate checks the manifest's own structure: kind, identity fields,
// capabilities, and payload paths unique, safe and bounded.
func (m *Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("manifest: schema version %d is not %d", m.SchemaVersion, SchemaVersion)
	}
	if m.Kind != ManifestKind {
		return fmt.Errorf("manifest: kind %q is not %q", m.Kind, ManifestKind)
	}
	if !rePolicyID.MatchString(m.PolicyID) {
		return fmt.Errorf("manifest: policy id %q is not a policy id", m.PolicyID)
	}
	if !reChannel.MatchString(m.Channel) {
		return fmt.Errorf("manifest: channel %q is not a channel", m.Channel)
	}
	if m.Sequence < 1 {
		return fmt.Errorf("manifest: sequence %d is not positive", m.Sequence)
	}
	if len(m.RequiredCapabilities) < 1 {
		return fmt.Errorf("manifest: carries no required capabilities")
	}
	caps := map[string]bool{}
	for _, c := range m.RequiredCapabilities {
		switch c {
		case CapDNSSuffix, CapDNSExact, CapDNSOverride, CapTLSScopedCA, CapServiceURL:
		default:
			return fmt.Errorf("manifest: capability %q is not one this schema knows", c)
		}
		if caps[c] {
			return fmt.Errorf("manifest: capability %q is declared twice", c)
		}
		caps[c] = true
	}
	if len(m.Payloads) < 1 || len(m.Payloads) > 1024 {
		return fmt.Errorf("manifest: %d payloads is outside 1-1024", len(m.Payloads))
	}
	paths := make(map[string]bool, len(m.Payloads))
	for i := range m.Payloads {
		p := &m.Payloads[i]
		if !validArchivePath(p.Path) {
			return fmt.Errorf("manifest: payload path %q is not a safe archive path", p.Path)
		}
		if paths[p.Path] {
			return fmt.Errorf("manifest: payload path %q appears twice", p.Path)
		}
		paths[p.Path] = true
		switch p.MediaType {
		case MediaTypeJSON, MediaTypePEM, MediaTypeOctetStream:
		default:
			return fmt.Errorf("manifest: payload %q has unknown media type %q", p.Path, p.MediaType)
		}
		if p.Length < 1 || p.Length > MaxPayloadBytes {
			return fmt.Errorf("manifest: payload %q is %d bytes, outside 1-%d", p.Path, p.Length, MaxPayloadBytes)
		}
		if !isSHA256(p.SHA256) {
			return fmt.Errorf("manifest: payload %q digest is not a lowercase sha-256", p.Path)
		}
	}
	return nil
}

// ValidateBundleCoherence checks that a bundle and its manifest describe the
// same publication: identity fields agree, the policy document is bound, and
// every artifact a bundle declares (trust, patches) is present in the
// manifest with the patch size bounds of PRD 01 satisfied. It does not
// verify payload bytes; callers check digests as they unpack.
func ValidateBundleCoherence(b *Bundle, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.PolicyID != b.PolicyID || m.Platform != b.Platform || m.Channel != b.Channel || m.Sequence != b.Sequence {
		return fmt.Errorf("manifest identity (policy %q platform %q channel %q sequence %d) does not match the bundle (policy %q platform %q channel %q sequence %d)",
			m.PolicyID, m.Platform, m.Channel, m.Sequence,
			b.PolicyID, b.Platform, b.Channel, b.Sequence)
	}
	if !equalStrings(m.RequiredCapabilities, b.RequiredCapabilities) {
		return fmt.Errorf("manifest capabilities %v do not match the bundle's %v", m.RequiredCapabilities, b.RequiredCapabilities)
	}
	byPath := make(map[string]*Payload, len(m.Payloads))
	for i := range m.Payloads {
		byPath[m.Payloads[i].Path] = &m.Payloads[i]
	}
	policyPayload, ok := byPath[PolicyPayloadPath]
	if !ok {
		return fmt.Errorf("manifest does not bind %s", PolicyPayloadPath)
	}
	if policyPayload.MediaType != MediaTypeJSON || policyPayload.Length > MaxPolicyBytes {
		return fmt.Errorf("manifest binds %s as %s, %d bytes; a policy document is json within %d",
			PolicyPayloadPath, policyPayload.MediaType, policyPayload.Length, MaxPolicyBytes)
	}
	patchTotal := int64(0)
	for i := range b.PatchSets {
		p, ok := byPath[b.PatchSets[i].Artifact]
		if !ok {
			return fmt.Errorf("manifest does not bind patch artifact %q", b.PatchSets[i].Artifact)
		}
		if p.Length > MaxPatchArtifactBytes {
			return fmt.Errorf("patch artifact %q is %d bytes, over the %d byte limit", p.Path, p.Length, MaxPatchArtifactBytes)
		}
		patchTotal += p.Length
	}
	if patchTotal > MaxPatchTotalBytes {
		return fmt.Errorf("patch artifacts total %d bytes, over the %d byte limit", patchTotal, MaxPatchTotalBytes)
	}
	for i := range b.Trust {
		if _, ok := byPath[b.Trust[i].Artifact]; !ok {
			return fmt.Errorf("manifest does not bind trust artifact %q", b.Trust[i].Artifact)
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
