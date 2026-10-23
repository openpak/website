package registryhttp

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/clientip"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/registry"
)

// Server hosts the /api/v2/network routes.
type Server struct {
	// Registry is the v2 bundle distribution service. Set by main when
	// REGISTRY_* variables are configured; nil answers 503 on
	// /api/v2/network/*.
	Registry *registry.Service
	// CIVerifier turns the bearer presented to /build-sessions into a
	// verified CI assertion. OIDC in production (02 AUTH-M1); a static
	// verifier in staging until then.
	CIVerifier registry.CIVerifier
	// V2 quotas (02 AUTH-007): issuance per client hash, requests per
	// session, artifact bytes per session. Nil means un metered, which only
	// tests do; New sets the production defaults.
	V2ClientSessionLimit *ratelimit.Limiter
	V2SessionLimit       *ratelimit.Limiter
	V2Bytes              *v2ByteMeter
	// Auth is the per-IP limiter on build-session issuance.
	Auth *ratelimit.Limiter
	// AdminKey authorizes GET /internal/status. Empty disables the route.
	AdminKey string
	log      *slog.Logger
}

// New builds the server with the production quotas. svc and ci may be nil:
// the routes then answer 503, the supported "REGISTRY_* not set" state.
func New(svc *registry.Service, ci registry.CIVerifier, log *slog.Logger) *Server {
	return &Server{
		Registry:             svc,
		CIVerifier:           ci,
		Auth:                 ratelimit.New(10, time.Minute),
		V2ClientSessionLimit: ratelimit.New(v2SessionsPerClient, time.Hour),
		V2SessionLimit:       ratelimit.New(v2SessionRequestsPerMinute, time.Minute),
		V2Bytes:              newV2ByteMeter(),
		log:                  log,
	}
}

// SetLog injects the logger for harnesses outside this package (the v2
// fetch-tool tests mount the real routes against a memory registry).
func (s *Server) SetLog(l *slog.Logger) { s.log = l }

// clientKey is the visitor's address behind Cloudflare and Traefik (internal/clientip).
func clientKey(r *http.Request) string { return clientip.Of(r) }

// newID mints a random request id for error answers.
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// etagMatch applies If-None-Match the way RFC 9110 asks: a list of entity
// tags, weak comparison, `*` matches whatever is there.
func etagMatch(ifNoneMatch, etag string) bool {
	if strings.TrimSpace(ifNoneMatch) == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}
	tag := func(t string) string {
		t = strings.TrimSpace(t)
		t = strings.TrimPrefix(t, "W/")
		return strings.Trim(t, `"`)
	}
	// The stored etag always carries its quotes; compare weak forms.
	want := tag(etag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		if tag(candidate) == want {
			return true
		}
	}
	return false
}
