package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/pages"
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
//	GET  /api/v1/me/wiiu/console-password  {nnid, pid, mode: sync|custom|incompatible|"", min, max}: the ID to type on a Wii U/3DS
//	POST /api/v1/me/wiiu/console-password  {current_password, code?, console_password}: a separate console password, only for an OpenPak password a Wii U cannot take
//	GET  /ca.pem                           the OpenPak CA, so a client can pin it instead of trusting anything
func (s *Server) registerEmulator(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/switch", s.auth(s.handleSwitch))
	mux.HandleFunc("POST /api/v1/me/switch/friend", s.auth(s.handleSwitchFriend))
	mux.HandleFunc("POST /api/v1/me/switch/link", s.auth(s.handleSwitchLink))
	mux.HandleFunc("GET /api/v1/me/wiiu", s.auth(s.handleConsole("wiiu")))
	mux.HandleFunc("GET /api/v1/me/3ds", s.auth(s.handleConsole("3ds")))
	mux.HandleFunc("GET /api/v1/me/wiiu/console-password", s.auth(s.handleConsolePasswordStatus))
	mux.HandleFunc("POST /api/v1/me/wiiu/console-password", s.auth(s.handleConsolePasswordSet))
	mux.HandleFunc("GET /ca.pem", s.handleCA)
}

func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	out, err := s.Adapter.Emulator(r.Context(), tok.AccountID)
	if err != nil {
		s.log.Error("emulator identity", "err", err)
		writeError(w, http.StatusBadGateway, "switch identity unavailable")
		return
	}
	s.labelSwitchFriends(r.Context(), out)
	writeJSON(w, http.StatusOK, out)
}

// labelSwitchFriends gives each friend's presence in the adapter's answer the
// same platform/client/label/title_name the rest of the API carries. The
// adapter knows the client; only the website knows every title's name.
func (s *Server) labelSwitchFriends(ctx context.Context, out map[string]any) {
	friends, _ := out["friends"].([]any)
	var catalogue map[string]string
	for _, f := range friends {
		fm, _ := f.(map[string]any)
		p, _ := fm["presence"].(map[string]any)
		if p == nil {
			continue
		}
		ns, _ := p["namespace"].(string)
		client, _ := p["client"].(string)
		titleID, _ := p["app_id"].(string)
		if _, ok := p["platform"]; !ok {
			p["platform"] = ns
		}
		p["client"] = client
		p["label"] = core.ClientLabel(ns, client)
		name := ""
		if titleID != "" {
			if catalogue == nil {
				catalogue = pages.TitleCatalogue(ctx, s.DB)
			}
			name, _ = pages.FriendGame(ctx, catalogue, ns, titleID)
		}
		p["title_name"] = name
	}
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

// handleConsolePasswordStatus: the Network ID to type on a Wii U or 3DS and where its password
// comes from. The OpenPak password is the console password ("sync"); a separate one is the
// fallback for a password a Wii U cannot take.
func (s *Server) handleConsolePasswordStatus(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.NNAdapter == nil {
		writeError(w, http.StatusServiceUnavailable, "wii u identities are not configured")
		return
	}
	out, err := s.NNAdapter.ConsolePasswordStatus(r.Context(), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no wii u id yet")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleConsolePasswordSet sets a separate console password. It asks for the OpenPak password
// again, and a code when the account has two-factor authentication, like deleting an account.
func (s *Server) handleConsolePasswordSet(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.NNAdapter == nil {
		writeError(w, http.StatusServiceUnavailable, "wii u identities are not configured")
		return
	}
	var req struct {
		Current string `json:"current_password"`
		Code    string `json:"code"`
		Console string `json:"console_password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Current == "" || req.Console == "" {
		writeError(w, http.StatusBadRequest, "current_password and console_password are required")
		return
	}
	nnid, err := accounts.SetConsolePassword(r.Context(), s.Identity, s.NNAdapter, tok.AccountID, req.Current, req.Code, req.Console)
	var refused *adapter.ErrConsolePassword
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nnid": nnid})
	case errors.As(err, &refused):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "console password breaks the rules", "code": refused.Reason})
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
	case errors.Is(err, core.ErrSecondFactorRequired):
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this account has two-factor authentication: send a code", "code": "two_factor_required", "two_factor_required": true})
	case errors.Is(err, core.ErrInvalidCode):
		writeError(w, http.StatusForbidden, "wrong code")
	case errors.Is(err, core.ErrTwoFactorLocked):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many wrong codes; try again in 15 minutes")
	default:
		s.log.Error("api console password", "err", err)
		writeError(w, http.StatusBadGateway, "console password unavailable")
	}
}
