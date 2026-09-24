package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/openpak/website/internal/netprofile"
)

// The network profile surface (PRD prds/network-profile-service-prd.md). Public on
// purpose: the profile is identical for everyone, carries no credential, and
// the clients most likely to need it are the ones that cannot sign in. One
// conditional GET per launch: ETag in, 304 out.
//
//	GET /api/v1/network/profile?platform=switch   the generated profile
//	GET /api/v1/network/ca.pem                    the console CA the profile pins
//	GET /api/v1/network/ceiling                   the signed redirect ceiling, verbatim
//
// Served only because the endpoint has no write side: it tells a client
// "send these hostnames to this address", which is a redirection primitive,
// so it must never grow auth, per-user answers, or a plain-HTTP path.
func (s *Server) registerNetwork(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/network/profile", s.handleNetworkProfile)
	mux.HandleFunc("GET /api/v1/network/ca.pem", s.handleNetworkCA)
	mux.HandleFunc("GET /api/v1/network/ceiling", s.handleNetworkCeiling)
}

// handleNetworkCeiling serves the signed redirect ceiling
// (docs/signed-ceiling.md) byte for byte as the owner signed it. The server
// holds no key and never rewrites the file; it only refuses to serve one
// that does not verify under the pinned key. Rollback protection is the
// clients' job: an older, validly signed file is served as it is.
func (s *Server) handleNetworkCeiling(w http.ResponseWriter, r *http.Request) {
	if s.Ceiling == nil {
		writeError(w, http.StatusServiceUnavailable, "network ceiling is not configured")
		return
	}
	body, etag, _, err := s.Ceiling.Current()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network ceiling unavailable")
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleNetworkProfile answers with the generated profile for one platform.
// Omitted platform means every platform united — what a diagnostic or the
// website itself wants and no emulator does.
func (s *Server) handleNetworkProfile(w http.ResponseWriter, r *http.Request) {
	if s.Profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "network profiles are not configured")
		return
	}
	body, etag, err := s.Profiles.Bytes(r.Context(), r.URL.Query().Get("platform"))
	if err != nil {
		if errors.Is(err, netprofile.ErrUnknownPlatform) {
			writeError(w, http.StatusBadRequest, "unknown platform")
			return
		}
		s.log.Error("network profile", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleNetworkCA serves the console CA the profile's ca block pins. A
// client bootstraps trust from nothing but openpak.org: fetch the profile,
// fetch the CA, check the digest before trusting it.
func (s *Server) handleNetworkCA(w http.ResponseWriter, r *http.Request) {
	if s.Profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "network profiles are not configured")
		return
	}
	pem, info, err := s.Profiles.CA(r.Context())
	if err != nil {
		// The same answer as the emulator surface's /ca.pem: no CA is a
		// 404, not a 500, because the client has a fallback and this is a
		// state question, not a server failure.
		writeError(w, http.StatusNotFound, "no CA available")
		return
	}
	etag := `"` + info.SHA256 + `"`
	w.Header().Set("ETag", etag)
	// The digest again in a plain header, because the profile's ca.sha256
	// must be checkable against the response with nothing but curl.
	w.Header().Set("X-CA-Sha256", info.SHA256)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pem)
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
