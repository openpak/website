package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/openpak/website/internal/store"
)

// The catalog: titles and features live in the requests table, and these routes are the only
// way to change them besides the admin forms (which call the same store methods). Writes need
// a signed-in admin (WEBSITE_ADMIN_ACCOUNT_IDS) or the WEBSITE_CATALOG_KEY bearer.
func (s *Server) registerCatalog(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/catalog.json", s.handleCatalogExport)
	mux.HandleFunc("POST /api/v1/admin/titles", s.catalogWriter(s.handleCreateTitle))
	mux.HandleFunc("PATCH /api/v1/admin/titles/{id}", s.catalogWriter(s.handleUpdateTitle))
	mux.HandleFunc("DELETE /api/v1/admin/titles/{id}", s.catalogWriter(s.handleDeleteTitle))
}

// catalogWriter resolves who is writing: "api-key" for the bearer, or the admin's account id.
// A session caller must also send the page's CSRF token as X-CSRF-Token, so another site cannot
// write with an admin's cookie.
func (s *Server) catalogWriter(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			key := os.Getenv("WEBSITE_CATALOG_KEY")
			// An unset key is no bearer path at all, not an empty secret anyone can send.
			if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+key)) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r, "api-key")
			return
		}
		id := s.Sessions.CoreSessionID(r)
		if id == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ws, err := s.DB.GetWebSession(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !slices.Contains(s.AdminIDs, ws.AccountID) || !s.Sessions.CheckCSRF(id, r.Header.Get("X-CSRF-Token")) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r, ws.AccountID)
	}
}

// titleBody is the create and PATCH body. Pointers tell "absent" from "empty" for PATCH.
type titleBody struct {
	Kind     *string `json:"kind"`
	Name     *string `json:"name"`
	Console  *string `json:"console"`
	Category *string `json:"category"`
	Backend  *string `json:"backend"`
	Status   *string `json:"status"`
	Notes    *string `json:"notes"`
	Sort     *int    `json:"sort"`
	TitleID  *string `json:"title_id"`
	// TitleIDs is every id the game has, primary first; it replaces the list.
	TitleIDs *[]string `json:"title_ids"`
}

// decodeStrict reads a small JSON body and refuses unknown fields: a misspelt "stauts" must
// fail, not quietly change nothing.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleCreateTitle(w http.ResponseWriter, r *http.Request, actor string) {
	var b titleBody
	if !decodeStrict(w, r, &b) {
		return
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	req := store.Request{Kind: str(b.Kind), Name: str(b.Name), Console: str(b.Console), Category: str(b.Category),
		Backend: str(b.Backend), Status: str(b.Status), Notes: str(b.Notes), TitleID: str(b.TitleID), Sort: -1}
	if b.TitleIDs != nil {
		req.TitleIDs = *b.TitleIDs
		if req.TitleID != "" {
			req.TitleIDs = append([]string{req.TitleID}, req.TitleIDs...)
		}
	}
	if b.Sort != nil {
		if *b.Sort < 0 {
			writeError(w, http.StatusBadRequest, "sort must not be negative")
			return
		}
		req.Sort = *b.Sort
	}
	created, err := s.DB.CreateRequest(r.Context(), req, actor)
	if err != nil {
		s.catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, requestJSON(created))
}

func (s *Server) handleUpdateTitle(w http.ResponseWriter, r *http.Request, actor string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	var b titleBody
	if !decodeStrict(w, r, &b) {
		return
	}
	if b.Kind != nil {
		writeError(w, http.StatusBadRequest, "kind cannot change; delete and create instead")
		return
	}
	if b.Sort != nil && *b.Sort < 0 {
		writeError(w, http.StatusBadRequest, "sort must not be negative")
		return
	}
	updated, err := s.DB.UpdateRequest(r.Context(), id, store.RequestPatch{Name: b.Name, Console: b.Console,
		Category: b.Category, Backend: b.Backend, Status: b.Status, Notes: b.Notes, Sort: b.Sort,
		TitleID: b.TitleID, TitleIDs: b.TitleIDs}, actor)
	if err != nil {
		s.catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, requestJSON(updated))
}

func (s *Server) handleDeleteTitle(w http.ResponseWriter, r *http.Request, actor string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	if err := s.DB.DeleteRequest(r.Context(), id, actor); err != nil {
		s.catalogError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) catalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such request")
	case errors.Is(err, store.ErrDuplicate), errors.Is(err, store.ErrHasVotes):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("api: catalog write", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// requestJSON is one request as the API shows it, without the per-viewer vote fields.
func requestJSON(q store.Request) map[string]any {
	return map[string]any{
		"id": q.ID, "kind": q.Kind, "name": q.Name, "console": q.Console,
		"category": q.Category, "backend": q.Backend, "status": q.Status, "notes": q.Notes,
		"sort": q.Sort, "title_id": q.TitleID, "title_ids": titleIDs(q), "date_added": q.DateAdded.UTC().Format(time.RFC3339),
		"last_updated": q.LastUpdated.UTC().Format(time.RFC3339),
	}
}

// titleIDs is a request's ids for JSON: never null, and never missing the primary.
func titleIDs(q store.Request) []string {
	if len(q.TitleIDs) == 0 && q.TitleID != "" {
		return []string{q.TitleID}
	}
	if q.TitleIDs == nil {
		return []string{}
	}
	return q.TitleIDs
}

// handleCatalogExport: GET /api/v1/catalog.json → every title and feature in the old
// catalog.json shape (a bare array, catalog order) plus the two dates, for anything that still
// reads the file.
func (s *Server) handleCatalogExport(w http.ResponseWriter, r *http.Request) {
	if !s.PerToken.Allow("ip:" + clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	reqs, err := s.DB.ListRequests(r.Context(), "", "")
	if err != nil {
		s.log.Error("api: catalog export", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(reqs))
	for _, q := range reqs {
		m := requestJSON(q)
		delete(m, "id")
		delete(m, "sort")
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}
