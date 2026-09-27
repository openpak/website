package api

// Parental controls, the app's half. The console-facing API is nx-baas's
// (`api-<env>.pctl.srv.nintendo.net/moon/v1/…`, contract in the docs repo at
// docs/switch/parental-controls-firmware-2026-09-26.md); this is the parent side, which
// is ours to shape because nothing in the firmware reads it.
//
//	GET  /api/v1/me/parental-controls                                  the consoles this account manages
//	POST /api/v1/me/parental-controls/pairing                          mint the code to type on a console
//	GET  /api/v1/me/parental-controls/{moonID}/pin                     one console's PIN
//	GET  /api/v1/me/parental-controls/{moonID}/players/{playerID}/image a player's picture
//	POST /api/v1/me/parental-controls/unlock                           check the password before the app opens its screens
//
// Nintendo's own parent surface is a smartphone app talking to the same moon API;
// ours is the OpenPak app talking to this.

import (
	"errors"
	"net/http"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/pctl"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/store"
)

// parentalUnlocks caps password checks per account: the unlock answers nothing
// but yes or no, which is exactly what a guesser needs.
var parentalUnlocks = ratelimit.New(5, time.Minute)

func (s *Server) registerParental(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/parental-controls", s.auth(s.handleParentalDevices))
	mux.HandleFunc("POST /api/v1/me/parental-controls/pairing", s.auth(s.handleParentalPair))
	mux.HandleFunc("GET /api/v1/me/parental-controls/{moonID}/pin", s.auth(s.handleParentalPin))
	mux.HandleFunc("GET /api/v1/me/parental-controls/{moonID}/players/{playerID}/image", s.auth(s.handleParentalPlayerImage))
	mux.HandleFunc("POST /api/v1/me/parental-controls/unlock", s.auth(s.handleParentalUnlock))
}

// parentalErr: an unconfigured deployment is not a failure the app can act on,
// so it is said plainly and separately from a service that answered badly.
func parentalErr(w http.ResponseWriter, err error) {
	var pe *pctl.Error
	switch {
	case errors.Is(err, pctl.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "parental controls are not available on this deployment")
	case errors.As(err, &pe):
		writeError(w, http.StatusBadGateway, pe.Code)
	default:
		writeError(w, http.StatusBadGateway, "parental controls service error")
	}
}

func (s *Server) handleParentalDevices(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	devices, err := s.Parental.Devices(r.Context(), tok.AccountID)
	if err != nil {
		parentalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// handleParentalPair mints a code. It is deliberately not idempotent: asking
// again is how someone who walked away from the console gets a fresh code, and
// the old one stops being typed the moment it expires.
func (s *Server) handleParentalPair(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	// The console names the parent on its confirmation screen. A failed lookup
	// costs the name, not the pairing.
	name := ""
	if acct, err := s.Identity.GetAccount(r.Context(), tok.AccountID); err == nil {
		name = acct.DisplayName
	}
	p, err := s.Parental.Pair(r.Context(), tok.AccountID, name)
	if err != nil {
		parentalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// handleParentalPin is one console's PIN. On the app the phone's own unlock is
// the gate; the list never carries it, so it is fetched only when shown. A
// console that is not this account's is a 404, the same as one with no PIN yet.
func (s *Server) handleParentalPin(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	w.Header().Set("Cache-Control", "no-store")
	code, err := s.Parental.UnlockCode(r.Context(), tok.AccountID, r.PathValue("moonID"))
	if err != nil {
		parentalErr(w, err)
		return
	}
	if code == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"unlock_code": code})
}

// handleParentalPlayerImage relays a player's picture. nx-baas checks that the
// console is this account's and answers 404 otherwise.
func (s *Server) handleParentalPlayerImage(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	img, err := s.Parental.PlayerImage(r.Context(), tok.AccountID, r.PathValue("moonID"), r.PathValue("playerID"))
	var pe *pctl.Error
	if errors.As(err, &pe) && pe.Status == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		parentalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(img)
}

// handleParentalUnlock is the app's fallback where the phone has no lock of its
// own: the account password, checked the way every step-up here checks it.
// A second factor is not asked for: this opens a screen on a phone already
// signed in, and changes nothing. The body is never logged.
func (s *Server) handleParentalUnlock(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if !parentalUnlocks.Allow(tok.AccountID) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	_, err := s.Identity.VerifyPasswordByID(r.Context(), tok.AccountID, req.Password)
	switch {
	case err == nil, errors.Is(err, core.ErrSecondFactorRequired):
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, core.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "wrong password")
	default:
		s.log.Error("api parental unlock", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
	}
}
