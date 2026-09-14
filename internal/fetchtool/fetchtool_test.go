package fetchtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/api"
	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/policy"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/registry"
)

// harness is the whole stack: registry, routes, and one promoted revision.
type harness struct {
	ts         *httptest.Server
	svc        *registry.Service
	blobs      *registry.DirBlobs
	result     registry.PublicationResult
	clientHash string
}

func newHarness(t *testing.T) *harness {
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
	m, err := netprofile.LoadMap([]byte(`
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
`))
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
	secret := "harness-client-secret"
	sum := sha256.Sum256([]byte(secret))
	clientHash := hex.EncodeToString(sum[:])
	if err := store.PutClient(ctx, registry.Client{
		ID:          "client-1",
		ClientHash:  clientHash,
		Repository:  "openpak/citron",
		Status:      registry.ClientProductionApproved,
		Environment: registry.EnvProduction,
		Platforms:   []string{"switch"},
		Channels:    []string{"stable"},
		Workflows:   []string{"openpak/citron/.github/workflows/release.yml"},
	}); err != nil {
		t.Fatal(err)
	}
	s := &api.Server{
		Registry: svc,
		CIVerifier: registry.StaticCIVerifier{Token: "ci-token-1", Assertion: registry.CIAssertion{
			Issuer:      "https://token.actions.githubusercontent.com",
			Repository:  "openpak/citron",
			Workflow:    "openpak/citron/.github/workflows/release.yml",
			Commit:      "abc123",
			Environment: registry.EnvProduction,
		}},
		Auth:                 ratelimit.New(1000, time.Minute),
		V2ClientSessionLimit: ratelimit.New(1000, time.Hour),
		V2SessionLimit:       ratelimit.New(1000, time.Minute),
	}
	s.SetLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &harness{
		ts:         ts,
		svc:        svc,
		blobs:      blobs,
		result:     res,
		clientHash: clientHash,
	}
}

