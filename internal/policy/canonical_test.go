package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

// The fixture map mirrors the shipped platforms.yml for the platforms the
// drift cases live on: the Battle.net ceiling, the Among Us matchmakers, the
// Photon name server, the second NAT responder and the connectivity-test
// `never`. Wii U carries the URL services Cemu consumes.
const fixtureMapYAML = `
public_base_url: https://openpak.org
server:
  address: 145.241.199.19
  https_port: 443
  http_port: 80
recheck_after: 21600
platforms:
  switch:
    families: [".nintendo.net", ".nintendo.com", ".battle.net"]
    exact:
      - "matchmaker.among.us"
      - "matchmaker-eu.among.us"
      - "matchmaker-as.among.us"
      - "ns.photonengine.io"
    overrides:
      "nncs2-lp1.n.n.srv.nintendo.net": "145.241.228.207"
    never: ["conntest.nintendowifi.net"]
    services: {}
  wiiu:
    families: [".nintendo.net", ".nintendowifi.net", ".openpak.org"]
    never: ["conntest.nintendowifi.net"]
    services:
      act: "https://account.openpak.org"
      ecs: "https://ecs.wup.shop.openpak.org/ecs/services/ECommerceSOAP"
      ccs: "http://ccs.cdn.wup.shop.openpak.org/ccs/download"
  3ds:
    families: [".nintendo.net", ".nintendowifi.net", ".openpak.org"]
    never: ["conntest.nintendowifi.net"]
    services: {}
`

const fixtureCeiling = "unified-1"

// fixtureMeta carries the reviewed transport declarations POL-002 asks for:
// the NAT responder's probe ports and the Photon name server's UDP role.
var fixtureMeta = map[string]map[string]TransportSpec{
	"switch": {
		"nncs2-lp1.n.n.srv.nintendo.net": {Transport: TransportUDP, Ports: []int{10025, 10125}},
		"ns.photonengine.io":             {Transport: TransportUDP, Ports: []int{5055}},
	},
}

var fixtureIssuedAt = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

type fixtureOptions struct {
	policyID string
	// noCA builds the revision without a trust anchor. POL-008 completeness
	// means that revision cannot carry https services: the build must fail.
	noCA bool
}

func fixtureInputs(t *testing.T, opts fixtureOptions) Inputs {
	t.Helper()
	m, err := netprofile.LoadMap([]byte(fixtureMapYAML))
	if err != nil {
		t.Fatalf("fixture map does not load: %v", err)
	}
	policyID := opts.policyID
	if policyID == "" {
		policyID = "openpak-production"
	}
	in := Inputs{
		PolicyID:         policyID,
		Map:              m,
		CeilingRevisions: map[string]string{"switch": fixtureCeiling, "wiiu": fixtureCeiling, "3ds": fixtureCeiling},
		TransportMeta:    fixtureMeta,
	}
	if !opts.noCA {
		in.CA = &CA{ID: "openpak-ca-2", PEM: []byte("fixture pem")}
	}
	return in
}

func fixtureCanonical(t *testing.T, opts fixtureOptions) *Canonical {
	t.Helper()
	c, err := Build(fixtureInputs(t, opts), EnvProduction)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return c
}

func findRule(t *testing.T, rules []Rule, id string) *Rule {
	t.Helper()
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	t.Fatalf("no rule %q in %d rules", id, len(rules))
	return nil
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// netprofileLoadForBootstrapTest loads a map that claims the distribution
// apex itself: the one name POL-005 keeps out of the forwarding path.
func netprofileLoadForBootstrapTest() (*netprofile.PlatformMap, error) {
	return netprofile.LoadMap([]byte(`
public_base_url: https://openpak.org
server:
  address: 145.241.199.19
  https_port: 443
  http_port: 80
recheck_after: 21600
platforms:
  switch:
    families: [".nintendo.net"]
    exact: ["openpak.org"]
    never: []
    services: {}
`))
}
