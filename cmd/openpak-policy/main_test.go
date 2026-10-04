package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/registry"
)

// The same authored shape the registry's tests use: the map stays the only
// host input, and the tool must refuse to run without it.
const testMapYAML = `
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
      ccs: "http://ccs.cdn.wup.shop.openpak.org/ccs/download"
`

const testTransportsJSON = `{
  "switch": {
    "nncs2-lp1.n.n.srv.nintendo.net": {"transport": "udp", "ports": [10025, 10125]},
    "ns.photonengine.io": {"transport": "udp", "ports": [5055]}
  }
}`

// The tool parses the CA's expiry out of the PEM, so the fixture needs a
// real certificate, not placeholder bytes.
func writeTestCA(t *testing.T, dir string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "openpak operator test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestInputs(t *testing.T) (mapPath, transportsPath, caPath string) {
	t.Helper()
	dir := t.TempDir()
	mapPath = filepath.Join(dir, "platforms.yml")
	if err := os.WriteFile(mapPath, []byte(testMapYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	transportsPath = filepath.Join(dir, "transports.json")
	if err := os.WriteFile(transportsPath, []byte(testTransportsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return mapPath, transportsPath, writeTestCA(t, dir)
}

// newTestService swaps the service constructor for an in-memory registry
// with fresh signing keys, so the real command logic runs end to end.
func newTestService(t *testing.T) *registry.Service {
	t.Helper()
	store := registry.NewMemStore(nil)
	blobs, err := registry.NewDirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootKey, err := registry.GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	onlineKey, err := registry.GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := registry.NewPublisher(rootKey, onlineKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &registry.Service{Store: store, Blobs: blobs, Publisher: publisher, PolicyID: "openpak-production"}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type cliResult struct {
	code   int
	stdout string
	stderr string
}

func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, func(string) string { return "" }, &stdout, &stderr)
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// The full operator journey: publish both projections of one canonical
// revision, prove the signed chain verifies, survive a refused publication,
// roll back, withdraw, and recover with a fresh sequence.
func TestPublishRollbackWithdrawJourney(t *testing.T) {
	svc := newTestService(t)
	newService = func(func(string) string) (*registry.Service, func(), error) {
		return svc, func() {}, nil
	}
	defer func() { newService = openService }()

	mapPath, transportsPath, caPath := writeTestInputs(t)
	publishArgs := []string{
		"publish", "--map", mapPath, "--transports", transportsPath, "--ca", caPath,
		"--ceiling", "switch=unified-1", "--ceiling", "wiiu=unified-1", "--channel", "stable",
	}

	res := runCLI(t, publishArgs...)
	if res.code != 0 {
		t.Fatalf("publish: code %d stderr %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "switch stable: sequence 1") || !strings.Contains(res.stdout, "wiiu stable: sequence 1") {
		t.Fatalf("publish output missing both projections: %s", res.stdout)
	}

	// The published switch bundle's whole verification chain — the exact
	// calls the fetch tool makes — verifies against the served bytes.
	ctx := context.Background()
	rev, err := svc.ResolveChannel(ctx, "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	fetched := map[string][]byte{}
	for _, name := range registry.MetadataNames {
		data, err := svc.Blobs.Get(ctx, rev.Metadata[name])
		if err != nil {
			t.Fatal(err)
		}
		fetched[name] = data
	}
	manifest, err := svc.Blobs.Get(ctx, rev.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.VerifyPublication(fetched["root.json"], fetched["targets.json"], fetched["snapshot.json"], fetched["timestamp.json"], manifest, time.Now()); err != nil {
		t.Fatalf("published chain does not verify: %v", err)
	}

	// A publication whose inputs refuse to build — a platform without an
	// approved ceiling — fails before anything is allocated or moved.
	res = runCLI(t, "publish", "--map", mapPath, "--ca", caPath, "--ceiling", "wiiu=unified-1")
	if res.code == 0 {
		t.Fatal("publishing without a ceiling revision succeeded")
	}
	after, err := svc.ResolveChannel(ctx, "switch", "stable")
	if err != nil || after.BundleSHA256 != rev.BundleSHA256 {
		t.Fatalf("the refused publication touched the channel: %+v err=%v", after, err)
	}

	// Rollback: a higher sequence, the same content, and the channel keeps
	// resolving.
	res = runCLI(t, "rollback", "--platform", "switch", "--channel", "stable")
	if res.code != 0 {
		t.Fatalf("rollback: code %d stderr %s", res.code, res.stderr)
	}
	// The same policy content at a higher sequence, which is what a rollback is
	// (PRD 06 UPD-007) -- never the old metadata served again. The bundle digest
	// therefore moves on purpose: the reissued bundle carries the new sequence and
	// issue time in its own bytes. Canonical content is the thing that must not.
	rb, err := svc.ResolveChannel(ctx, "switch", "stable")
	if err != nil || rb.CanonicalPolicySHA256 != rev.CanonicalPolicySHA256 || rb.Sequence != rev.Sequence+1 {
		t.Fatalf("channel after rollback: %+v err=%v (was sequence %d)", rb, err, rev.Sequence)
	}
	if rb.BundleSHA256 == rev.BundleSHA256 {
		t.Fatal("rollback reissued the old bundle bytes instead of signing the content afresh")
	}

	// Status reports the current resolution.
	res = runCLI(t, "status", "--platform", "switch", "--channel", "stable")
	if res.code != 0 {
		t.Fatalf("status: code %d stderr %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, fmt.Sprintf("sequence %d", rb.Sequence)) {
		t.Fatalf("status output missing the sequence: %s", res.stdout)
	}

	// Withdraw the live bytes: the channel stops resolving...
	res = runCLI(t, "withdraw", "--bundle-sha256", rb.BundleSHA256)
	if res.code != 0 {
		t.Fatalf("withdraw: code %d stderr %s", res.code, res.stderr)
	}
	res = runCLI(t, "status", "--platform", "switch", "--channel", "stable")
	if res.code == 0 || !strings.Contains(res.stderr, "withdrawn") {
		t.Fatalf("status after withdraw: code %d stderr %s", res.code, res.stderr)
	}

	// ...and the sanctioned recovery is a fresh publication, which lands
	// on a higher sequence than everything burned above.
	res = runCLI(t, publishArgs...)
	if res.code != 0 {
		t.Fatalf("recovery publish: code %d stderr %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "sequence 3") {
		t.Fatalf("recovery publish did not continue the sequence: %s", res.stdout)
	}
}

// Registration is the AUTH-M0 operator lifecycle: issue a hash, approve it
// through staging, and prove a build session opens and dies on suspension.
func TestRegisterAndSuspendLifecycle(t *testing.T) {
	svc := newTestService(t)
	newService = func(func(string) string) (*registry.Service, func(), error) {
		return svc, func() {}, nil
	}
	defer func() { newService = openService }()

	res := runCLI(t, "register",
		"--repository", "openpak/citron", "--kind", "emulator",
		"--platform", "switch", "--channel", "staging",
		"--workflow", ".github/workflows/release.yml",
		"--status", "staging-approved", "--environment", "nonproduction")
	if res.code != 0 {
		t.Fatalf("register: code %d stderr %s", res.code, res.stderr)
	}
	var clientHash, clientID string
	for _, line := range strings.Split(res.stdout, "\n") {
		if value, ok := strings.CutPrefix(line, "client hash (OPENPAK_CLIENT_HASH): "); ok {
			clientHash = value
		}
		if _, rest, ok := strings.Cut(line, "registered "); ok {
			clientID, _, _ = strings.Cut(rest, " ")
		}
	}
	if len(clientHash) != 64 || clientID == "" {
		t.Fatalf("register output missing hash/id: %s", res.stdout)
	}

	// The registration opens a build session for the approved workflow.
	ci := &registry.CIAssertion{
		Issuer:      "https://token.actions.githubusercontent.com",
		Repository:  "openpak/citron",
		Workflow:    ".github/workflows/release.yml",
		Commit:      "abc123",
		Environment: "nonproduction",
	}
	raw, _, err := svc.OpenBuildSession(context.Background(), clientHash, "switch", "staging", ci)
	if err != nil {
		t.Fatalf("build session refused for the registered client: %v", err)
	}
	if _, _, err := svc.AuthorizeSession(context.Background(), raw); err != nil {
		t.Fatalf("fresh session denied: %v", err)
	}

	// Suspending the client kills the session on its next request.
	res = runCLI(t, "client-status", "--client-id", clientID, "--status", "suspended")
	if res.code != 0 {
		t.Fatalf("client-status: code %d stderr %s", res.code, res.stderr)
	}
	if _, _, err := svc.AuthorizeSession(context.Background(), raw); err == nil {
		t.Fatal("a session survived its client's suspension")
	}
}

// Keygen provisions the two signing seeds of an environment, and root hands
// back exactly the bytes a client repository pins against.
func TestKeygenAndRootProvisioning(t *testing.T) {
	dir := t.TempDir()
	res := runCLI(t, "keygen", "--root", filepath.Join(dir, "root.seed"), "--online", filepath.Join(dir, "online.seed"),
		"--root-metadata", filepath.Join(dir, "root.json"))
	if res.code != 0 {
		t.Fatalf("keygen: code %d stderr %s", res.code, res.stderr)
	}
	// The provisioned root is real TUF root metadata delegating to the
	// freshly minted online key — the file a client repository pins.
	var rootDoc struct {
		Signed struct {
			Expires string `json:"expires"`
			Roles   map[string]struct {
				KeyIDs []string `json:"keyIDs"`
			} `json:"roles"`
		} `json:"signed"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(dir, "root.json")), &rootDoc); err != nil {
		t.Fatalf("provisioned root is not json: %v", err)
	}
	if len(rootDoc.Signed.Roles["targets"].KeyIDs) == 0 {
		t.Fatal("provisioned root carries no targets key")
	}
	// provision-root derives the same object from keys that already exist.
	res = runCLI(t, "provision-root", "--root-key", filepath.Join(dir, "root.seed"),
		"--online-key", filepath.Join(dir, "online.seed"), "--out", filepath.Join(dir, "root-again.json"))
	if res.code != 0 {
		t.Fatalf("provision-root: code %d stderr %s", res.code, res.stderr)
	}
	for _, name := range []string{"root.seed", "online.seed"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("keygen produced no %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v, want 0600", name, info.Mode().Perm())
		}
	}
	// The two files must carry different keys: one identity per role.
	rootSeed, _ := os.ReadFile(filepath.Join(dir, "root.seed"))
	onlineSeed, _ := os.ReadFile(filepath.Join(dir, "online.seed"))
	if string(rootSeed) == string(onlineSeed) {
		t.Fatal("keygen wrote the same seed to both roles")
	}
	// Refusals before anything is written.
	if res := runCLI(t, "keygen", "--root", filepath.Join(dir, "same"), "--online", filepath.Join(dir, "same")); res.code == 0 {
		t.Fatal("keygen accepted one file for both roles")
	}

	// Provision a service through those seeds, publish, and read the pinned
	// root back out of the registry.
	store := registry.NewMemStore(nil)
	blobs, err := registry.NewDirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootKey, err := registry.LoadEd25519(filepath.Join(dir, "root.seed"))
	if err != nil {
		t.Fatal(err)
	}
	onlineKey, err := registry.LoadEd25519(filepath.Join(dir, "online.seed"))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := registry.NewPublisher(rootKey, onlineKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	svc := &registry.Service{Store: store, Blobs: blobs, Publisher: publisher, PolicyID: "openpak-production"}
	newService = func(func(string) string) (*registry.Service, func(), error) { return svc, func() {}, nil }
	defer func() { newService = openService }()

	mapPath, transportsPath, caPath := writeTestInputs(t)
	res = runCLI(t, "publish", "--map", mapPath, "--transports", transportsPath, "--ca", caPath,
		"--ceiling", "switch=unified-1", "--ceiling", "wiiu=unified-1", "--channel", "stable")
	if res.code != 0 {
		t.Fatalf("publish: code %d stderr %s", res.code, res.stderr)
	}

	rootOut := filepath.Join(dir, "pinned-root.json")
	res = runCLI(t, "root", "--platform", "switch", "--channel", "stable", "--out", rootOut)
	if res.code != 0 {
		t.Fatalf("root: code %d stderr %s", res.code, res.stderr)
	}
	rev, err := svc.ResolveChannel(context.Background(), "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	want, err := svc.Blobs.Get(context.Background(), rev.Metadata["root.json"])
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rootOut)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("the written pinned root is not the registry's root.json")
	}
	if res := runCLI(t, "root"); res.code == 0 {
		t.Fatal("root without --platform succeeded")
	}
}

// Unknown commands and missing required input fail loudly without touching
// the registry.
func TestCommandRefusals(t *testing.T) {
	called := false
	newService = func(func(string) string) (*registry.Service, func(), error) {
		called = true
		return nil, func() {}, fmt.Errorf("the service must not be constructed")
	}
	defer func() { newService = openService }()

	if res := runCLI(t, "nonsense"); res.code != 2 {
		t.Fatalf("unknown command code %d, want 2", res.code)
	}
	if res := runCLI(t, "publish"); res.code == 0 {
		t.Fatal("publish without --map succeeded")
	}
	if res := runCLI(t, "withdraw", "--bundle-sha256", "nothex"); res.code == 0 {
		t.Fatal("withdraw with a non-digest succeeded")
	}
	if res := runCLI(t, "rollback"); res.code == 0 {
		t.Fatal("rollback without --platform succeeded")
	}
	if res := runCLI(t, "register", "--repository", "openpak/citron", "--kind", "emulator",
		"--platform", "gamecube", "--channel", "stable"); res.code == 0 {
		t.Fatal("register accepted an unknown platform")
	}
	if called {
		t.Fatal("a refused command constructed the registry service")
	}
}

// refresh re-signs the current content of every channel past the threshold and leaves a
// freshly published one alone.
func TestRefreshResignsStaleChannels(t *testing.T) {
	svc := newTestService(t)
	newService = func(func(string) string) (*registry.Service, func(), error) {
		return svc, func() {}, nil
	}
	defer func() { newService = openService }()
	mapPath, transportsPath, caPath := writeTestInputs(t)
	if res := runCLI(t, "publish", "--map", mapPath, "--transports", transportsPath, "--ca", caPath,
		"--ceiling", "switch=unified-1", "--ceiling", "wiiu=unified-1", "--channel", "stable"); res.code != 0 {
		t.Fatalf("publish: code %d stderr %s", res.code, res.stderr)
	}
	ctx := context.Background()
	before, err := svc.ResolveChannel(ctx, "switch", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if res := runCLI(t, "refresh"); res.code != 0 || strings.Contains(res.stdout, "re-signed") {
		t.Fatalf("a fresh channel was re-signed: code %d out %s err %s", res.code, res.stdout, res.stderr)
	}
	res := runCLI(t, "refresh", "--older-than", "0s")
	if res.code != 0 || !strings.Contains(res.stdout, "switch stable: re-signed") || !strings.Contains(res.stdout, "wiiu stable: re-signed") {
		t.Fatalf("refresh: code %d out %s err %s", res.code, res.stdout, res.stderr)
	}
	after, err := svc.ResolveChannel(ctx, "switch", "stable")
	if err != nil || after.Sequence <= before.Sequence {
		t.Fatalf("switch stable after refresh: %+v err=%v (was sequence %d)", after, err, before.Sequence)
	}
}
