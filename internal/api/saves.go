package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/store"
)

// The saves surface of the mobile and emulator API. An emulator pulls the newest save of a
// title on launch and pushes it on exit; the app lists. Bodies are the raw save bytes.
//
//	GET /api/v1/me/saves                                 list, with usage
//	GET /api/v1/me/saves/{platform}/{title}              newest bytes; 204 when none;
//	                                                     X-Save-Version, X-Save-Sha256, X-Save-Conflict
//	PUT /api/v1/me/saves/{platform}/{title}              body = the save; X-Save-Base = version loaded
//	                                                     from, X-Save-Device = a name for the machine
func (s *Server) registerSaves(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/saves", s.auth(s.handleSavesList))
	mux.HandleFunc("GET /api/v1/me/saves/{platform}/{title}", s.auth(s.handleSaveNewest))
	mux.HandleFunc("PUT /api/v1/me/saves/{platform}/{title}", s.auth(s.handleSaveUpload))
}

func savesErr(w http.ResponseWriter, err error) {
	var se *saves.Error
	switch {
	case errors.Is(err, saves.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "cloud saves are not available on this deployment")
	case errors.As(err, &se) && se.Code == "quota_exceeded":
		writeJSON(w, http.StatusInsufficientStorage, map[string]any{"error": "quota_exceeded", "used": se.Used, "limit": se.Limit,
			"hint": "connect your own storage at openpak.org/account/saves"})
	case errors.As(err, &se):
		writeError(w, http.StatusBadGateway, se.Error())
	default:
		writeError(w, http.StatusBadGateway, "saves service error")
	}
}

func (s *Server) handleSavesList(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	list, usage, err := s.Saves.List(r.Context(), tok.AccountID)
	if err != nil {
		savesErr(w, err)
		return
	}
	if list == nil {
		list = []saves.Save{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"saves": list, "usage": usage})
}

func (s *Server) handleSaveNewest(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	resp, err := s.Saves.Newest(r.Context(), tok.AccountID, r.PathValue("platform"), r.PathValue("title"))
	if err != nil {
		savesErr(w, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for _, h := range []string{"Content-Length", "X-Save-Version", "X-Save-Sha256", "X-Save-Conflict"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleSaveUpload(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "Content-Length required")
		return
	}
	v, err := s.Saves.Upload(r.Context(), tok.AccountID, r.PathValue("platform"), r.PathValue("title"),
		r.Header.Get("X-Save-Base"), r.Header.Get("X-Save-Device"), r.Body, r.ContentLength)
	if err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
