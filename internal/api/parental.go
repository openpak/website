package api

// Parental controls, the app's half. The console-facing API is nx-baas's
// (`api-<env>.pctl.srv.nintendo.net/moon/v1/…`, contract in the docs repo at
// docs/switch/parental-controls-firmware-2026-09-26.md); this is the parent side, which
// is ours to shape because nothing in the firmware reads it.
//
//	GET  /api/v1/me/parental-controls          the consoles this account manages
//	POST /api/v1/me/parental-controls/pairing  mint the code to type on a console
//
// Nintendo's own parent surface is a smartphone app talking to the same moon API;
// ours is the OpenPak app talking to this.

import (
	"errors"
	"net/http"

	"github.com/openpak/website/internal/pctl"
	"github.com/openpak/website/internal/store"
)

func (s *Server) registerParental(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/parental-controls", s.auth(s.handleParentalDevices))
	mux.HandleFunc("POST /api/v1/me/parental-controls/pairing", s.auth(s.handleParentalPair))
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
	p, err := s.Parental.Pair(r.Context(), tok.AccountID)
	if err != nil {
		parentalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}
