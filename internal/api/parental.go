package api

// Parental controls, the app's half. The console-facing API is nx-baas's
// (`api-<env>.pctl.srv.nintendo.net/moon/v1/…`, contract in the docs repo at
// internal research notes); this is the parent side, which
// is ours to shape because nothing in the firmware reads it.
//
//	GET  /api/v1/me/parental-controls                                  the consoles this account manages
//	POST /api/v1/me/parental-controls/pairing                          mint the code to type on a console
//	GET  /api/v1/me/parental-controls/{moonID}/pin                     one console's PIN
//	GET  /api/v1/me/parental-controls/{moonID}/players/{playerID}/image a player's picture
//	POST /api/v1/me/parental-controls/unlock                           check the password before the app opens its screens
//	PUT  /api/v1/me/parental-controls/{moonID}/setting                 the whole setting document the console is served
//	PUT  /api/v1/me/parental-controls/{moonID}/pin                     a new PIN for the console
//	DELETE /api/v1/me/parental-controls/{moonID}                       take a console off the list
//	GET  /api/v1/me/parental-controls/{moonID}/activity?days=7         time played per day and per title
//	PUT  /api/v1/me/parental-controls/{moonID}/name                    the parent's name for a console
//
// Nintendo's own parent surface is a smartphone app talking to the same moon API;
// ours is the OpenPak app talking to this.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/pages"
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
	mux.HandleFunc("PUT /api/v1/me/parental-controls/{moonID}/setting", s.auth(s.handleParentalSetting))
	mux.HandleFunc("PUT /api/v1/me/parental-controls/{moonID}/pin", s.auth(s.handleParentalSetPin))
	mux.HandleFunc("DELETE /api/v1/me/parental-controls/{moonID}", s.auth(s.handleParentalRemove))
	mux.HandleFunc("GET /api/v1/me/parental-controls/{moonID}/activity", s.auth(s.handleParentalActivity))
	mux.HandleFunc("PUT /api/v1/me/parental-controls/{moonID}/name", s.auth(s.handleParentalName))
}

// notFound says whether nx-baas answered 404: no such console, or not this
// account's, which the caller must not be able to tell apart.
func notFound(err error) bool {
	var pe *pctl.Error
	return errors.As(err, &pe) && pe.Status == http.StatusNotFound
}

// handleParentalSetting stores the whole document the console is served. The
// app sends back what it was given with its changes applied; nx-baas refuses,
// rather than thins, anything the console's parse table would not take, and
// the refusal is passed on with the key it names.
func (s *Server) handleParentalSetting(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Setting map[string]any `json:"setting"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Setting == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "setting required", "code": "invalid_setting", "path": ""})
		return
	}
	saved, err := s.Parental.SetSetting(r.Context(), tok.AccountID, r.PathValue("moonID"), req.Setting)
	var pe *pctl.Error
	switch {
	case notFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &pe) && pe.Status == http.StatusBadRequest:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": pe.Code, "code": "invalid_setting", "path": pe.Path()})
	case err != nil:
		parentalErr(w, err)
	default:
		writeJSON(w, http.StatusOK, saved)
	}
}

// handleParentalSetPin gives the console a new PIN at its next sync. Exactly
// four digits: the console's entry field has four places. The body is never
// logged and the code is never in a URL.
func (s *Server) handleParentalSetPin(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		UnlockCode string `json:"unlock_code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if !pctl.ValidPin(req.UnlockCode) {
		writeError(w, http.StatusBadRequest, "the PIN must be four digits")
		return
	}
	err := s.Parental.SetUnlockCode(r.Context(), tok.AccountID, r.PathValue("moonID"), req.UnlockCode)
	var pe *pctl.Error
	switch {
	case notFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &pe) && pe.Status == http.StatusBadRequest:
		writeError(w, http.StatusBadRequest, "the PIN must be four digits")
	case err != nil:
		parentalErr(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleParentalRemove takes a console off the account's list. The console is
// not told: it stays linked on its side until it is unlinked there.
func (s *Server) handleParentalRemove(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	err := s.Parental.Remove(r.Context(), tok.AccountID, r.PathValue("moonID"))
	switch {
	case notFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case err != nil:
		parentalErr(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleParentalName names a console so a parent with two can tell them apart.
// The name stays with us: the console never sends or receives one.
func (s *Server) handleParentalName(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if !pctl.ValidName(name) {
		writeError(w, http.StatusBadRequest, "a console name is at most 32 characters, with no control characters")
		return
	}
	err := s.Parental.SetName(r.Context(), tok.AccountID, r.PathValue("moonID"), name)
	var pe *pctl.Error
	switch {
	case notFound(err):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &pe) && pe.Status == http.StatusBadRequest:
		writeError(w, http.StatusBadRequest, "a console name is at most 32 characters, with no control characters")
	case errors.Is(err, pctl.ErrNameUnsupported):
		writeError(w, http.StatusBadGateway, "naming a console is not available yet")
	case err != nil:
		parentalErr(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleParentalActivity is the time used per day, per title and per player.
// Titles are named from the titles catalogue and players from the console's
// own list; one either does not know has no name.
func (s *Server) handleParentalActivity(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	days := 7
	if q := r.URL.Query().Get("days"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 30 {
			writeError(w, http.StatusBadRequest, "days must be 1 to 30")
			return
		}
		days = n
	}
	out, err := s.Parental.Activity(r.Context(), tok.AccountID, r.PathValue("moonID"), days)
	switch {
	case notFound(err):
		writeError(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		parentalErr(w, err)
		return
	}
	pages.NameTitles(r.Context(), s.DB, out)
	// A failed lookup costs the players' names, not the activity.
	devices, _ := s.Parental.Devices(r.Context(), tok.AccountID)
	for _, d := range devices {
		if d.MoonID == r.PathValue("moonID") {
			pctl.NamePlayers(out, d.Players)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
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
