// Package registryhttp serves the v2 network distribution surface (PRD
// emulators/prds/universal-tls-forwarding/01) as its own service,
// cmd/openpak-registry. It is a separate trust domain from the website: its
// own database, blob store, online signing key and CI identity, so none of
// that is loaded into the process that serves openpak.org's pages.
//
// Unlike the v1 profile, everything here is protected: a route change is a
// release input, not a launch-time answer, and the bytes a certified client
// embeds come from an authenticated, signed registry. Credentials ride
// authorization headers, never query strings, and bootstrap redirects to
// arbitrary hosts are forbidden.
//
//	POST /api/v2/network/build-sessions   CI assertion + client hash → build credential
//	POST /api/v2/network/resolve          pinned digest or channel → resolved revision
//	GET  /api/v2/network/metadata/{name}  signed TUF metadata, declared names only
//	GET  /api/v2/network/bundles/{sha256} the resolved bundle's signed manifest
//	GET  /api/v2/network/artifacts/{sha256} artifact bytes bound by that manifest
//	GET  /api/v2/network/clients/self     the caller's own registration
//
// Every request re-authorizes, conditional ones included: a cached ETag
// never answers before the credential does.
package registryhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/policy"
	"github.com/openpak/website/internal/registry"
	"github.com/openpak/website/internal/session"
)

const v2RFC3339 = "2006-01-02T15:04:05Z07:00"

// Quotas of AUTH-007, proposed starting values. They are abuse controls,
// not authorization: exceeding one answers 429 with a bounded hint and
// changes no scope.
const (
	// v2SessionsPerClient is the build-session issuance quota per client
	// hash per hour. Applied to unknown hashes too, so probing a hash list
	// buys nothing but a 403 every time.
	v2SessionsPerClient = 30
	// v2SessionRequestsPerMinute is the per-session request quota across
	// the authenticated surface.
	v2SessionRequestsPerMinute = 120
	// v2SessionByteBudget meters artifact bytes per session separately
	// from the request count: one 256 MiB patch bundle must not look like
	// 120 small metadata reads.
	v2SessionByteBudget = int64(2) << 30
)

// v2ByteMeter tracks cumulative artifact bytes served to one session.
// Entries outlive their sessions by at most an hour (the sweep).
type v2ByteMeter struct {
	mu   sync.Mutex
	used map[string]v2ByteUse
}

type v2ByteUse struct {
	bytes int64
	seen  time.Time
}

func newV2ByteMeter() *v2ByteMeter {
	m := &v2ByteMeter{used: map[string]v2ByteUse{}}
	go m.sweep()
	return m
}

// take records n bytes for the session and reports whether the budget
// allows it.
func (m *v2ByteMeter) take(sessionID string, n int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	use := m.used[sessionID]
	if use.bytes+n > v2SessionByteBudget {
		return false
	}
	use.bytes += n
	use.seen = time.Now()
	m.used[sessionID] = use
	return true
}

func (m *v2ByteMeter) sweep() {
	for range time.Tick(time.Hour) {
		cut := time.Now().Add(-time.Hour)
		m.mu.Lock()
		for id, use := range m.used {
			if use.seen.Before(cut) {
				delete(m.used, id)
			}
		}
		m.mu.Unlock()
	}
}

