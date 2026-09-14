package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// fixturePublication builds the bundle and payload bytes of one coherent
// switch publication with a trust artifact.
func fixturePublication(t *testing.T) (*Bundle, []PayloadInput) {
	t.Helper()
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	bundleBytes, err := MarshalBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	payloads := []PayloadInput{
		{Path: PolicyPayloadPath, MediaType: MediaTypeJSON, Bytes: bundleBytes},
		{Path: TrustArtifactPath, MediaType: MediaTypePEM, Bytes: []byte("pem")},
	}
	return b, payloads
}

func TestBuildManifestBindsPayloads(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	bundleBytes, err := MarshalBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	caBytes := []byte("fixture pem")
	m, err := BuildManifest("openpak-production", "switch", "stable", 42,
		[]string{CapDNSSuffix, CapDNSExact, CapDNSOverride, CapTLSScopedCA},
		[]PayloadInput{
			{Path: TrustArtifactPath, MediaType: MediaTypePEM, Bytes: caBytes},
			{Path: PolicyPayloadPath, MediaType: MediaTypeJSON, Bytes: bundleBytes},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Payloads) != 2 || m.Payloads[0].Path != PolicyPayloadPath {
		t.Fatalf("payloads not sorted by path: %+v", m.Payloads)
	}
	sum := sha256.Sum256(caBytes)
	if m.Payloads[1].SHA256 != hex.EncodeToString(sum[:]) || m.Payloads[1].Length != int64(len(caBytes)) {
		t.Fatalf("trust payload not bound correctly: %+v", m.Payloads[1])
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest does not validate: %v", err)
	}
}

func TestManifestRejections(t *testing.T) {
	b, payloads := fixturePublication(t)
	build := func(paths ...string) (*Manifest, error) {
		ins := make([]PayloadInput, 0, len(paths))
		for _, p := range paths {
			if p == TrustArtifactPath {
				ins = append(ins, payloads[1])
				continue
			}
			ins = append(ins, PayloadInput{Path: p, MediaType: MediaTypeJSON, Bytes: payloads[0].Bytes})
		}
		return BuildManifest(b.PolicyID, b.Platform, b.Channel, b.Sequence, b.RequiredCapabilities, ins)
	}
	for _, bad := range []string{"../evil.json", "/abs.json", "a//b.json", "./a.json", "a/../b.json", "policy.json/"} {
		if _, err := build(PolicyPayloadPath, bad); err == nil {
			t.Errorf("unsafe path %q accepted", bad)
		}
	}
	// Distinct paths build and validate.
	m, err := build(PolicyPayloadPath, "extra/meta.json")
	if err != nil {
		t.Fatalf("distinct paths rejected: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest does not validate: %v", err)
	}
}

func TestBundleManifestCoherence(t *testing.T) {
	b, payloads := fixturePublication(t)
	m, err := BuildManifest(b.PolicyID, b.Platform, b.Channel, b.Sequence, b.RequiredCapabilities, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundleCoherence(b, m); err != nil {
		t.Fatalf("coherent publication rejected: %v", err)
	}

	// Identity mismatch: the manifest describes another channel.
	other, err := BuildManifest(b.PolicyID, b.Platform, "staging", b.Sequence, b.RequiredCapabilities, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundleCoherence(b, other); err == nil {
		t.Fatal("identity mismatch accepted")
	}

	// A bundle with a patch set needs its artifact bound, within bounds.
	patched := *b
	patched.PatchSets = []PatchSet{{ID: "patch-a", Artifact: "patches/a.bin", MinFirmware: "18.1.0"}}
	if err := ValidateBundleCoherence(&patched, m); err == nil || !strings.Contains(err.Error(), "patches/a.bin") {
		t.Fatalf("unbound patch artifact accepted: %v", err)
	}
	withPatch := append(append([]PayloadInput(nil), payloads...),
		PayloadInput{Path: "patches/a.bin", MediaType: MediaTypeOctetStream, Bytes: []byte("patch bytes")})
	patchManifest, err := BuildManifest(b.PolicyID, b.Platform, b.Channel, b.Sequence, b.RequiredCapabilities, withPatch)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundleCoherence(&patched, patchManifest); err != nil {
		t.Fatalf("patched publication rejected: %v", err)
	}

	// Patch size bounds (PRD 01: 64 MiB each, 256 MiB total). The bound is
	// checked against the artifacts the bundle declares, so the manifest and
	// the patch sets must agree on what exists.
	patched.PatchSets = []PatchSet{
		{ID: "patch-a", Artifact: "patches/a.bin", MinFirmware: "18.1.0"},
		{ID: "patch-huge", Artifact: "patches/huge.bin", MinFirmware: "18.1.0"},
	}
	huge := *patchManifest
	huge.Payloads = append(huge.Payloads, Payload{
		Path: "patches/huge.bin", MediaType: MediaTypeOctetStream,
		Length: MaxPatchArtifactBytes + 1, SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err := ValidateBundleCoherence(&patched, &huge); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("oversized patch accepted: %v", err)
	}

	// Five 64 MiB patches are over the 256 MiB total.
	total := *patchManifest
	patches := []PatchSet{{ID: "patch-a", Artifact: "patches/a.bin", MinFirmware: "18.1.0"}}
	for _, p := range []string{"h1", "h2", "h3", "h4", "h5"} {
		total.Payloads = append(total.Payloads, Payload{
			Path: "patches/" + p + ".bin", MediaType: MediaTypeOctetStream,
			Length: MaxPatchArtifactBytes, SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		})
		patches = append(patches, PatchSet{ID: "patch-" + p, Artifact: "patches/" + p + ".bin", MinFirmware: "18.1.0"})
	}
	patched.PatchSets = patches
	if err := ValidateBundleCoherence(&patched, &total); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("patch total over 256 MiB accepted: %v", err)
	}
}
