package netprofile

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestService(t *testing.T, traefik, platformMap string, ca CAFetcher) *Service {
	t.Helper()
	opts := Options{TraefikFile: traefik, CA: ca, Log: quietLogger()}
	if platformMap != "" {
		opts.PlatformMap = platformMap
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// CAFetcher mirrors the signature of the adapter's CA call, so tests need no
// adapter and the wire-up in main stays a plain method value.
type CAFetcher = func(context.Context) ([]byte, error)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustProfile(t *testing.T, s *Service, platform string) *Profile {
	t.Helper()
	p, _, err := s.Profile(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The live snapshot plus the shipped map: what a Switch client is told.
func TestSwitchProfileIsGeneratedFromTheLiveRouting(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p := mustProfile(t, s, "switch")

	if p.Platform != "switch" || p.Version != 1 || p.GeneratedAt == 0 {
		t.Fatalf("identity fields: %+v", p)
	}
	if p.Server.Address == "" || p.Server.HTTPSPort != 443 || p.Server.HTTPPort != 80 {
		t.Fatalf("server block: %+v", p.Server)
	}
	// Everything a Switch reaches, Nintendo's and the third parties both, and
	// still no DS-era family: a Wii's .gamespy.com is not a name this console
	// ever resolves.
	want := ".among.us,.battle.net,.demonware.net,.ea.com,.epicgames.dev,.exitgames.com," +
		".klei.com,.kleientertainment.com,.live.com,.microsoft.com,.mojang.com,.nintendo.com," +
		".nintendo.net,.nintendowifi.net,.photonengine.io,.xboxlive.com"
	if strings.Join(p.Redirect.Suffixes, ",") != want {
		t.Fatalf("suffixes: %v", p.Redirect.Suffixes)
	}
	if contains(p.Redirect.Suffixes, ".gamespy.com") || contains(p.Redirect.Suffixes, ".openpak.org") {
		t.Errorf("switch is told about a family it never resolves: %v", p.Redirect.Suffixes)
	}
	// A family covers every name this platform claims now, so exact is empty --
	// the matchmakers moved under .among.us rather than being listed one by one.
	if len(p.Redirect.Exact) != 0 {
		t.Errorf("exact repeats names a family already covers: %v", p.Redirect.Exact)
	}
	for _, name := range []string{"matchmaker.among.us", "matchmaker-eu.among.us", "matchmaker-as.among.us"} {
		if !coveredByAny(name, p.Redirect.Suffixes) {
			t.Errorf("no family covers %s: %v", name, p.Redirect.Suffixes)
		}
	}
	// The connection test reaches OpenPak too -- OpenPak answers that probe as a service. The
	// only names a Switch still sends to the real internet are Klei's content delivery, which
	// carries no account and serves the game its own data.
	if strings.Join(p.Redirect.Never, ",") != "cdn-galette.klei.com,galette.klei.com" {
		t.Errorf("never holds %v", p.Redirect.Never)
	}
	if !coveredByAny("conntest.nintendowifi.net", p.Redirect.Suffixes) {
		t.Errorf("conntest is not covered: %v", p.Redirect.Suffixes)
	}
	if len(p.Services) != 0 {
		t.Errorf("switch carries services: %v", p.Services)
	}
	if p.RecheckAfter != 21600 {
		t.Errorf("recheck_after: %d", p.RecheckAfter)
	}
	// And a title routed this morning is in the profile without any client
	// release: the NPLN names come from the routing, not from this repo.
	if !liveRoutingCovers(t, s, "switch") {
		t.Error("a routed NPLN name is not claimed by the switch profile's families")
	}
}

func liveRoutingCovers(t *testing.T, s *Service, platform string) bool {
	t.Helper()
	routes, err := LoadRoutes("testdata/traefik.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range routes.Names {
		if strings.HasSuffix(n, ".npln.srv.nintendo.net") {
			if !coveredByAny(n, mustProfile(t, s, platform).Redirect.Suffixes) {
				return false
			}
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The union narrows the same way every profile does: families unite, exact
// carries what no family covers, and never keeps only what every profile
// agrees on.
func TestUnionProfile(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p := mustProfile(t, s, PlatformAll)

	if p.Platform != "all" {
		t.Fatalf("platform: %s", p.Platform)
	}
	for _, f := range Universe {
		if !contains(p.Redirect.Suffixes, f) {
			t.Errorf("union lacks family %s: %v", f, p.Redirect.Suffixes)
		}
	}
	if !coveredByAny("matchmaker.among.us", p.Redirect.Suffixes) {
		t.Errorf("union covers no matchmaker: %v", p.Redirect.Suffixes)
	}
	// never keeps only what every profile agrees on, and the Switch no longer
	// agrees: nothing it resolves is left to the real internet.
	if len(p.Redirect.Never) != 0 {
		t.Errorf("union never: %v", p.Redirect.Never)
	}
	if len(p.Services) != 0 {
		t.Errorf("union carries services: %v", p.Services)
	}
}

// A Wii must not be told about a Switch's names.
func TestWiiProfileIsNarrowedAwayFromTheSwitch(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p := mustProfile(t, s, "wii")

	for _, f := range p.Redirect.Suffixes {
		if f == ".nintendo.net" || f == ".nintendo.com" {
			t.Errorf("wii carries the Switch family %s", f)
		}
	}
	for _, e := range p.Redirect.Exact {
		if strings.Contains(e, "npln") || strings.Contains(e, "baas") {
			t.Errorf("wii carries the Switch name %s", e)
		}
	}
	if !contains(p.Redirect.Suffixes, ".gamespy.com") {
		t.Errorf("wii lacks gamespy: %v", p.Redirect.Suffixes)
	}
}

// Cemu addresses URLs: the ten services come through for wiiu, and every one
// lands inside a family that profile already redirects.
func TestWiiUServices(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p := mustProfile(t, s, "wiiu")
	ids := map[string]string{}
	for _, svc := range p.Services {
		ids[svc.ID] = svc.URL
	}
	for _, id := range []string{"act", "ecs", "nus", "ias", "ccs", "ccsu", "idbe", "boss", "tagaya", "olv"} {
		if ids[id] == "" {
			t.Errorf("wiiu service %s missing", id)
		}
	}
	if ids["ecs"] != "https://ecs.wup.shop.openpak.org/ecs/services/ECommerceSOAP" {
		t.Errorf("ecs: %s", ids["ecs"])
	}
}

func TestUnknownPlatform(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	if _, _, err := s.Profile(context.Background(), "vita"); !errors.Is(err, ErrUnknownPlatform) {
		t.Fatalf("want ErrUnknownPlatform, got %v", err)
	}
}

// ETag over the same content is stable, version does not move, and
// generated_at freezes: a client that re-validates gets the same 304 forever
// until the content really changes.
func TestETagAndVersionAreStableWhileContentIsUnchanged(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p1, etag1, err := s.Profile(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p2, etag2, err := s.Profile(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	if etag1 != etag2 || p1.Version != p2.Version || p1.GeneratedAt != p2.GeneratedAt {
		t.Fatalf("unchanged content moved: %d/%d %s != %s", p1.Version, p2.Version, etag1, etag2)
	}
}

// Changed content bumps the version and moves the ETag. And the shape of the
// change matters: a newly routed name that a family already covers must not
// change the profile at all — that is the promise the whole PRD exists for.
func TestAddingACoveredTitleChangesNothing(t *testing.T) {
	dir := t.TempDir()
	traefik := filepath.Join(dir, "dynamic.yml")
	if err := os.WriteFile(traefik, []byte(minimalRouting), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestService(t, traefik, "", nil)
	p1, etag1, body1, err := profileBytes(t, s, "switch")
	if err != nil {
		t.Fatal(err)
	}
	// A second title lands in Traefik; every Switch client already redirects
	// its family, so no client release and no profile change is needed.
	grown := strings.Replace(minimalRouting, "HostSNI(`t-9f607adf-lp1.lp1.t.npln.srv.nintendo.net`)",
		"HostSNI(`t-9f607adf-lp1.lp1.t.npln.srv.nintendo.net`) || HostSNI(`t-abcdef01-lp1.lp1.t.npln.srv.nintendo.net`)", 1)
	if err := os.WriteFile(traefik, []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	s.expire()
	p2, etag2, body2, err := profileBytes(t, s, "switch")
	if err != nil {
		t.Fatal(err)
	}
	if etag1 != etag2 || p1.Version != p2.Version || string(body1) != string(body2) {
		t.Fatal("a covered title changed the profile; clients would refetch for nothing")
	}
}

// Content that really changed — the CA was rotated — bumps the version,
// moves the ETag, and publishes the new digest.
func TestVersionBumpsWhenContentChanges(t *testing.T) {
	dir := t.TempDir()
	traefik := filepath.Join(dir, "dynamic.yml")
	if err := os.WriteFile(traefik, []byte(minimalRouting), 0o644); err != nil {
		t.Fatal(err)
	}
	caPEM := testCertificate(t)
	opts := Options{
		TraefikFile: traefik,
		CA:          func(context.Context) ([]byte, error) { return caPEM, nil },
		Log:         quietLogger(),
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	p1, etag1, _, err := profileBytes(t, s, "switch")
	if err != nil {
		t.Fatal(err)
	}
	caPEM = testCertificate(t)
	s.expire()
	s.expireCA()
	p2, etag2, _, err := profileBytes(t, s, "switch")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Version != p1.Version+1 {
		t.Fatalf("version did not bump: %d -> %d", p1.Version, p2.Version)
	}
	if etag1 == etag2 {
		t.Fatal("etag survived a CA rotation")
	}
	if p2.CA.SHA256 == p1.CA.SHA256 {
		t.Fatal("ca digest survived a CA rotation")
	}
}

func profileBytes(t *testing.T, s *Service, platform string) (*Profile, string, []byte, error) {
	t.Helper()
	p, etag, err := s.Profile(context.Background(), platform)
	if err != nil {
		return nil, "", nil, err
	}
	body, _, err := s.Bytes(context.Background(), platform)
	return p, etag, body, err
}

// Stale beats broken: a routing file that goes away leaves the last good
// profile serving.
func TestUnreadableTraefikFileKeepsTheLastGoodProfile(t *testing.T) {
	dir := t.TempDir()
	traefik := filepath.Join(dir, "dynamic.yml")
	if err := os.WriteFile(traefik, []byte(minimalRouting), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestService(t, traefik, "", nil)
	p1, e1, err := s.Profile(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(traefik); err != nil {
		t.Fatal(err)
	}
	s.expire()
	p2, e2, err := s.Profile(context.Background(), "switch")
	if err != nil {
		t.Fatalf("a missing routing file must serve the last good profile, got %v", err)
	}
	if string(mustBytes(t, s, "switch")) == "" || e1 != e2 || p1.Version != p2.Version {
		t.Fatal("last good profile was not kept")
	}
}

func mustBytes(t *testing.T, s *Service, platform string) []byte {
	t.Helper()
	b, _, err := s.Bytes(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The CA block is only present when a CA source is configured, and the
// digest it publishes is the digest of the bytes the ca.pem endpoint serves.
func TestCABlockMatchesTheServedPEM(t *testing.T) {
	pemBytes := testCertificate(t)
	s := newTestService(t, "testdata/traefik.yml", "", func(context.Context) ([]byte, error) { return pemBytes, nil })

	p := mustProfile(t, s, "switch")
	if p.CA == nil {
		t.Fatal("no ca block")
	}
	if p.CA.URL != "https://openpak.org/api/v1/network/ca.pem" {
		t.Errorf("ca url: %s", p.CA.URL)
	}
	body, info, err := s.CA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(body)
	if info.SHA256 != sum || p.CA.SHA256 != sum {
		t.Errorf("digest mismatch: profile %s ca endpoint %s", p.CA.SHA256, info.SHA256)
	}
	if info.NotAfter == 0 || info.NotAfter != p.CA.NotAfter {
		t.Errorf("not_after mismatch: %d vs %d", p.CA.NotAfter, info.NotAfter)
	}
}

func TestNoCASourceOmitsTheBlock(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	if p := mustProfile(t, s, "switch"); p.CA != nil {
		t.Fatal("ca block present without a CA source")
	}
	if _, _, err := s.CA(context.Background()); err == nil {
		t.Fatal("CA answered without a source")
	}
}

// A CA fetch failure keeps the last good CA serving.
func TestCAFetchFailureKeepsTheLastGoodCA(t *testing.T) {
	pemBytes := testCertificate(t)
	calls := 0
	s := newTestService(t, "testdata/traefik.yml", "", func(context.Context) ([]byte, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("adapter down")
		}
		return pemBytes, nil
	})
	if _, _, err := s.CA(context.Background()); err != nil {
		t.Fatal(err)
	}
	body2, _, err := s.CA(context.Background())
	if err != nil {
		t.Fatalf("stale CA not kept: %v", err)
	}
	if sha256Hex(body2) != sha256Hex(pemBytes) {
		t.Fatal("stale CA differs")
	}
}

// Map validation: the security rules as load errors.
func TestMapValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"family outside the universe", `
public_base_url: https://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch: {families: [".example.com"]}
`},
		{"invented platform", `
public_base_url: https://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  vita: {families: [".nintendo.net"]}
`},
		{"non-IP server address", `
public_base_url: https://openpak.org
server: {address: "openpak.example", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch: {families: [".nintendo.net"]}
`},
		{"service outside its own families", `
public_base_url: https://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch:
    families: [".nintendo.net"]
    services: {act: "https://account.example.com"}
`},
		{"wildcard exact name", `
public_base_url: https://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch:
    families: [".nintendo.net"]
    exact: ["*.nintendo.net"]
`},
		{"plain-http public base", `
public_base_url: http://openpak.org
server: {address: "127.0.0.1", https_port: 443, http_port: 80}
recheck_after: 60
platforms:
  switch: {families: [".nintendo.net"]}
`},
	}
	for _, tc := range cases {
		if _, err := LoadMap([]byte(tc.yaml)); err == nil {
			t.Errorf("%s: map accepted", tc.name)
		}
	}
}

// The shipped map itself loads, so the package cannot build with a broken
// default. Its families are checked against the signed ceiling in
// drift_test.go, not against the compiled Universe.
func TestEmbeddedMapLoads(t *testing.T) {
	if _, err := parseMap(embeddedMap); err != nil {
		t.Fatal(err)
	}
}

func TestProfileJSONRoundTrip(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	body, _, err := s.Bytes(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Version      int      `json:"version"`
		Platform     string   `json:"platform"`
		Redirect     struct{} `json:"redirect"`
		RecheckAfter int      `json:"recheck_after"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Platform != "switch" || probe.RecheckAfter != 21600 {
		t.Fatalf("json shape: %+v", probe)
	}
}

func testCertificate(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "OpenPak Console CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// One TCP router and one HTTP router: the smallest routing that exercises
// every generation path.
const minimalRouting = `
tcp:
  routers:
    stardew:
      entryPoints: [console]
      rule: "HostSNI(` + "`t-9f607adf-lp1.lp1.t.npln.srv.nintendo.net`)" + `"
      service: stardew
      tls: { passthrough: true }
  services:
    stardew:
      loadBalancer:
        servers: [{ address: "openpak-stardew:21010" }]
http:
  routers:
    matchmaker:
      entryPoints: [console]
      rule: "Host(` + "`matchmaker.among.us`)" + `"
      service: among-us
      tls: {}
  services:
    among-us:
      loadBalancer:
        servers: [{ url: "http://openpak-among-us:22600" }]
`

func TestSwitchOverridesCarrySecondNatAddress(t *testing.T) {
	s := newTestService(t, "testdata/traefik.yml", "", nil)
	p := mustProfile(t, s, "switch")
	got := p.Redirect.Overrides["nncs2-lp1.n.n.srv.nintendo.net"]
	if got == "" || got == p.Server.Address {
		t.Fatalf("nncs2 must have an address of its own, not the server's: %q", got)
	}
}