// v2Error is the PRD 01 error shape: no secrets, a code a client can branch
// on, and a request id to ask support about.
type v2Error struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	RequestID         string `json:"request_id"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

func writeV2Error(w http.ResponseWriter, status int, code, message string, retryAfter ...int) {
	e := v2Error{Code: code, Message: message, RequestID: newID()}
	if len(retryAfter) > 0 {
		e.RetryAfterSeconds = retryAfter[0]
		w.Header().Set("Retry-After", itoa(retryAfter[0]))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// v2Auth is the identity a build credential carries into a handler.
type v2Auth struct {
	Session registry.BuildSession
	Client  registry.Client
}

// networkV2 authorizes every v2 request, conditionals included, and maps
// registry denials to one generic answer so nothing about registrations
// leaks through status codes. The per-session request quota runs here too,
// after authorization (an expired session learns nothing at all).
func (s *Server) networkV2(next func(http.ResponseWriter, *http.Request, v2Auth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Registry == nil || s.CIVerifier == nil {
			writeV2Error(w, http.StatusServiceUnavailable, "unavailable", "network registry is not configured")
			return
		}
		raw := session.BearerFromRequest(r)
		if raw == "" {
			writeV2Error(w, http.StatusUnauthorized, "unauthorized", "a build credential is required")
			return
		}
		sess, client, err := s.Registry.AuthorizeSession(r.Context(), raw)
		if err != nil {
			// Missing, expired, revoked, suspended: one answer.
			writeV2Error(w, http.StatusUnauthorized, "unauthorized", "credential rejected")
			return
		}
		if s.V2SessionLimit != nil && !s.V2SessionLimit.Allow(sess.ID) {
			writeV2Error(w, http.StatusTooManyRequests, "rate_limited", "session request quota exceeded", 60)
			return
		}
		next(w, r, v2Auth{Session: sess, Client: client})
	}
}

// RegisterRoutes mounts the protected distribution surface.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v2/network/build-sessions", s.handleBuildSessions)
	mux.HandleFunc("POST /api/v2/network/resolve", s.networkV2(s.handleResolve))
	mux.HandleFunc("GET /api/v2/network/metadata/{name}", s.networkV2(s.handleMetadataV2))
	mux.HandleFunc("GET /api/v2/network/bundles/{sha256}", s.networkV2(s.handleBundle))
	mux.HandleFunc("GET /api/v2/network/artifacts/{sha256}", s.networkV2(s.handleArtifact))
	mux.HandleFunc("GET /api/v2/network/clients/self", s.networkV2(s.handleClientSelf))
}

// handleBuildSessions exchanges a verified CI assertion plus a client hash
// for a short-lived build credential (AUTH-001..003). The CI bearer is
// presented once in the Authorization header and never stored; the returned
// credential is the only thing the job keeps.
func (s *Server) handleBuildSessions(w http.ResponseWriter, r *http.Request) {
	if s.Registry == nil || s.CIVerifier == nil {
		writeV2Error(w, http.StatusServiceUnavailable, "unavailable", "network registry is not configured")
		return
	}
	if !s.Auth.Allow(clientKey(r)) {
		writeV2Error(w, http.StatusTooManyRequests, "rate_limited", "too many session attempts", 60)
		return
	}
	var req struct {
		ClientHash string `json:"client_hash"`
		Platform   string `json:"platform"`
		Channel    string `json:"channel"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeV2Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// Issuance quota per attempted client hash (AUTH-007). Applied before
	// any authorization answer so a valid hash and an invalid one cost the
	// same and reveal the same thing: nothing.
	if s.V2ClientSessionLimit != nil && !s.V2ClientSessionLimit.Allow(req.ClientHash) {
		writeV2Error(w, http.StatusTooManyRequests, "rate_limited", "session quota exceeded for this client", 60)
		return
	}
	assertion, err := s.CIVerifier.VerifyAssertion(r.Context(), session.BearerFromRequest(r))
	if err != nil {
		// One answer for a missing, malformed or untrusted assertion; the
		// same answer for an unknown client hash. Neither reveals more.
		writeV2Error(w, http.StatusForbidden, "forbidden", "authorization failed")
		return
	}
	raw, sess, err := s.Registry.OpenBuildSession(r.Context(), req.ClientHash, req.Platform, req.Channel, assertion)
	if err != nil {
		writeV2Error(w, http.StatusForbidden, "forbidden", "authorization failed")
		return
	}
	writeV2JSON(w, http.StatusCreated, map[string]any{
		"session_id":  sess.ID,
		"build_token": raw,
		"token_type":  "Bearer",
		"expires_at":  sess.ExpiresAt.UTC().Format(v2RFC3339),
	})
}

