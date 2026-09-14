// Package fetchtool is the CI-side half of build-time integration (PRD
// universal-tls-forwarding/03): the logic behind the openpak-network CLI.
// It talks to the distribution API exactly like a certified release job
// does — bootstrap URL and root trust pinned here, never taken from the
// environment in certified mode — and it verifies before it exposes
// anything: the output directory appears atomically, only after the whole
// signature chain and every artifact digest check out (BUILD-002).
package fetchtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openpak/website/internal/policy"
	"github.com/openpak/website/internal/registry"
)

// ToolVersion rides into the lockfile, so provenance can name the tool that
// fetched the bundle.
const ToolVersion = "0.1.0"

// LockFormatVersion is the lockfile schema the tool writes.
const LockFormatVersion = 1

// LockfileName is the lockfile every release keeps (BUILD-003).
const LockfileName = "openpak-network.lock.json"

// DefaultRegistryURL is the production distribution endpoint, pinned. A
// certified build cannot override it with an arbitrary environment URL.
const DefaultRegistryURL = "https://openpak.org/api/v2/network"

// Network modes (03, stable variable contract). Disabled is the default:
// local builds never infer certification from the presence of a hash.
const (
	ModeDisabled    = "disabled"
	ModeDevelopment = "development"
	ModeCertified   = "certified"
)

// Failure budget (BUILD-007): the fetch phase gets 120 seconds and at most
// three attempts, backing off exponentially with jitter on network errors,
// 429 and 503 only.
const (
	fetchBudget      = 120 * time.Second
	fetchMaxAttempts = 3
	fetchBaseBackoff = 2 * time.Second
	maxRetryHint     = 60 * time.Second
)

// ErrDisabled reports the supported no-networking configuration: the caller
// treats it as success and moves on without an output directory.
var ErrDisabled = errors.New("fetchtool: networking disabled")

// ErrUnverified reports any verification failure; the CLI prints the cause
// and fails the build before compilation (BUILD-010).
var ErrUnverified = errors.New("fetchtool: bundle failed verification")

// Config is the tool's view of the stable variable contract.
type Config struct {
	Mode       string // OPENPAK_NETWORK_MODE
	ClientHash string // OPENPAK_CLIENT_HASH
	Platform   string // OPENPAK_PLATFORM
	Channel    string // OPENPAK_CHANNEL
	Pin        string // OPENPAK_BUNDLE_SHA256
	TokenFile  string // OPENPAK_BUILD_TOKEN_FILE
	Output     string // OPENPAK_BUNDLE_DIR
	RootFile   string // OPENPAK_ROOT_FILE: the pinned root the tool verifies against
	// RegistryURL is derived: pinned production endpoint in certified mode,
	// OPENPAK_REGISTRY_URL in development mode only.
	RegistryURL string

	// HTTP and Clock are injectable for tests.
	HTTP  *http.Client
	Clock func() time.Time
}

// endpointFor resolves the distribution endpoint under the pinning rules:
// development points where the operator says, certified always at the
// production registry.
func endpointFor(mode, override string) (string, error) {
	switch mode {
	case ModeDevelopment:
		endpoint := strings.TrimSuffix(strings.TrimSpace(override), "/")
		if endpoint == "" {
			return "", fmt.Errorf("fetchtool: development mode needs OPENPAK_REGISTRY_URL; there is no default development endpoint")
		}
		return endpoint, nil
	case ModeCertified:
		return DefaultRegistryURL, nil
	default:
		return "", fmt.Errorf("fetchtool: OPENPAK_NETWORK_MODE %q is not one of disabled, development, certified", mode)
	}
}

