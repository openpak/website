package api

import (
	"github.com/openpak/website/internal/core"
	"net/http"
)

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	handles, ok := s.Identity.(core.Handles)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "handle check unavailable")
		return
	}
	h := r.URL.Query().Get("h")
	if len(h) > 15 {
		writeJSON(w, http.StatusOK, core.HandleCheck{Reason: "handle_invalid"})
		return
	}
	result, err := handles.CheckHandle(r.Context(), h)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "handle check unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
