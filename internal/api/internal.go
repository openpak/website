package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// The internal surface: service-to-service, never a browser, never published
// in the OpenAPI. It exists because the console uploads its profile picture to
// the Switch adapter, and an avatar is an account fact — it belongs where the
// website and the app read avatars from, not stranded in one adapter.
//
// Registered directly on the mux rather than through the /api/v1 helpers, so
// the contract test does not expect to find it in the spec. That is the one
// case where bypassing that check is right: this is deliberately not a public
// route.
func (s *Server) registerInternal(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/avatar", s.handleInternalAvatar)
	mux.HandleFunc("GET /internal/titles/name", s.handleInternalTitleName)
}

// handleInternalTitleName names a title for an adapter drawing a friend who is
// playing on another platform ("Playing Mario Kart 8 on Wii U"), from the same
// sources the friends pages use. {"name": ""} is an answer: nobody knows it.
func (s *Server) handleInternalTitleName(w http.ResponseWriter, r *http.Request) {
	key := internalKey()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable, "internal surface is not configured")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ns, id := r.URL.Query().Get("namespace"), r.URL.Query().Get("title_id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "title_id is required")
		return
	}
	name, _ := pages.FriendGame(r.Context(), pages.TitleCatalogue(r.Context(), s.DB), ns, id)
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// internalKey is the shared secret. Empty disables the surface completely:
// a deployment that has not set one should refuse these calls, not accept
// them from anybody.
func internalKey() string { return os.Getenv("WEBSITE_INTERNAL_KEY") }

func (s *Server) handleInternalAvatar(w http.ResponseWriter, r *http.Request) {
	key := internalKey()
	if key == "" {
		writeError(w, http.StatusServiceUnavailable, "internal surface is not configured")
		return
	}
	// Constant time: this is a bearer secret, and a timing-visible compare on
	// a secret is worth avoiding even on a private network.
	got := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+key)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		// Namespace and Subject are how an adapter names an account without
		// knowing the core's id for it: the same pair the core's Links hold.
		Namespace string `json:"namespace"`
		Subject   string `json:"subject"`
		Content   string `json:"content_base64"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Namespace == "" || req.Subject == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "namespace, subject and content_base64 are required")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, "content_base64 is not base64")
		return
	}
	if s.Media == nil {
		writeError(w, http.StatusServiceUnavailable, "avatars are not configured")
		return
	}
	accountID, err := s.Links.AccountBySubject(r.Context(), req.Namespace, req.Subject)
	if err != nil {
		// An adapter can hold a subject the core has never heard of — a link
		// that was revoked, say. That is not an error worth 500ing over.
		s.log.Info("internal avatar: no account for subject", "namespace", req.Namespace)
		writeError(w, http.StatusNotFound, "no account for that subject")
		return
	}
	if err := s.Media.StoreAvatar(r.Context(), accountID, bytes.NewReader(raw)); err != nil {
		if errors.Is(err, media.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "not an image we can store")
			return
		}
		s.log.Error("internal avatar: store", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The bytes are only half of it. Every cache -- the browser, Cloudflare,
	// the console's own ?v= key -- keys off this row, so writing the picture
	// without stamping it leaves the site serving the previous avatar
	// against a validator that never moves. That is exactly what happened on
	// 2026-09-08: the file changed at 18:49 and the row still said the day
	// before.
	if err := s.DB.UpsertMedia(r.Context(), store.MediaRecord{
		AccountID: accountID, Kind: "avatar", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.log.Error("internal avatar: media metadata", "err", err)
	}
	s.log.Info("internal avatar stored", "account", accountID, "bytes", len(raw))
	writeJSON(w, http.StatusOK, map[string]any{"stored": true})
}