// LoadConfig derives the configuration from a variable lookup and fails
// with an actionable message when certified mode is missing a required
// field (BUILD-001).
func LoadConfig(get func(string) string) (Config, error) {
	cfg := Config{
		Mode:       strings.ToLower(strings.TrimSpace(get("OPENPAK_NETWORK_MODE"))),
		ClientHash: strings.ToLower(strings.TrimSpace(get("OPENPAK_CLIENT_HASH"))),
		Platform:   strings.ToLower(strings.TrimSpace(get("OPENPAK_PLATFORM"))),
		Channel:    strings.TrimSpace(get("OPENPAK_CHANNEL")),
		Pin:        strings.ToLower(strings.TrimSpace(get("OPENPAK_BUNDLE_SHA256"))),
		TokenFile:  get("OPENPAK_BUILD_TOKEN_FILE"),
		Output:     get("OPENPAK_BUNDLE_DIR"),
		RootFile:   get("OPENPAK_ROOT_FILE"),
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeDisabled
	}
	if cfg.Mode == ModeDisabled {
		// No identity is acceptable here and no request is ever made.
		return cfg, nil
	}
	endpoint, err := endpointFor(cfg.Mode, get("OPENPAK_REGISTRY_URL"))
	if err != nil {
		return cfg, err
	}
	cfg.RegistryURL = endpoint
	// Everything below is required to talk to the registry at all.
	missing := func(name string) error {
		return fmt.Errorf("fetchtool: certified fetch needs %s; a failed official build never falls back to an unpinned release (BUILD-001)", name)
	}
	if cfg.Channel == "" {
		cfg.Channel = "stable"
	}
	if cfg.ClientHash == "" {
		return cfg, missing("OPENPAK_CLIENT_HASH")
	}
	if cfg.Platform == "" {
		return cfg, missing("OPENPAK_PLATFORM")
	}
	if cfg.TokenFile == "" {
		return cfg, missing("OPENPAK_BUILD_TOKEN_FILE")
	}
	if cfg.Output == "" {
		return cfg, missing("OPENPAK_BUNDLE_DIR")
	}
	if cfg.RootFile == "" {
		return cfg, missing("OPENPAK_ROOT_FILE")
	}
	if cfg.Pin != "" && !isSHA256(cfg.Pin) {
		return cfg, fmt.Errorf("fetchtool: OPENPAK_BUNDLE_SHA256 is not a sha-256")
	}
	return cfg, nil
}

// Tool is one configured client of the distribution API.
type Tool struct {
	cfg   Config
	token string
	http  *http.Client
	clock func() time.Time
}

// New loads the build credential from its file and returns a tool. The
// credential is read once and held only in memory; it never lands in logs
// or the lockfile (BUILD-008).
func New(cfg Config) (*Tool, error) {
	if cfg.Mode == ModeDisabled {
		return nil, ErrDisabled
	}
	raw, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("fetchtool: read OPENPAK_BUILD_TOKEN_FILE: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, fmt.Errorf("fetchtool: %s carries no credential", cfg.TokenFile)
	}
	if cfg.RootFile != "" {
		if _, err := os.ReadFile(cfg.RootFile); err != nil {
			return nil, fmt.Errorf("fetchtool: read pinned root: %w", err)
		}
	}
	httpClient := cfg.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Tool{cfg: cfg, token: token, http: httpClient, clock: clock}, nil
}

