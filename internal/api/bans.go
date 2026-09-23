package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// The ban lookup (docs/ban-lookup.md): for services that speak HTTP and hold
// WEBSITE_INTERNAL_KEY, or that want the reason to show a player. The status
// is the core's (Identity.GetAccount); the reason, actor and time are the
// website's record of the ban it made. Internal, so not in the OpenAPI.
func (s *Server) registerBans(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/bans/{id}", s.handleBanLookup)
	mux.HandleFunc("GET /internal/bans", s.handleBanLookup)
}

// internalAuthorized checks the shared key in constant time and answers the
// failure itself. Bearer is the pattern of the other /internal routes;
// X-Internal-Key is accepted too, as the core's gRPC callers spell it.
func internalAuthorized(w http.ResponseWriter, r *http.Request) bool {
	key := internalKey()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable, "internal surface is not configured")
		return false
	}
	bearer := subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) == 1
	header := subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(key)) == 1
	if !bearer && !header {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *Server) handleBanLookup(w http.ResponseWriter, r *http.Request) {
	if !internalAuthorized(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		ns, subject := r.URL.Query().Get("namespace"), r.URL.Query().Get("subject")
		if ns == "" || subject == "" {
			writeError(w, http.StatusBadRequest, "an account id, or namespace and subject, is required")
			return
		}
		var err error
		if id, err = s.Links.AccountBySubject(r.Context(), ns, subject); err != nil {
			if errors.Is(err, core.ErrNotFound) {
				writeError(w, http.StatusNotFound, "no account for that subject")
				return
			}
			s.log.Error("ban lookup: subject", "err", err)
			writeError(w, http.StatusBadGateway, "the account service did not answer")
			return
		}
	}
	acct, err := s.Identity.GetAccount(r.Context(), id)
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "account not found")
		return
	case err != nil:
		s.log.Error("ban lookup: account", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
		return
	}
	out := map[string]any{"account_id": id, "status": acct.Status, "banned": acct.Status != "active",
		"reason": "", "actor": "", "since": ""}
	if acct.Status == "banned" {
		if b, err := s.DB.GetBan(r.Context(), id); err == nil && b.Banned {
			out["reason"], out["actor"], out["since"] = b.Reason, b.Actor, b.UpdatedAt.UTC().Format(time.RFC3339)
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Error("ban lookup: record", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
