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
//	POST /api/v1/me/switch/link            {"client_id":…} -> {"id_token":…} for the emulated console's federation
//	GET  /ca.pem                           the OpenPak CA, so a client can pin it instead of trusting anything
func (s *Server) registerEmulator(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/switch", s.auth(s.handleSwitch))
	mux.HandleFunc("POST /api/v1/me/switch/friend", s.auth(s.handleSwitchFriend))
	mux.HandleFunc("POST /api/v1/me/switch/link", s.auth(s.handleSwitchLink))
	mux.HandleFunc("GET /api/v1/me/wiiu", s.auth(s.handleConsole("wiiu")))
	mux.HandleFunc("GET /api/v1/me/3ds", s.auth(s.handleConsole("3ds")))
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

// handleConsole is the Wii U or 3DS identity of the account (PNID, NEX credentials, Mii, and
// on the 3DS the friend code), minted by nn-account on first sight. Cemu and Azahar write it
// into their emulated console instead of needing a console dump.
func (s *Server) handleConsole(family string) func(http.ResponseWriter, *http.Request, store.APIToken) {
	return func(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
		if s.NNAdapter == nil {
			writeError(w, http.StatusServiceUnavailable, family+" identities are not configured")
			return
		}
		out, err := s.NNAdapter.Console(r.Context(), family, tok.AccountID)
		if err != nil {
			s.log.Error("console identity", "family", family, "err", err)
			writeError(w, http.StatusBadGateway, family+" identity unavailable")
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleSwitchLink hands an emulator the Nintendo-Account id_token its device account binds
// with, so signing in to the website once is also the console link — the same proof a console
// gets by typing the password into the link page, minted for an account already proved here.
func (s *Server) handleSwitchLink(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var in struct {
		ClientID string `json:"client_id"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	out, err := s.Adapter.LinkToken(r.Context(), tok.AccountID, in.ClientID)
	if err != nil {
		s.log.Error("emulator link token", "err", err)
		writeError(w, http.StatusBadGateway, "switch link unavailable")
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