// Exchange turns one CI identity into one short-lived build credential
// (02 AUTH-002): the endpoint follows the same pinning rules as fetch, and
// the issued credential — never the CI identity itself — is what the fetch
// phase presents afterwards.
func Exchange(ctx context.Context, client *http.Client, mode, override, ciToken, clientHash, platform, channel string) (string, error) {
	endpoint, err := endpointFor(mode, override)
	if err != nil {
		return "", err
	}
	if ciToken == "" {
		return "", fmt.Errorf("fetchtool: exchange carries no CI identity")
	}
	if clientHash == "" || platform == "" {
		return "", fmt.Errorf("fetchtool: exchange needs OPENPAK_CLIENT_HASH and OPENPAK_PLATFORM")
	}
	if channel == "" {
		channel = "stable"
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	body, err := json.Marshal(map[string]string{
		"client_hash": strings.ToLower(clientHash),
		"platform":    strings.ToLower(platform),
		"channel":     channel,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/build-sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+ciToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetchtool: build-session exchange: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e)
		return "", fmt.Errorf("fetchtool: build-session exchange answered %d (%s): %s", res.StatusCode, e.Code, e.Message)
	}
	var out struct {
		BuildToken string `json:"build_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("fetchtool: build-session exchange: %w", err)
	}
	if out.BuildToken == "" {
		return "", fmt.Errorf("fetchtool: build-session exchange carried no credential")
	}
	return out.BuildToken, nil
}

// resolveResponse mirrors the registry's /resolve answer.
type resolveResponse struct {
	BundleSHA256          string `json:"bundle_sha256"`
	ManifestSHA256        string `json:"manifest_sha256"`
	Sequence              int64  `json:"sequence"`
	PolicyID              string `json:"policy_id"`
	Platform              string `json:"platform"`
	Channel               string `json:"channel"`
	CanonicalPolicySHA256 string `json:"canonical_policy_sha256"`
	MetadataPaths         []string
}

// Lockfile is the release provenance record (BUILD-003): no secrets, no
// expiring access URLs. FetchedAt records fetch time deliberately beside
// the deterministic inputs — reproducibility claims exclude it.
type Lockfile struct {
	FormatVersion         int               `json:"format_version"`
	ToolVersion           string            `json:"tool_version"`
	RegistryURL           string            `json:"registry_url"`
	ClientHash            string            `json:"client_hash"`
	Platform              string            `json:"platform"`
	Channel               string            `json:"channel"`
	PolicyID              string            `json:"policy_id"`
	CanonicalPolicySHA256 string            `json:"canonical_policy_sha256"`
	BundleSHA256          string            `json:"bundle_sha256"`
	ManifestSHA256        string            `json:"manifest_sha256"`
	Sequence              int64             `json:"sequence"`
	Metadata              map[string]string `json:"metadata"`
	Artifacts             map[string]string `json:"artifacts"`
	FetchedAt             string            `json:"fetched_at"`
}

// Fetch resolves, verifies and materializes one bundle, atomically. The
// returned lockfile is already written into the output directory.
func (t *Tool) Fetch(ctx context.Context) (*Lockfile, error) {
	// The pinned root is the trust anchor; it comes from the operator's
	// pinned source, never from the endpoint being verified.
	rootBytes, err := os.ReadFile(t.cfg.RootFile)
	if err != nil {
		return nil, fmt.Errorf("%w: pinned root unreadable: %v", ErrUnverified, err)
	}

	// Resolve: an explicit pin is passed through and never "upgraded"; a
	// channel question returns the promoted revision.
	reqBody := "{}"
	if t.cfg.Pin != "" {
		reqBody = `{"bundle_sha256":"` + t.cfg.Pin + `"}`
	}
	var res resolveResponse
	if err := t.do(ctx, http.MethodPost, "/resolve", reqBody, &res); err != nil {
		return nil, err
	}
	if t.cfg.Pin != "" && res.BundleSHA256 != t.cfg.Pin {
		return nil, fmt.Errorf("%w: resolved %s for a %s pin", ErrUnverified, res.BundleSHA256, t.cfg.Pin)
	}

	// Manifest first: it binds every payload.
	var manifestBytes []byte
	if err := t.doGet(ctx, "/bundles/"+res.ManifestSHA256, &manifestBytes, nil); err != nil {
		return nil, err
	}
	manifest, err := policy.DecodeManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	// The manifest must describe the revision resolve named.
	if manifest.PolicyID != res.PolicyID || manifest.Platform != res.Platform ||
		manifest.Channel != res.Channel || manifest.Sequence != res.Sequence {
		return nil, fmt.Errorf("%w: manifest identity does not match the resolution", ErrUnverified)
	}

	// Metadata: the signed roles, verified against the pinned root.
	metadataBytes := map[string][]byte{}
	for _, name := range []string{"root.json", "targets.json", "snapshot.json", "timestamp.json"} {
		var b []byte
		if err := t.doGet(ctx, "/metadata/"+name, &b, nil); err != nil {
			return nil, err
		}
		metadataBytes[name] = b
	}
	if err := registry.VerifyPublication(
		metadataBytes["root.json"], metadataBytes["targets.json"],
		metadataBytes["snapshot.json"], metadataBytes["timestamp.json"],
		manifestBytes, t.clock(),
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	// The pinned root is what the chain was verified against; the served
	// root must be byte-identical to it, or the "pin" pinned nothing.
	if string(metadataBytes["root.json"]) != string(rootBytes) {
		return nil, fmt.Errorf("%w: served root does not match the pinned root", ErrUnverified)
	}

	// Artifacts: every payload, digest-checked as it lands.
	artifacts := map[string]string{}
	staged, err := os.MkdirTemp(filepath.Dir(t.cfg.Output), filepath.Base(t.cfg.Output)+".tmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staged)
	for _, p := range manifest.Payloads {
		var data []byte
		if err := t.doGet(ctx, "/artifacts/"+p.SHA256, &data, nil); err != nil {
			return nil, err
		}
		if int64(len(data)) != p.Length {
			return nil, fmt.Errorf("%w: payload %s is %d bytes, manifest says %d", ErrUnverified, p.Path, len(data), p.Length)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != p.SHA256 {
			return nil, fmt.Errorf("%w: payload %s hashes to %s, manifest says %s", ErrUnverified, p.Path, got, p.SHA256)
		}
		if err := os.WriteFile(filepath.Join(staged, filepath.FromSlash(p.Path)), data, 0o644); err != nil {
			return nil, err
		}
		artifacts[p.Path] = p.SHA256
	}
	// The manifest and the verified metadata join the output, so verify and
	// audit need nothing but the directory.
	if err := os.WriteFile(filepath.Join(staged, "manifest.json"), manifestBytes, 0o644); err != nil {
		return nil, err
	}
	metaDir := filepath.Join(staged, "metadata")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	for _, name := range []string{"root.json", "targets.json", "snapshot.json", "timestamp.json"} {
		sum := sha256.Sum256(metadataBytes[name])
		digest := hex.EncodeToString(sum[:])
		metadata[name] = digest
		if err := os.WriteFile(filepath.Join(metaDir, name), metadataBytes[name], 0o644); err != nil {
			return nil, err
		}
	}

	lock := &Lockfile{
		FormatVersion:         LockFormatVersion,
		ToolVersion:           ToolVersion,
		RegistryURL:           t.cfg.RegistryURL,
		ClientHash:            t.cfg.ClientHash,
		Platform:              res.Platform,
		Channel:               res.Channel,
		PolicyID:              res.PolicyID,
		CanonicalPolicySHA256: res.CanonicalPolicySHA256,
		BundleSHA256:          res.BundleSHA256,
		ManifestSHA256:        res.ManifestSHA256,
		Sequence:              res.Sequence,
		Metadata:              metadata,
		Artifacts:             artifacts,
		FetchedAt:             t.clock().UTC().Format(time.RFC3339),
	}
	lockBytes, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staged, LockfileName), lockBytes, 0o644); err != nil {
		return nil, err
	}

	// Atomic exposure (BUILD-002): the build system sees the output
	// directory complete or not at all.
	if err := os.RemoveAll(t.cfg.Output); err != nil {
		return nil, err
	}
	if err := os.Rename(staged, t.cfg.Output); err != nil {
		return nil, err
	}
	return lock, nil
}

// Verify re-checks a fetched directory against its pinned root: the
// offline half of BUILD-005 and the `openpak-network verify` step.
func (t *Tool) Verify(ctx context.Context, dir string) (*Lockfile, error) {
	rootBytes, err := os.ReadFile(t.cfg.RootFile)
	if err != nil {
		return nil, fmt.Errorf("%w: pinned root unreadable: %v", ErrUnverified, err)
	}
	lockBytes, err := os.ReadFile(filepath.Join(dir, LockfileName))
	if err != nil {
		return nil, fmt.Errorf("fetchtool: no lockfile in %s: %w", dir, err)
	}
	var lock Lockfile
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		return nil, fmt.Errorf("fetchtool: lockfile: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	manifest, err := policy.DecodeManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, "metadata", name)) }
	targetsBytes, err := read("targets.json")
	if err != nil {
		return nil, err
	}
	snapshotBytes, err := read("snapshot.json")
	if err != nil {
		return nil, err
	}
	timestampBytes, err := read("timestamp.json")
	if err != nil {
		return nil, err
	}
	// The pinned root decides; the directory's root.json is compared to it,
	// then used only for cross-checking.
	if served, err := read("root.json"); err != nil || string(served) != string(rootBytes) {
		return nil, fmt.Errorf("%w: directory root does not match the pinned root", ErrUnverified)
	}
	if err := registry.VerifyPublication(rootBytes, targetsBytes, snapshotBytes, timestampBytes, manifestBytes, t.clock()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	// Every payload of the manifest must be present and byte-exact.
	for _, p := range manifest.Payloads {
		path := filepath.Join(dir, filepath.FromSlash(p.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: payload %s missing: %v", ErrUnverified, p.Path, err)
		}
		if int64(len(data)) != p.Length {
			return nil, fmt.Errorf("%w: payload %s is %d bytes, manifest says %d", ErrUnverified, p.Path, len(data), p.Length)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != p.SHA256 {
			return nil, fmt.Errorf("%w: payload %s hashes to %s, manifest says %s", ErrUnverified, p.Path, got, p.SHA256)
		}
	}
	// The lockfile's claims must match the verified directory.
	if lock.ManifestSHA256 != "" {
		sum := sha256.Sum256(manifestBytes)
		if got := hex.EncodeToString(sum[:]); got != lock.ManifestSHA256 {
			return nil, fmt.Errorf("%w: lockfile manifest digest %s, directory says %s", ErrUnverified, lock.ManifestSHA256, got)
		}
	}
	return &lock, nil
}

// do runs one JSON request under the failure budget (BUILD-007): at most
// three attempts within 120 seconds, exponential backoff with jitter on
// network errors, 429 and 503, immediate failure on everything else, and
// bounded server retry hints are obeyed.
func (t *Tool) do(ctx context.Context, method, path, body string, out any) error {
	deadline := t.clock().Add(fetchBudget)
	var lastErr error
	for attempt := 1; attempt <= fetchMaxAttempts; attempt++ {
		var retryAfter time.Duration
		retryable, err := t.attempt(ctx, method, path, body, out, &retryAfter)
		if err == nil {
			return nil
		}
		if !retryable {
			return err
		}
		lastErr = err
		if attempt == fetchMaxAttempts || !t.clock().Add(backoffFor(attempt, retryAfter)).Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoffFor(attempt, retryAfter)):
		}
	}
	return fmt.Errorf("fetchtool: %s %s failed after %d attempts: %w", method, path, fetchMaxAttempts, lastErr)
}

func backoffFor(attempt int, hint time.Duration) time.Duration {
	if hint > 0 {
		if hint > maxRetryHint {
			hint = maxRetryHint
		}
		return hint
	}
	d := fetchBaseBackoff << (attempt - 1)
	// Jitter: half to full of the computed wait, so a thousand runners do
	// not stampede on the same tick.
	return d/2 + time.Duration(randInt63n(int64(d/2)+1))
}

func (t *Tool) attempt(ctx context.Context, method, path, body string, out any, retryAfter *time.Duration) (retryable bool, err error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.cfg.RegistryURL+path, reader)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := t.http.Do(req)
	if err != nil {
		return true, err // network error: worth another try
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
		*retryAfter = parseRetryHint(res.Header.Get("Retry-After"))
		var e struct {
			RetryAfterSeconds int `json:"retry_after_seconds"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e) == nil && e.RetryAfterSeconds > 0 {
			*retryAfter = time.Duration(e.RetryAfterSeconds) * time.Second
		}
		return true, fmt.Errorf("endpoint answered %d", res.StatusCode)
	case res.StatusCode >= 400:
		// 400/401/403/404/410: the answer is final. Retrying an
		// authorization failure only teaches runners to hammer.
		var e struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e)
		return false, fmt.Errorf("endpoint answered %d (%s): %s", res.StatusCode, e.Code, e.Message)
	}
	if out == nil {
		return false, nil
	}
	return false, json.NewDecoder(io.LimitReader(res.Body, policy.MaxPolicyBytes<<2)).Decode(out)
}

