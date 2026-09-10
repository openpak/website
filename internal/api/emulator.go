package api

import (
	"net/http"

	"github.com/openpak/website/internal/store"
)

// The emulator surface. An emulator signs in with /api/v1/token like the app, then asks for the
// Switch identity of the account: the PID and friend code the games see, the openpak token its own
// id_token carries so title servers know who is playing, and the friend graph with presence.
// The adapter mints the identity on first sight, exactly as linking a console does.
//
//	GET  /api/v1/me/switch                 identity, token, friends, requests
//	POST /api/v1/me/switch/friend          {"action":"request","friend_code":…} | {"action":"accept"|"decline","pid":…}
//	GET  /ca.pem                           the OpenPak CA, so a client can pin it instead of trusting anything
func (s *Server) registerEmulator(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/switch", s.auth(s.handleSwitch))
	mux.HandleFunc("POST /api/v1/me/switch/friend", s.auth(s.handleSwitchFriend))
	mux.HandleFunc("GET /ca.pem", s.handleCA)
}

func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	out, err := s.Adapter.Emulator(r.Context(), tok.AccountID)
	if err != nil {
		s.log.Error("emulator identity", "err", err)
		writeError(w, http.StatusBadGateway, "switch identity unavailable")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSwitchFriend(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var in struct {
		Action     string `json:"action"`
		PID        uint64 `json:"pid"`
		FriendCode string `json:"friend_code"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	if err := s.Adapter.EmulatorFriend(r.Context(), tok.AccountID, in.Action, in.PID, in.FriendCode); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	pem, err := s.Adapter.CA(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "no CA available")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(pem)
}
