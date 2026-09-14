package policy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// POL-M1's generation core: identical pinned inputs build byte-identical
// canonical forms, and a projection with only its timestamp moved changes
// its bytes but never its canonical digest.
func TestDeterministicGeneration(t *testing.T) {
	c1 := fixtureCanonical(t, fixtureOptions{})
	c2 := fixtureCanonical(t, fixtureOptions{})

	d1, err := c1.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := c2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("canonical digest moved across builds: %s vs %s", d1, d2)
	}
	b1, err := json.Marshal(c1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(c2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("canonical bytes differ across builds from identical inputs")
	}

	p1, err := c1.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c1.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	j1, err := MarshalBundle(p1)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := MarshalBundle(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(j1, j2) {
		t.Fatal("projection bytes differ for identical inputs")
	}
}

func TestTimestampAloneNeverMovesTheCanonicalDigest(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	digest, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	early, err := c.Project("switch", "stable", 7, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	later, err := c.Project("switch", "stable", 7, fixtureIssuedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if early.CanonicalPolicySHA256 != digest || later.CanonicalPolicySHA256 != digest {
		t.Fatalf("projection carries a digest other than the canonical one: %s, %s, %s",
			digest, early.CanonicalPolicySHA256, later.CanonicalPolicySHA256)
	}
	eBytes, err := MarshalBundle(early)
	if err != nil {
		t.Fatal(err)
	}
	lBytes, err := MarshalBundle(later)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(eBytes, lBytes) {
		t.Fatal("projections issued an hour apart have identical bytes; issued_at is not riding along")
	}
}

// The drift cases from the README and POL-M0, projected onto the switch
// bundle: the second NAT responder keeps its own address and its UDP probe
// ports, the connectivity test passes through, Battle.net stays a family,
// Photon is an exact UDP redirect — and the NRO's broader Photon/Exit Games
// wildcards stay out of the production bundle.
func TestSwitchProjectionCarriesTheDriftCases(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}

	nat := findRule(t, b.Rules, "override.nncs2-lp1.n.n.srv.nintendo.net")
	if nat.Destination == nil || nat.Destination.IPv4 != "145.241.228.207" {
		t.Fatalf("second NAT responder lost its own address: %+v", nat)
	}
	if nat.Transport != TransportUDP || len(nat.Ports) != 2 || nat.Ports[0] != 10025 || nat.Ports[1] != 10125 {
		t.Fatalf("NAT responder rule lost its UDP declaration: %+v", nat)
	}

	pass := findRule(t, b.Rules, "passthrough.conntest.nintendowifi.net")
	if pass.Action != ActionPassthrough {
		t.Fatalf("connectivity test is not passthrough: %+v", pass)
	}
	if pass.Destination != nil || pass.Transport != "" || len(pass.Ports) > 0 {
		t.Fatalf("passthrough rule carries redirect-only fields: %+v", pass)
	}

	suffix := findRule(t, b.Rules, "suffix.battle.net")
	if suffix.Match.Suffix == nil || *suffix.Match.Suffix != ".battle.net" || !suffix.Match.IncludeApex {
		t.Fatalf("Battle.net family lost its apex coverage: %+v", suffix)
	}

	photon := findRule(t, b.Rules, "exact.ns.photonengine.io")
	if photon.Transport != TransportUDP || len(photon.Ports) != 1 || photon.Ports[0] != 5055 {
		t.Fatalf("Photon name server rule lost its UDP declaration: %+v", photon)
	}
	for i := range b.Rules {
		if b.Rules[i].Match.Suffix != nil &&
			(strings.Contains(*b.Rules[i].Match.Suffix, "photonengine") || strings.Contains(*b.Rules[i].Match.Suffix, "exitgames")) {
			t.Fatalf("wildcard %s entered the production bundle; researched wildcards stay inactive inventory", *b.Rules[i].Match.Suffix)
		}
	}
	for _, want := range []string{
		"exact.matchmaker.among.us",
		"exact.matchmaker-eu.among.us",
		"exact.matchmaker-as.among.us",
	} {
		findRule(t, b.Rules, want)
	}

	if b.DefaultDestination.IPv4 != "145.241.199.19" || b.DefaultDestination.HTTPSPort != 443 || b.DefaultDestination.HTTPPort != 80 {
		t.Fatalf("default destination drifted: %+v", b.DefaultDestination)
	}
	for _, cap := range []string{CapDNSSuffix, CapDNSExact, CapDNSOverride, CapTLSScopedCA} {
		if !hasString(b.RequiredCapabilities, cap) {
			t.Errorf("switch bundle lacks capability %s: %v", cap, b.RequiredCapabilities)
		}
	}
	if hasString(b.RequiredCapabilities, CapServiceURL) {
		t.Errorf("switch bundle declares %s with no services", CapServiceURL)
	}
	if b.Sequence != 42 || b.Channel != "stable" || b.Platform != "switch" {
		t.Fatalf("publication fields: %+v", b)
	}
}

func TestWiiUProjectionCarriesURLServices(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("wiiu", "stable", 8, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Services) != 3 {
		t.Fatalf("wiiu services: %+v", b.Services)
	}
	if !hasString(b.RequiredCapabilities, CapServiceURL) {
		t.Errorf("wiiu bundle lacks %s: %v", CapServiceURL, b.RequiredCapabilities)
	}
	// The https services pin the fixture CA; the plain http one names none.
	for _, s := range b.Services {
		if strings.HasPrefix(s.URL, "https://") && s.Trust == "" {
			t.Errorf("https service %q carries no trust reference: %+v", s.ID, s)
		}
		if s.URL == "http://ccs.cdn.wup.shop.openpak.org/ccs/download" && s.Trust != "" {
			t.Errorf("plain http service %q carries a trust reference: %+v", s.ID, s)
		}
	}
}

func TestTLSRevisionRequiresAndDistributesTheCA(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("wiiu", "stable", 8, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Trust) != 1 {
		t.Fatalf("trust entries: %+v", b.Trust)
	}
	trust := b.Trust[0]
	if trust.ID != "openpak-ca-2" || trust.Artifact != TrustArtifactPath || trust.Scope != "redirected-services" {
		t.Fatalf("trust entry: %+v", trust)
	}
	if !hasString(b.RequiredCapabilities, CapTLSScopedCA) {
		t.Fatalf("TLS-enabled bundle lacks %s: %v", CapTLSScopedCA, b.RequiredCapabilities)
	}
	for _, s := range b.Services {
		if strings.HasPrefix(s.URL, "https://") && s.Trust != "openpak-ca-2" {
			t.Errorf("https service %q does not pin the CA: %+v", s.ID, s)
		}
	}
	// The same revision without a CA must not even build with https services
	// present: POL-008 completeness fails the generation, not the client.
	if _, err := Build(fixtureInputs(t, fixtureOptions{noCA: true}), EnvProduction); err == nil || !strings.Contains(err.Error(), "trust") {
		t.Fatalf("a CA-less revision with https services built: %v", err)
	}
}

func TestBuildFailsWithoutACeilingRevision(t *testing.T) {
	in := fixtureInputs(t, fixtureOptions{})
	delete(in.CeilingRevisions, "switch")
	if _, err := Build(in, EnvProduction); err == nil {
		t.Fatal("generation succeeded with an undefined ceiling")
	}
}

func TestBuildRejectsTheBootstrapHostAsARule(t *testing.T) {
	m, err := netprofileLoadForBootstrapTest()
	if err != nil {
		t.Fatal(err)
	}
	in := Inputs{
		PolicyID:         "openpak-production",
		Map:              m,
		CeilingRevisions: map[string]string{"switch": fixtureCeiling},
	}
	if _, err := Build(in, EnvProduction); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("bootstrap host was claimable: %v", err)
	}
}

// TestProjectionOfUnknownPlatformFails pins the difference between an empty
// bundle and a rejected question: a platform the revision does not carry is
// an error, never an empty document.
func TestProjectionOfUnknownPlatformFails(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	if _, err := c.Project("ds", "stable", 1, fixtureIssuedAt); err == nil {
		t.Fatal("projected a platform the revision does not carry")
	}
	if _, err := c.Project("switch", "stable", 0, fixtureIssuedAt); err == nil {
		t.Fatal("sequence 0 projected")
	}
}
