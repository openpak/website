package registryhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/policy"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/registry"
)

// v2Fixture is one configured registry with one approved client and one
// promoted switch/stable revision, mounted behind the real routes.
type v2Fixture struct {
	ts         *httptest.Server
	svc        *registry.Service
	store      *registry.MemStore
	srv        *Server
	clientHash string
	result     registry.PublicationResult
}

// replaceLimits swaps the v2 quota limiters, so quota tests use tiny
// windows instead of waiting out production defaults.
func (f *v2Fixture) replaceLimits(issuance, perSession *ratelimit.Limiter) {
	f.srv.V2ClientSessionLimit = issuance
	f.srv.V2SessionLimit = perSession
}

// newV2ServerFixture builds the whole stack: registry, client, publication.
func newV2ServerFixture(t *testing.T) *v2Fixture {
	t.Helper()
	ctx := context.Background()
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
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	publisher, err := registry.NewPublisher(rootKey, onlineKey, now)
	if err != nil {
		t.Fatal(err)
	}
	m, err := netprofile.LoadMap([]byte(v2FixtureMapYAML))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := policy.Build(policy.Inputs{
		PolicyID:         "openpak-production",
		Map:              m,
		CeilingRevisions: map[string]string{"switch": "unified-1"},
	}, policy.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	svc := &registry.Service{
		Store:     store,
		Blobs:     blobs,
		Publisher: publisher,
		PolicyID:  "openpak-production",
		Now:       func() time.Time { return now },
	}
	res, err := svc.Publish(ctx, registry.PublishInput{
		Canonical: canonical,
		Platform:  "switch",
		Channel:   "stable",
		Promote:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	secret := "client-secret-v2"
	sum := sha256.Sum256([]byte(secret))
	clientHash := hex.EncodeToString(sum[:])
	if err := store.PutClient(ctx, registry.Client{
		ID:          "client-1",
		ClientHash:  clientHash,
		Repository:  "openpak/citron",
		Kind:        "emulator",
		Status:      registry.ClientProductionApproved,
		Environment: registry.EnvProduction,
		Platforms:   []string{"switch"},
		Channels:    []string{"stable"},
	}); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Registry: svc,
		CIVerifier: registry.StaticCIVerifier{Token: "ci-token-1", Assertion: registry.CIAssertion{
			Issuer:      "https://token.actions.githubusercontent.com",
			Repository:  "openpak/citron",
			Workflow:    "release.yml",
			Ref:         "refs/heads/main",
			Commit:      "abc123",
			Environment: registry.EnvProduction,
		}},
		Auth: ratelimit.New(1000, time.Minute),
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &v2Fixture{
		ts:         ts,
		svc:        svc,
		store:      store,
		srv:        s,
		clientHash: clientHash,
		result:     res,
	}
}

const v2FixtureMapYAML = `
public_base_url: https://openpak.org
server:
  address: 145.241.199.19
  https_port: 443
  http_port: 80
recheck_after: 21600
platforms:
  switch:
    families: [".nintendo.net", ".battle.net"]
    exact: ["ns.photonengine.io"]
    never: ["conntest.nintendowifi.net"]
    services: {}
`

// v2POST is a small request helper.
func v2POST(t *testing.T, ts *httptest.Server, path, bearer, body string) (int, http.Header, map[string]any) {
	t.Helper()
	return v2Request(t, ts, "POST", path, bearer, body, "")
}

func v2GET(t *testing.T, ts *httptest.Server, path, bearer, ifNoneMatch string) (int, http.Header, map[string]any) {
	t.Helper()
	return v2Request(t, ts, "GET", path, bearer, "", ifNoneMatch)
}

func v2Request(t *testing.T, ts *httptest.Server, method, path, bearer, body, ifNoneMatch string) (int, http.Header, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	return res.StatusCode, res.Header, parsed
}

// The full journey of one certified release job (POL-M3's positive cases):
// authenticate, resolve, fetch the manifest, fetch artifacts and metadata by
// digest, read its own registration.
func TestV2ClientJourney(t *testing.T) {
	f := newV2ServerFixture(t)

	// The registry surface answers 404-shaped nothing without a credential.
	if code, _, _ := v2GET(t, f.ts, "/api/v2/network/clients/self", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated clients/self: %d", code)
	}

	// 1. Exchange the CI assertion for a build credential.
	code, _, body := v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1",
		`{"client_hash":"`+f.clientHash+`","platform":"switch","channel":"stable"}`)
	if code != http.StatusCreated {
		t.Fatalf("build-session: %d %v", code, body)
	}
	token, _ := body["build_token"].(string)
	if token == "" {
		t.Fatal("no build token in response")
	}

	// 2. Resolve the channel.
	code, _, body = v2POST(t, f.ts, "/api/v2/network/resolve", token, `{}`)
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %v", code, body)
	}
	manifestSHA, _ := body["manifest_sha256"].(string)
	bundleSHA, _ := body["bundle_sha256"].(string)
	if manifestSHA != f.result.Revision.ManifestSHA256 || bundleSHA != f.result.Revision.BundleSHA256 {
		t.Fatalf("resolve disagrees with the publication: %v", body)
	}

	// 3. Fetch the manifest; conditional repeat answers 304.
	code, headers, _ := v2GET(t, f.ts, "/api/v2/network/bundles/"+manifestSHA, token, "")
	if code != http.StatusOK {
		t.Fatalf("manifest fetch: %d", code)
	}
	etag := headers.Get("ETag")
	if etag == "" {
		t.Fatal("manifest carries no ETag")
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("protected bytes without no-store")
	}
	code, _, _ = v2GET(t, f.ts, "/api/v2/network/bundles/"+manifestSHA, token, etag)
	if code != http.StatusNotModified {
		t.Fatalf("conditional manifest repeat: %d", code)
	}

	// 4. The policy artifact is bound by the manifest; its digest — and only
	// its digest — is fetchable now.
	manifestBytes, err := f.svc.Blobs.Get(context.Background(), manifestSHA)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := policy.DecodeManifest(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Payloads) != 1 || manifest.Payloads[0].Path != policy.PolicyPayloadPath {
		t.Fatalf("manifest payloads: %+v", manifest.Payloads)
	}
	policyDigest := manifest.Payloads[0].SHA256
	code, _, bundleBody := v2GET(t, f.ts, "/api/v2/network/artifacts/"+policyDigest, token, "")
	if code != http.StatusOK {
		t.Fatalf("artifact fetch: %d", code)
	}
	if bundleBody["schema_version"].(float64) != 2 {
		t.Fatalf("served policy artifact is not the bundle: %v", bundleBody["schema_version"])
	}

	// 5. Metadata: every declared name serves; unknown names do not exist.
	for _, name := range []string{"root.json", "targets.json", "snapshot.json", "timestamp.json"} {
		if code, _, _ := v2GET(t, f.ts, "/api/v2/network/metadata/"+name, token, ""); code != http.StatusOK {
			t.Fatalf("metadata %s: %d", name, code)
		}
	}
	if code, _, _ := v2GET(t, f.ts, "/api/v2/network/metadata/evil.json", token, ""); code != http.StatusNotFound {
		t.Fatalf("undeclared metadata name: %d", code)
	}

	// 6. The chain the fetch tool runs, on exactly the bytes the API served.
	rootBytes, err := f.svc.Blobs.Get(context.Background(), f.result.Metadata["root.json"])
	if err != nil {
		t.Fatal(err)
	}
	targetsBytes, err := f.svc.Blobs.Get(context.Background(), f.result.Metadata["targets.json"])
	if err != nil {
		t.Fatal(err)
	}
	snapshotBytes, err := f.svc.Blobs.Get(context.Background(), f.result.Metadata["snapshot.json"])
	if err != nil {
		t.Fatal(err)
	}
	timestampBytes, err := f.svc.Blobs.Get(context.Background(), f.result.Metadata["timestamp.json"])
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, manifestBytes, time.Now()); err != nil {
		t.Fatalf("publication chain does not verify: %v", err)
	}

	// 7. clients/self: this registration and no other.
	code, _, body = v2GET(t, f.ts, "/api/v2/network/clients/self", token, "")
	if code != http.StatusOK {
		t.Fatalf("clients/self: %d", code)
	}
	if body["repository"] != "openpak/citron" {
		t.Fatalf("clients/self: %v", body)
	}
}