// pinnedRoot writes the published root to a file: what an operator's pinned
// source hands CI.
func (h *harness) pinnedRoot(t *testing.T) string {
	t.Helper()
	root, err := h.blobs.Get(context.Background(), h.result.Metadata["root.json"])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(path, root, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (h *harness) issueToken(t *testing.T) string {
	t.Helper()
	body := `{"client_hash":"` + h.clientHash + `","platform":"switch","channel":"stable"}`
	req, err := http.NewRequest(http.MethodPost, h.ts.URL+"/api/v2/network/build-sessions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ci-token-1")
	res, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var doc struct {
		Token string `json:"build_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Token == "" {
		t.Fatal("issuance returned no token")
	}
	return doc.Token
}

func (h *harness) tool(t *testing.T, mutate func(*Config)) *Tool {
	t.Helper()
	cfg := Config{
		Mode:        ModeCertified,
		RegistryURL: h.ts.URL + "/api/v2/network", // the harness endpoint stands in for the pinned one
		ClientHash:  h.clientHash,
		Platform:    "switch",
		Channel:     "stable",
		TokenFile:   h.writeToken(t, h.issueToken(t)),
		Output:      filepath.Join(t.TempDir(), "bundle"),
		RootFile:    h.pinnedRoot(t),
		HTTP:        h.ts.Client(),
		Clock:       func() time.Time { return time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC) },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	tool, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func (h *harness) writeToken(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The full release-job flow of BUILD-M1: fetch, then verify offline, then
// confirm the emitted tree.
func TestFetchAndVerify(t *testing.T) {
	h := newHarness(t)
	tool := h.tool(t, nil)
	lock, err := tool.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if lock.Sequence != h.result.Revision.Sequence || lock.BundleSHA256 != h.result.Revision.BundleSHA256 {
		t.Fatalf("lockfile disagrees with the publication: %+v", lock)
	}
	dir := tool.cfg.Output
	for _, name := range []string{
		"policy.json", "manifest.json", LockfileName,
		"metadata/root.json", "metadata/targets.json", "metadata/snapshot.json", "metadata/timestamp.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("fetched tree lacks %s: %v", name, err)
		}
	}
	// The lockfile carries no token-shaped secret.
	raw, _ := os.ReadFile(filepath.Join(dir, LockfileName))
	if strings.Contains(string(raw), "opb_") {
		t.Fatal("lockfile leaked a credential-shaped string")
	}
	// Offline verify of the fetched directory.
	if _, err := tool.Verify(context.Background(), dir); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// A tampered policy byte must fail verification (BUILD-M3 corruption).
	policyBytes, err := os.ReadFile(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(policyBytes), `"sequence":1`, `"sequence":2`, 1)
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Verify(context.Background(), dir); err == nil {
		t.Fatal("verify accepted a tampered policy document")
	}
}

func TestFetchRejections(t *testing.T) {
	h := newHarness(t)

	t.Run("pinned digest is honored exactly", func(t *testing.T) {
		tool := h.tool(t, func(c *Config) { c.Pin = strings.Repeat("a", 64) })
		if _, err := tool.Fetch(context.Background()); err == nil {
			t.Fatal("a foreign pin resolved")
		}
	})
	t.Run("wrong pinned root is refused", func(t *testing.T) {
		other, err := registry.GenerateEd25519()
		if err != nil {
			t.Fatal(err)
		}
		otherPublisher, err := registry.NewPublisher(other, other, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := otherPublisher.Publish(registry.Publication{Sequence: 1, Manifest: []byte("x"), ExpiryAnchor: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "root.json")
		if err := os.WriteFile(path, metadata["root.json"], 0o600); err != nil {
			t.Fatal(err)
		}
		tool := h.tool(t, func(c *Config) { c.RootFile = path })
		if _, err := tool.Fetch(context.Background()); err == nil {
			t.Fatal("fetch succeeded against a root we do not pin")
		}
	})
	t.Run("missing credential file is an actionable failure", func(t *testing.T) {
		cfg := Config{
			Mode: ModeCertified, RegistryURL: h.ts.URL, ClientHash: h.clientHash,
			Platform: "switch", Channel: "stable", Output: filepath.Join(t.TempDir(), "b"),
			RootFile: h.pinnedRoot(t), TokenFile: filepath.Join(t.TempDir(), "absent"),
		}
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "OPENPAK_BUILD_TOKEN_FILE") {
			t.Fatalf("missing credential: %v", err)
		}
	})
	t.Run("certified mode requires every variable", func(t *testing.T) {
		// The same environment, minus the client hash: certification may
		// not degrade gracefully into a partial identity.
		_, err := LoadConfig(func(name string) string {
			if name == "OPENPAK_NETWORK_MODE" {
				return "certified"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "OPENPAK_CLIENT_HASH") {
			t.Fatal("certified mode tolerated a missing identity")
		}
	})
	t.Run("disabled mode makes no request", func(t *testing.T) {
		cfg, err := LoadConfig(func(string) string { return "" })
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Mode != ModeDisabled {
			t.Fatalf("mode %q, want disabled", cfg.Mode)
		}
		if _, err := New(cfg); !errors.Is(err, ErrDisabled) {
			t.Fatalf("disabled mode built a tool: %v", err)
		}
	})
	t.Run("development mode may not look certified", func(t *testing.T) {
		_, err := LoadConfig(func(name string) string {
			if name == "OPENPAK_NETWORK_MODE" {
				return "development"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "OPENPAK_REGISTRY_URL") {
			t.Fatal("development mode without an endpoint was accepted")
		}
	})
}

// flakyTransport fails the first n requests with 503, then passes through:
// the failure budget must absorb it (BUILD-007).
type flakyTransport struct {
	inner http.RoundTripper
	fail  int
	seen  int
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.seen++
	if f.seen <= f.fail {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return f.inner.RoundTrip(req)
}

func TestFetchRetriesThroughATransientOutage(t *testing.T) {
	h := newHarness(t)
	tool := h.tool(t, nil)
	flaky := &flakyTransport{inner: h.ts.Client().Transport, fail: 2}
	tool.http = &http.Client{Transport: flaky}
	if _, err := tool.Fetch(context.Background()); err != nil {
		t.Fatalf("fetch did not ride out the outage: %v", err)
	}
	if flaky.seen < 3 {
		t.Fatalf("only %d transport requests; the retries did not happen", flaky.seen)
	}
	// More failures than the budget allows must fail the fetch, not hang.
	stubborn := &flakyTransport{inner: h.ts.Client().Transport, fail: 1000}
	tool.http = &http.Client{Transport: stubborn}
	if _, err := tool.Fetch(context.Background()); err == nil {
		t.Fatal("fetch succeeded through a permanent outage")
	}
}

// Authorization failures are final: no retry, one request.
func TestFetchDoesNotRetryAuthorizationFailures(t *testing.T) {
	h := newHarness(t)
	tool := h.tool(t, func(c *Config) { c.TokenFile = h.writeToken(t, "opb_expired") })
	counting := &flakyTransport{inner: h.ts.Client().Transport}
	tool.http = &http.Client{Transport: counting}
	if _, err := tool.Fetch(context.Background()); err == nil {
		t.Fatal("fetch succeeded with a dead credential")
	}
	if counting.seen != 1 {
		t.Fatalf("authorization failure retried %d times", counting.seen-1)
	}
}