// doGet downloads raw bytes under the failure budget.
func (t *Tool) doGet(ctx context.Context, path string, out *[]byte, _ any) error {
	return t.doRaw(ctx, path, out)
}

func (t *Tool) doRaw(ctx context.Context, path string, out *[]byte) error {
	deadline := t.clock().Add(fetchBudget)
	var lastErr error
	for attempt := 1; attempt <= fetchMaxAttempts; attempt++ {
		var retryAfter time.Duration
		retryable, data, err := t.attemptRaw(ctx, path, &retryAfter)
		if err == nil {
			*out = data
			return nil
		}
		if !retryable {
			return err
		}
		lastErr = err
		wait := backoffFor(attempt, retryAfter)
		if attempt == fetchMaxAttempts || !t.clock().Add(wait).Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("fetchtool: GET %s failed after %d attempts: %w", path, fetchMaxAttempts, lastErr)
}

func (t *Tool) attemptRaw(ctx context.Context, path string, retryAfter *time.Duration) (bool, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.cfg.RegistryURL+path, nil)
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	res, err := t.http.Do(req)
	if err != nil {
		return true, nil, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
		*retryAfter = parseRetryHint(res.Header.Get("Retry-After"))
		return true, nil, fmt.Errorf("endpoint answered %d", res.StatusCode)
	case res.StatusCode >= 400:
		return false, nil, fmt.Errorf("endpoint answered %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, policy.MaxPayloadBytes+1))
	if err != nil {
		return true, nil, err
	}
	if int64(len(data)) > policy.MaxPayloadBytes {
		return false, nil, fmt.Errorf("payload exceeds %d bytes", policy.MaxPayloadBytes)
	}
	return false, data, nil
}

func parseRetryHint(header string) time.Duration {
	if header == "" {
		return 0
	}
	var s int
	if _, err := fmt.Sscanf(header, "%d", &s); err != nil || s <= 0 {
		return 0
	}
	d := time.Duration(s) * time.Second
	if d > maxRetryHint {
		d = maxRetryHint
	}
	return d
}

func randInt63n(n int64) int64 {
	// Cheap, non-crypto jitter.
	return time.Now().UnixNano() % n
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
