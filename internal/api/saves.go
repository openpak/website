package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/store"
)

// The saves surface of the app: the account's Save Data Cloud, which holds console backups
// made by a real Switch or by Harbor. The emulator save routes are retired (410 Gone).
//
//	GET /api/v1/me/saves                                 list, with usage against the quota; each version
//	                                                     has date, size and console ("Harbor" or "Switch")
//	DELETE /api/v1/me/saves/{platform}/{title}           delete every backup of a title ({"deleted":n}; 404: none)
//	GET|PUT /api/v1/me/saves/{platform}/{title}          410 Gone
//	GET    /api/v1/me/saves/versions/{id}               one version's bytes (single-part versions)
//	POST   /api/v1/me/saves/versions/{id}/restore       make it the current one; the old stays
//	DELETE /api/v1/me/saves/versions/{id}               delete it
func (s *Server) registerSaves(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/saves", s.auth(s.handleSavesList))
	mux.HandleFunc("GET /api/v1/me/saves/versions/{id}", s.auth(s.handleSaveDownload))
	mux.HandleFunc("POST /api/v1/me/saves/versions/{id}/restore", s.auth(s.handleSaveRestore))
	mux.HandleFunc("DELETE /api/v1/me/saves/versions/{id}", s.auth(s.handleSaveDelete))
	mux.HandleFunc("GET /api/v1/me/saves/{platform}/{title}", s.auth(savesRetired))
	mux.HandleFunc("PUT /api/v1/me/saves/{platform}/{title}", s.auth(savesRetired))
	mux.HandleFunc("DELETE /api/v1/me/saves/{platform}/{title}", s.auth(s.handleSaveDeleteTitle))
	s.registerSavesStorage(mux)
}

func savesErr(w http.ResponseWriter, err error) {
	var se *saves.Error
	switch {
	case errors.Is(err, saves.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "cloud saves are not available on this deployment")
	case errors.As(err, &se) && se.Code == "quota_exceeded":
		writeJSON(w, http.StatusInsufficientStorage, map[string]any{"error": "quota_exceeded", "code": "quota_exceeded", "used": se.Used, "limit": se.Limit,
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
	// The game's name and icon, the same the website's saves page shows.
	type game struct {
		saves.Save
		Name string `json:"name"`
		Icon string `json:"icon,omitempty"` // a path on this site
	}
	out := make([]game, 0, len(list))
	for _, sv := range list {
		g := game{Save: sv, Name: sv.TitleID}
		if s.SaveTitle != nil {
			var icon bool
			if g.Name, icon = s.SaveTitle(r.Context(), sv.Platform, sv.TitleID); icon {
				g.Icon = "/titles/icon/" + sv.TitleID
			}
		}
		out = append(out, g)
	}
	writeJSON(w, http.StatusOK, map[string]any{"saves": out, "usage": usage})
}

// versionID is the {id} of a version route, or a 404 already written.
func versionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such version")
	}
	return id, err == nil
}

func (s *Server) handleSaveDownload(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	id, ok := versionID(w, r)
	if !ok {
		return
	}
	resp, err := s.Saves.Download(r.Context(), tok.AccountID, id)
	if err != nil {
		savesErr(w, err)
		return
	}
	defer resp.Body.Close()
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleSaveRestore(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if id, ok := versionID(w, r); ok {
		if err := s.Saves.Restore(r.Context(), tok.AccountID, id); err != nil {
			savesErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleSaveDelete(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if id, ok := versionID(w, r); ok {
		if err := s.Saves.Delete(r.Context(), tok.AccountID, id); err != nil {
			savesErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// savesRetired answers the emulator save routes: the cloud is console archives only.
func savesRetired(w http.ResponseWriter, _ *http.Request, _ store.APIToken) {
	writeError(w, http.StatusGone, "emulator saves are retired; OpenPak's Save Data Cloud is console archives only")
}

// handleSaveDeleteTitle deletes every backup of a title for the token's own account. The app
// asks the player to confirm first; the token is the authentication.
func (s *Server) handleSaveDeleteTitle(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	n, err := s.Saves.DeleteTitle(r.Context(), tok.AccountID, r.PathValue("platform"), r.PathValue("title"))
	if err != nil {
		var se *saves.Error
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			writeError(w, http.StatusNotFound, "no backups for this title")
			return
		}
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
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
	mux.HandleFunc("GET /api/v1/me/saves/storage/kinds", s.auth(s.handleStorageKinds))
	mux.HandleFunc("GET /api/v1/me/saves/storage/oauth/{provider}/url", s.auth(s.handleStorageOAuthURL))
}

func (s *Server) handleStorageKinds(w http.ResponseWriter, r *http.Request, _ store.APIToken) {
	kinds, err := s.Saves.Kinds(r.Context())
	if err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kinds)
}

// handleStorageOAuthURL gives the app a consent URL to open in a browser. The state names the
// account and is signed with the website secret, so the website's own callback finishes the
// connection without a web session: the player just sees the site say it is done.
func (s *Server) handleStorageOAuthURL(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	provider := r.PathValue("provider")
	scheme := "https"
	if r.TLS == nil && os.Getenv("WEBSITE_ENVIRONMENT") != "production" {
		scheme = "http"
	}
	redirect := accounts.StorageOAuthCallback(scheme, r.Host, provider)
	u, err := s.Saves.OAuthURL(r.Context(), provider, redirect, accounts.StorageOAuthState(s.Sessions, tok.AccountID))
	if err != nil {
		savesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
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
	var cfg saves.StorageConfig
	if !decodeSmall(w, r, &cfg) {
		return
	}
	if cfg.Kind == "" {
		cfg.Kind = "s3"
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