// handleResolve answers with the exact revision: either the requested digest
// — never substituted, never "upgraded" — or the channel pointer's current
// revision. The response is what a lockfile pins (03: BUILD-003).
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, a v2Auth) {
	var req struct {
		BundleSHA256 string `json:"bundle_sha256"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeV2Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	var rev registry.Revision
	var err error
	if req.BundleSHA256 != "" {
		if !isSHA256Hex(req.BundleSHA256) {
			writeV2Error(w, http.StatusBadRequest, "bad_request", "bundle_sha256 is not a sha-256")
			return
		}
		rev, err = s.Registry.Store.RevisionByBundleDigest(r.Context(), a.Session.Platform, a.Session.Channel, req.BundleSHA256)
		if err != nil {
			// An explicit digest that does not exist, or that belongs to
			// another platform or channel: the same 404 either way, and
			// never a newer bundle in its place.
			writeV2Error(w, http.StatusNotFound, "not_found", "no such bundle for this registration")
			return
		}
		if rev.WithdrawnAt != nil {
			writeV2Error(w, http.StatusGone, "gone", "this bundle has been withdrawn")
			return
		}
	} else {
		rev, err = s.Registry.ResolveChannel(r.Context(), a.Session.Platform, a.Session.Channel)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeV2Error(w, http.StatusNotFound, "not_found", "channel has no promoted revision")
				return
			}
			s.log.Error("v2: channel resolution", "err", err)
			writeV2Error(w, http.StatusServiceUnavailable, "unavailable", "channel resolution failed; retry", 10)
			return
		}
	}
	if err := s.Registry.Store.MarkSessionResolved(r.Context(), a.Session.ID, rev.ManifestSHA256); err != nil {
		s.log.Error("v2: mark session resolved", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeV2JSON(w, http.StatusOK, map[string]any{
		"bundle_sha256":           rev.BundleSHA256,
		"manifest_sha256":         rev.ManifestSHA256,
		"sequence":                rev.Sequence,
		"policy_id":               rev.PolicyID,
		"platform":                rev.Platform,
		"channel":                 rev.Channel,
		"canonical_policy_sha256": rev.CanonicalPolicySHA256,
		"issued_at":               rev.IssuedAt.UTC().Format(v2RFC3339),
		"metadata_paths":          registry.MetadataNames,
	})
}

// handleMetadataV2 serves signed TUF metadata. Names are restricted to the
// declared set, and a session must have resolved a revision first; the bytes
// come from what that revision's publication signed, never re-signed.
func (s *Server) handleMetadataV2(w http.ResponseWriter, r *http.Request, a v2Auth) {
	name := r.PathValue("name")
	if !registry.IsMetadataName(name) {
		writeV2Error(w, http.StatusNotFound, "not_found", "no such metadata")
		return
	}
	manifestSHA, err := s.Registry.Store.SessionResolution(r.Context(), a.Session.ID)
	if err != nil {
		writeV2Error(w, http.StatusForbidden, "forbidden", "resolve a bundle before fetching metadata")
		return
	}
	meta, ok, err := s.Registry.MetadataFor(r.Context(), a.Session.Platform, a.Session.Channel, manifestSHA, name)
	if err != nil {
		s.log.Error("v2: metadata lookup", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if !ok {
		writeV2Error(w, http.StatusNotFound, "not_found", "no such metadata")
		return
	}
	writeV2DigestBytes(w, r, meta, "application/json")
}

// handleBundle serves the resolved revision's signed manifest. Digest
// knowledge alone grants nothing: only the manifest this session resolved
// is answerable.
func (s *Server) handleBundle(w http.ResponseWriter, r *http.Request, a v2Auth) {
	requested := r.PathValue("sha256")
	if !isSHA256Hex(requested) {
		writeV2Error(w, http.StatusBadRequest, "bad_request", "not a sha-256")
		return
	}
	manifestSHA, err := s.Registry.Store.SessionResolution(r.Context(), a.Session.ID)
	if err != nil || manifestSHA != requested {
		// Not resolved, or resolved to something else: no bytes, no hint.
		writeV2Error(w, http.StatusForbidden, "forbidden", "resolve this bundle first")
		return
	}
	manifestBytes, err := s.Registry.Blobs.Get(r.Context(), manifestSHA)
	if err != nil {
		s.log.Error("v2: manifest blob", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeV2DigestBytes(w, r, manifestBytes, "application/json")
}

// handleArtifact serves one artifact bound by the resolved manifest: the
// resolution is the authorization (PRD 01: digest knowledge grants no
// access).
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request, a v2Auth) {
	requested := r.PathValue("sha256")
	if !isSHA256Hex(requested) {
		writeV2Error(w, http.StatusBadRequest, "bad_request", "not a sha-256")
		return
	}
	manifestSHA, err := s.Registry.Store.SessionResolution(r.Context(), a.Session.ID)
	if err != nil {
		writeV2Error(w, http.StatusForbidden, "forbidden", "resolve a bundle first")
		return
	}
	manifestBytes, err := s.Registry.Blobs.Get(r.Context(), manifestSHA)
	if err != nil {
		s.log.Error("v2: artifact manifest blob", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	manifest, err := policy.DecodeManifest(manifestBytes)
	if err != nil {
		s.log.Error("v2: stored manifest unparsable", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	mediaType := ""
	found := false
	for _, p := range manifest.Payloads {
		if p.SHA256 == requested {
			mediaType, found = p.MediaType, true
			break
		}
	}
	if !found {
		// A digest outside the resolved manifest: no existence hint either.
		writeV2Error(w, http.StatusForbidden, "forbidden", "artifact is not part of the resolved bundle")
		return
	}
	data, err := s.Registry.Blobs.Get(r.Context(), requested)
	if err != nil {
		s.log.Error("v2: artifact blob", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	// Artifact bytes are metered separately from request count (AUTH-007).
	if s.V2Bytes != nil && !s.V2Bytes.take(a.Session.ID, int64(len(data))) {
		writeV2Error(w, http.StatusTooManyRequests, "rate_limited", "session artifact budget exceeded", 60)
		return
	}
	writeV2DigestBytes(w, r, data, mediaType)
}

// handleClientSelf answers with the caller's own registration; other
// clients' records do not exist for this endpoint.
func (s *Server) handleClientSelf(w http.ResponseWriter, _ *http.Request, a v2Auth) {
	writeV2JSON(w, http.StatusOK, map[string]any{
		"client_id":   a.Client.ID,
		"repository":  a.Client.Repository,
		"kind":        a.Client.Kind,
		"status":      a.Client.Status,
		"environment": a.Client.Environment,
		"platforms":   a.Client.Platforms,
		"channels":    a.Client.Channels,
	})
}

// writeV2JSON stamps the no-store discipline every control-plane answer
// carries.
func writeV2JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeV2DigestBytes serves immutable bytes with a digest-based ETag and
// answers conditional requests — after authorization, which networkV2 has
// already done by the time a handler runs.
func writeV2DigestBytes(w http.ResponseWriter, r *http.Request, data []byte, mediaType string) {
	digest := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func isSHA256Hex(s string) bool {
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

// Configure builds the registry service from REGISTRY_* variables. Absent
// configuration leaves the surface answering 503. The returned closer
// releases the store; a nil service means not configured.
func Configure(log *slog.Logger) (*registry.Service, registry.CIVerifier, func()) {
	databaseURL := os.Getenv("REGISTRY_DATABASE_URL")
	blobDir := os.Getenv("REGISTRY_BLOB_DIR")
	onlineKeyFile := os.Getenv("REGISTRY_ONLINE_KEY_FILE")
	rootMetadataFile := os.Getenv("REGISTRY_ROOT_METADATA_FILE")
	if databaseURL == "" && blobDir == "" && onlineKeyFile == "" && rootMetadataFile == "" {
		return nil, nil, func() {}
	}
	var missing []string
	for name, value := range map[string]string{
		"REGISTRY_DATABASE_URL":       databaseURL,
		"REGISTRY_BLOB_DIR":           blobDir,
		"REGISTRY_ONLINE_KEY_FILE":    onlineKeyFile,
		"REGISTRY_ROOT_METADATA_FILE": rootMetadataFile,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		// A half-configured registry would answer 503 while looking
		// configured; say which variable is absent instead.
		log.Error("v2 registry: configuration incomplete", "missing", strings.Join(missing, ", "))
		return nil, nil, func() {}
	}
	policyID := os.Getenv("REGISTRY_POLICY_ID")
	if policyID == "" {
		policyID = "openpak-production"
	}
	ctx := context.Background()
	store, err := registry.OpenPG(ctx, databaseURL)
	if err != nil {
		log.Error("v2 registry: open store", "err", err)
		return nil, nil, func() {}
	}
	fail := func(err error, what string) (*registry.Service, registry.CIVerifier, func()) {
		store.Close()
		log.Error("v2 registry: "+what, "err", err)
		return nil, nil, func() {}
	}
	blobs, err := registry.NewDirBlobs(blobDir)
	if err != nil {
		return fail(err, "blob dir")
	}
	// The root seed is deliberately NOT loaded here: the offline root
	// provisions its metadata once (POL-006), and the publication host only
	// ever needs the online key plus those provisioned bytes.
	onlineKey, err := registry.LoadEd25519(onlineKeyFile)
	if err != nil {
		return fail(err, "online key")
	}
	rootBytes, err := os.ReadFile(rootMetadataFile)
	if err != nil {
		return fail(err, "root metadata")
	}
	publisher, err := registry.NewPublisherWithRoot(rootBytes, onlineKey)
	if err != nil {
		return fail(err, "publisher")
	}
	var ci registry.CIVerifier
	if audience := os.Getenv("REGISTRY_OIDC_AUDIENCE"); audience != "" {
		// Production path (02 AUTH-M1): GitHub Actions workload identity,
		// verified against the issuer's published keys. A workflow must
		// request this audience explicitly; tokens minted for anyone else
		// are refused.
		issuer := os.Getenv("REGISTRY_OIDC_ISSUER")
		if issuer == "" {
			issuer = "https://token.actions.githubusercontent.com"
		}
		ci = registry.NewOIDCVerifier(issuer, audience, nil, nil)
		log.Info("v2 registry: OIDC verification enabled", "issuer", issuer, "audience", audience)
	} else if token := os.Getenv("REGISTRY_STAGING_CI_TOKEN"); token != "" {
		// Staging path: one static credential for the staging CI runner.
		// The OIDC verifier replaces it before production; the branch is
		// deliberately loud so an operator notices it in the logs.
		ci = registry.StaticCIVerifier{
			Token: token,
			Assertion: registry.CIAssertion{
				Issuer:      "openpak://staging",
				Repository:  os.Getenv("REGISTRY_STAGING_CI_REPOSITORY"),
				Commit:      "staging",
				Environment: "nonproduction",
			},
		}
		log.Warn("v2 registry: static staging CI credential in use; OIDC verification replaces it before production")
	}
	svc := &registry.Service{
		Store:     store,
		Blobs:     blobs,
		Publisher: publisher,
		PolicyID:  policyID,
	}
	return svc, ci, func() { store.Close() }
}
