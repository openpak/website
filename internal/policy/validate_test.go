package policy

import (
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

func minimalValidBundle() *Bundle {
	return &Bundle{
		SchemaVersion:         SchemaVersion,
		PolicyID:              "openpak-production",
		Sequence:              1,
		Platform:              "switch",
		Channel:               "stable",
		IssuedAt:              fixtureIssuedAt,
		CanonicalPolicySHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		RequiredCapabilities:  []string{CapDNSSuffix},
		CeilingRevision:       fixtureCeiling,
		DefaultDestination:    Destination{IPv4: "145.241.199.19", HTTPSPort: 443, HTTPPort: 80},
		Rules: []Rule{
			{ID: "suffix.nintendo.net", Match: Match{Suffix: strptr(".nintendo.net"), IncludeApex: true}, Action: ActionRedirect},
		},
		Services:  []Service{},
		Trust:     []Trust{},
		PatchSets: []PatchSet{},
	}
}

func TestMinimalBundleValidates(t *testing.T) {
	if err := minimalValidBundle().Validate(EnvProduction); err != nil {
		t.Fatalf("minimal bundle rejected: %v", err)
	}
}

func TestDestinationAddressClasses(t *testing.T) {
	cases := []struct {
		addr    string
		env     Environment
		wantErr bool
		reason  string
	}{
		{"145.241.199.19", EnvProduction, false, "public anycast"},
		{"145.241.228.207", EnvProduction, false, "public unicast"},
		{"2001:0db8:0000:0000:0000:0000:0000:0010", EnvProduction, false, "global v6, canonical form"},
		{"127.0.0.1", EnvProduction, true, "loopback"},
		{"10.0.0.5", EnvProduction, true, "private"},
		{"192.168.1.1", EnvProduction, true, "private"},
		{"172.16.0.9", EnvProduction, true, "private"},
		{"169.254.1.1", EnvProduction, true, "link-local"},
		{"224.0.0.1", EnvProduction, true, "multicast"},
		{"255.255.255.255", EnvProduction, true, "broadcast"},
		{"0.0.0.0", EnvProduction, true, "unspecified"},
		{"::ffff:145.241.199.19", EnvProduction, true, "IPv4-mapped"},
		{"2001:db8::10", EnvProduction, true, "non-canonical v6 spelling"},
		{"::1", EnvProduction, true, "loopback v6, non-canonical spelling"},
		{"fe80:0000:0000:0000:0000:0000:0000:0001", EnvProduction, true, "link-local v6"},
		{"fd00:0000:0000:0000:0000:0000:0000:0001", EnvProduction, true, "unique local v6"},
		{"192.168.1.1", EnvNonproduction, false, "private deployment"},
		{"127.0.0.1", EnvNonproduction, false, "local development"},
		{"0.0.0.0", EnvNonproduction, true, "unspecified stays wrong everywhere"},
	}
	for _, tc := range cases {
		err := validateAddress("test", tc.addr, tc.env)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s (%s) in %s: err=%v, wantErr=%v", tc.addr, tc.reason, tc.env, err, tc.wantErr)
		}
	}
}

func TestBundleValidationRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(b *Bundle)
		wantErr string
	}{
		{
			name:    "duplicate rule id",
			mutate:  func(b *Bundle) { b.Rules = append(b.Rules, b.Rules[0]) },
			wantErr: "twice",
		},
		{
			name: "conflicting equal-priority rules",
			mutate: func(b *Bundle) {
				b.Rules = append(b.Rules,
					Rule{ID: "exact.a.b", Match: Match{Exact: strptr("a.b")}, Action: ActionRedirect},
					Rule{ID: "exact.a.b.passthrough", Match: Match{Exact: strptr("a.b")}, Action: ActionPassthrough},
				)
			},
			wantErr: "conflicts",
		},
		{
			name: "same name on two transports is not a conflict",
			mutate: func(b *Bundle) {
				b.Rules = append(b.Rules,
					Rule{ID: "exact.a.b", Match: Match{Exact: strptr("a.b")}, Action: ActionRedirect},
					Rule{ID: "exact.a.b.udp", Match: Match{Exact: strptr("a.b")}, Action: ActionRedirect, Transport: TransportUDP},
				)
				b.RequiredCapabilities = append(b.RequiredCapabilities, CapDNSExact)
			},
			wantErr: "",
		},
		{
			name: "match matches nothing",
			mutate: func(b *Bundle) {
				b.Rules[0].Match.Suffix = nil
			},
			wantErr: "matches nothing",
		},
		{
			name: "both exact and suffix",
			mutate: func(b *Bundle) {
				b.Rules[0].Match.Exact = strptr("a.b")
			},
			wantErr: "both exact and suffix",
		},
		{
			name: "uppercase hostname",
			mutate: func(b *Bundle) {
				b.Rules[0].Match.Suffix = strptr(".Nintendo.net")
			},
			wantErr: "leading-dot family",
		},
		{
			name: "suffix without leading dot",
			mutate: func(b *Bundle) {
				b.Rules[0].Match.Suffix = strptr("nintendo.net")
			},
			wantErr: "leading-dot family",
		},
		{
			name: "unknown action",
			mutate: func(b *Bundle) {
				b.Rules[0].Action = "drop"
			},
			wantErr: "unknown action",
		},
		{
			name: "passthrough with destination",
			mutate: func(b *Bundle) {
				b.Rules[0].Action = ActionPassthrough
				b.Rules[0].Destination = &RedirectTarget{IPv4: "145.241.199.19"}
			},
			wantErr: "redirect-only fields",
		},
		{
			name: "private rule destination",
			mutate: func(b *Bundle) {
				b.Rules[0].Destination = &RedirectTarget{IPv4: "10.1.2.3"}
			},
			wantErr: "private",
		},
		{
			name: "dangling tls trust reference",
			mutate: func(b *Bundle) {
				b.Rules[0].TLS = &TLSRef{Trust: "nope"}
			},
			wantErr: "trust",
		},
		{
			name: "dangling service reference",
			mutate: func(b *Bundle) {
				b.Rules[0].ServiceID = "nope"
			},
			wantErr: "service",
		},
		{
			name: "https service without trust",
			mutate: func(b *Bundle) {
				b.Services = []Service{{ID: "act", URL: "https://account.openpak.org"}}
				b.RequiredCapabilities = []string{CapDNSSuffix, CapServiceURL}
			},
			wantErr: "trust",
		},
		{
			name: "capability declares nothing the content needs",
			mutate: func(b *Bundle) {
				b.Rules = nil
			},
			wantErr: "capability",
		},
		{
			name: "rules over the limit",
			mutate: func(b *Bundle) {
				b.Rules = make([]Rule, MaxRules+1)
				for i := range b.Rules {
					name := "a" + strings.Repeat("0", 40) + ".example" + strings.Repeat("x", 8) + ".b" + strings.Repeat("y", 8) + ".c" + strings.Repeat("z", 8) + ".d" + strings.Repeat("w", 8) + ".e" + strings.Repeat("v", 8) + ".f" + strings.Repeat("u", 8) + ".g" + strings.Repeat("s", 8) + ".h" + strings.Repeat("q", 8) + ".i" + strings.Repeat("p", 8) + ".j" + strings.Repeat("o", 8) + ".k" + strings.Repeat("n", 8) + ".l" + strings.Repeat("m", 8) + string(rune('a'+i%26)) + ".net"
					b.Rules[i] = Rule{ID: "exact." + name, Match: Match{Exact: &name}, Action: ActionRedirect}
				}
			},
			wantErr: "over the",
		},
		{
			name: "unknown platform",
			mutate: func(b *Bundle) {
				b.Platform = "vita"
			},
			wantErr: "platform",
		},
		{
			name: "bad digest",
			mutate: func(b *Bundle) {
				b.CanonicalPolicySHA256 = "NOT-A-DIGEST"
			},
			wantErr: "sha-256",
		},
		{
			name: "sequence zero",
			mutate: func(b *Bundle) {
				b.Sequence = 0
			},
			wantErr: "sequence",
		},
		{
			name: "bootstrap host claimed",
			mutate: func(b *Bundle) {
				b.Rules = append(b.Rules, Rule{ID: "exact.openpak.org", Match: Match{Exact: strptr("openpak.org")}, Action: ActionRedirect})
			},
			wantErr: "bootstrap",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := minimalValidBundle()
			tc.mutate(b)
			err := b.Validate(EnvProduction)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected rejection, got success")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestPatchDependencyRejections(t *testing.T) {
	b := minimalValidBundle()
	b.PatchSets = []PatchSet{
		{ID: "patch-a", Artifact: "patches/a.zip", MinFirmware: "18.1.0", DependsOn: []string{"patch-b"}},
		{ID: "patch-b", Artifact: "patches/b.zip", MinFirmware: "18.1.0", DependsOn: []string{"patch-a"}},
	}
	if err := b.Validate(EnvProduction); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("dependency cycle accepted: %v", err)
	}

	b.PatchSets = []PatchSet{
		{ID: "patch-a", Artifact: "patches/a.zip", MinFirmware: "18.1.0", DependsOn: []string{"patch-missing"}},
	}
	if err := b.Validate(EnvProduction); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("undeclared dependency accepted: %v", err)
	}

	b.PatchSets = []PatchSet{
		{ID: "patch-a", Artifact: "patches/a.zip", MinFirmware: "18.1.0"},
	}
	if err := b.Validate(EnvProduction); err != nil {
		t.Fatalf("resolvable dependency-free patches rejected: %v", err)
	}
}
