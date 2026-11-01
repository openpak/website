package api

import (
	"errors"
	"net/http"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/store"
)

// The title companion features as JSON, the same data the pages under /titles/<slug>/ show:
//
//	GET /api/v1/companion/titles                      titles that have a companion page (public)
//	GET /api/v1/companion/titles/{slug}               one title and its features (public)
//	GET /api/v1/companion/titles/{slug}/rankings      a ranking board; ?board= picks one (public;
//	                                                  with a bearer token the player's own rows are marked)
//	GET /api/v1/companion/titles/{slug}/stats         the signed-in player's stats for the title (bearer)
//	GET /api/v1/me/activity                           the signed-in player's play history (bearer)
//
// They live under /companion/ because /api/v1/titles/ belongs to the mod catalogue when one is mounted.
func (s *Server) registerCompanion(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/companion/titles", s.handleCompanionTitles)
	mux.HandleFunc("GET /api/v1/companion/titles/{slug}", s.handleCompanionTitle)
	mux.HandleFunc("GET /api/v1/companion/titles/{slug}/rankings", s.handleCompanionRankings)
	mux.HandleFunc("GET /api/v1/companion/titles/{slug}/stats", s.auth(s.handleCompanionStats))
	mux.HandleFunc("GET /api/v1/me/activity", s.auth(s.handleActivity))
}

type companionTitle struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Console  string   `json:"console"`
	Status   string   `json:"status"`
	TitleID  string   `json:"title_id,omitempty"`
	Features []string `json:"features"`
}

func titleJSON(t companion.Title) companionTitle {
	out := companionTitle{Slug: t.Slug, Name: t.Name, Console: t.Console, Status: t.Status, TitleID: t.TitleID, Features: []string{}}
	for _, f := range t.Features {
		out.Features = append(out.Features, string(f))
	}
	return out
}

// companionError answers a companion failure: an unknown title or board is a 404, a backend that
// is not configured is a 503, and one that did not answer a 502.
func (s *Server) companionError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, companion.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, companion.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, what+" is not available on this deployment")
	default:
		s.log.Warn("companion", "what", what, "err", err)
		writeError(w, http.StatusBadGateway, what+" service error")
	}
}

func (s *Server) companionTitle(w http.ResponseWriter, r *http.Request) (companion.Title, bool) {
	if s.Companion == nil {
		writeError(w, http.StatusServiceUnavailable, "title pages are not available on this deployment")
		return companion.Title{}, false
	}
	t, err := s.Companion.Resolve(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.companionError(w, "title pages", err)
		return companion.Title{}, false
	}
	return t, true
}

func (s *Server) handleCompanionTitles(w http.ResponseWriter, r *http.Request) {
	if s.Companion == nil {
		writeError(w, http.StatusServiceUnavailable, "title pages are not available on this deployment")
		return
	}
	with, err := s.Companion.WithFeatures(r.Context())
	if err != nil {
		s.companionError(w, "title pages", err)
		return
	}
	out := []companionTitle{}
	for _, t := range with {
		out = append(out, titleJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": out})
}

func (s *Server) handleCompanionTitle(w http.ResponseWriter, r *http.Request) {
	if t, ok := s.companionTitle(w, r); ok {
		writeJSON(w, http.StatusOK, titleJSON(t))
	}
}

func (s *Server) handleCompanionRankings(w http.ResponseWriter, r *http.Request) {
	t, ok := s.companionTitle(w, r)
	if !ok {
		return
	}
	out, err := s.Companion.Ranking(r.Context(), t, r.URL.Query().Get("board"), s.optionalAccount(r))
	if err != nil {
		s.companionError(w, "rankings", err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCompanionStats(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	t, ok := s.companionTitle(w, r)
	if !ok {
		return
	}
	out, err := s.Companion.Stats(r.Context(), t, tok.AccountID)
	if err != nil {
		s.companionError(w, "stats", err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.Companion == nil {
		writeError(w, http.StatusServiceUnavailable, "play activity is not available on this deployment")
		return
	}
	out, err := s.Companion.Activity(r.Context(), tok.AccountID)
	if err != nil {
		s.companionError(w, "play activity", err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}
