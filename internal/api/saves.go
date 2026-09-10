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
	s.registerSavesStorage(mux)
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

// The storage settings, for the app: the same operations the website's saves page offers.
//
//	GET    /api/v1/me/saves/storage          connected?, endpoint, bucket, prefix, access key, test result, move state, usage
//	PUT    /api/v1/me/saves/storage          S3 config JSON; tested before it is stored; 422 with detail when the bucket fails
//	DELETE /api/v1/me/saves/storage          disconnect; versions stored there stay listed, unreachable
//	POST   /api/v1/me/saves/storage/move     copy the allowance into the bucket, in the background
func (s *Server) registerSavesStorage(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/saves/storage", s.auth(s.handleStorageGet))
	mux.HandleFunc("PUT /api/v1/me/saves/storage", s.auth(s.handleStoragePut))
	mux.HandleFunc("DELETE /api/v1/me/saves/storage", s.auth(s.handleStorageDelete))
	mux.HandleFunc("POST /api/v1/me/saves/storage/move", s.auth(s.handleStorageMove))
}

func (s *Server) handleStorageGet(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	st, err := s.Saves.Storage(r.Context(), tok.AccountID)
	if err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStoragePut(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var cfg saves.S3Config
	if !decodeSmall(w, r, &cfg) {
		return
	}
	warning, err := s.Saves.Connect(r.Context(), tok.AccountID, cfg)
	if err != nil {
		var se *saves.Error
		if errors.As(err, &se) && se.Code == "storage_test_failed" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "storage_test_failed", "detail": se.Detail})
			return
		}
		savesErr(w, err)
		return
	}
	st, err := s.Saves.Storage(r.Context(), tok.AccountID)
	if err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"storage": st, "warning": warning})
}

func (s *Server) handleStorageDelete(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if err := s.Saves.Disconnect(r.Context(), tok.AccountID); err != nil {
		savesErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStorageMove(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if err := s.Saves.Move(r.Context(), tok.AccountID); err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"move_state": "moving"})
}
