package policy

import (
	"testing"
)

func TestEmbeddedSchemasCompile(t *testing.T) {
	if _, _, err := CompileSchemas(); err != nil {
		t.Fatalf("embedded schemas do not compile: %v", err)
	}
}

// The generated documents are the schema's examples: whatever a certified
// client will actually fetch must satisfy the published contract.
func TestGeneratedDocumentsSatisfyTheSchemas(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	for _, platform := range []string{"switch", "wiiu", "3ds"} {
		b, err := c.Project(platform, "stable", 42, fixtureIssuedAt)
		if err != nil {
			t.Fatal(err)
		}
		data, err := MarshalBundle(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePolicyAgainstSchema(data); err != nil {
			t.Errorf("%s bundle fails the published schema: %v", platform, err)
		}
		got, err := DecodeBundle(data)
		if err != nil {
			t.Errorf("%s bundle does not round trip: %v", platform, err)
		} else if err := got.Validate(EnvProduction); err != nil {
			t.Errorf("%s bundle fails Go-side validation: %v", platform, err)
		}
	}
	_, payloads := fixturePublication(t)
	b, _ := fixturePublication(t)
	m, err := BuildManifest(b.PolicyID, b.Platform, b.Channel, b.Sequence, b.RequiredCapabilities, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestAgainstSchema(mustJSON(t, m)); err != nil {
		t.Errorf("manifest fails the published schema: %v", err)
	}
}

// The example payload from PRD 01, with placeholders resolved to the real
// production addresses, must be a valid bundle.
func TestPRDExampleSatisfiesTheSchema(t *testing.T) {
	example := `{
	  "schema_version": 2,
	  "policy_id": "openpak-production",
	  "sequence": 42,
	  "platform": "switch",
	  "channel": "stable",
	  "issued_at": "2026-09-14T12:00:00Z",
	  "canonical_policy_sha256": "6b1f57a4b1cbb2d5133a332f9cf9ec934c0a41f572b3b7cb26e71e57d0091bd4",
	  "required_capabilities": ["dns.exact.v1", "dns.suffix.v1", "dns.override.v1", "tls.scoped-ca.v1"],
	  "ceiling_revision": "switch-2",
	  "default_destination": {"ipv4": "145.241.199.19", "https_port": 443, "http_port": 80},
	  "rules": [
	    {"id": "nat-secondary", "match": {"exact": "nncs2-lp1.n.n.srv.nintendo.net"}, "action": "redirect", "destination": {"ipv4": "145.241.228.207"}, "transport": "udp", "ports": [10025, 10125]},
	    {"id": "connectivity-test", "match": {"exact": "conntest.nintendowifi.net"}, "action": "passthrough"}
	  ],
	  "services": [],
	  "trust": [{"id": "openpak-ca-2", "artifact": "trust/ca.pem", "scope": "redirected-services"}],
	  "patch_sets": []
	}`
	if err := ValidatePolicyAgainstSchema([]byte(example)); err != nil {
		t.Fatalf("the PRD's own example fails the schema: %v", err)
	}
}

func TestSchemaRejectsTamperedDocuments(t *testing.T) {
	cases := []struct {
		name string
		bad  string
	}{
		{
			name: "unknown action",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"switch","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"` + hex64 + `","required_capabilities":["dns.exact.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"exact":"a.b"},"action":"drop"}],"services":[],"trust":[],"patch_sets":[]}`,
		},
		{
			name: "suffix without leading dot",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"switch","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"` + hex64 + `","required_capabilities":["dns.suffix.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"suffix":"nintendo.net"},"action":"redirect"}],"services":[],"trust":[],"patch_sets":[]}`,
		},
		{
			name: "uppercase digest",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"switch","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"A1F57A4B1CBB2D5133A332F9CF9EC934C0A41F572B3B7CB26E71E57D0091BD4","required_capabilities":["dns.exact.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"exact":"a.b"},"action":"redirect"}],"services":[],"trust":[],"patch_sets":[]}`,
		},
		{
			name: "missing required field",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"switch","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"` + hex64 + `","required_capabilities":["dns.exact.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"exact":"a.b"},"action":"redirect"}],"services":[],"trust":[]}`,
		},
		{
			name: "unknown platform",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"vita","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"` + hex64 + `","required_capabilities":["dns.exact.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"exact":"a.b"},"action":"redirect"}],"services":[],"trust":[],"patch_sets":[]}`,
		},
		{
			name: "passthrough with a destination",
			bad:  `{"schema_version":2,"policy_id":"p","sequence":1,"platform":"switch","channel":"stable","issued_at":"2026-09-14T12:00:00Z","canonical_policy_sha256":"` + hex64 + `","required_capabilities":["dns.exact.v1"],"ceiling_revision":"switch-2","default_destination":{"ipv4":"145.241.199.19","https_port":443,"http_port":80},"rules":[{"id":"r","match":{"exact":"a.b"},"action":"passthrough","destination":{"ipv4":"145.241.199.19"}}],"services":[],"trust":[],"patch_sets":[]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePolicyAgainstSchema([]byte(tc.bad)); err == nil {
				t.Fatal("schema accepted a tampered bundle")
			}
		})
	}
}

const hex64 = "6b1f57a4b1cbb2d5133a332f9cf9ec934c0a41f572b3b7cb26e71e57d0091bd4"
