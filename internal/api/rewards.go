package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// Rewards for the app, the same screens as /account/rewards. The website reaches the rewards service
// server-side with the account's core session; so do these, with the session the bearer is bound to.
// Texts come back worded in the caller's language (Accept-Language or ?lang=), pictures as site paths.
//
//	GET  /api/v1/me/rewards            header, missions as tiles, points history, shop, My items
//	GET  /api/v1/me/rewards/theme      the equipped theme's validated tokens, or {"theme":null}
//	POST /api/v1/me/rewards/claim      {"mission","period"} -> {"points"}
//	POST /api/v1/me/rewards/redeem     {"item"} (Idempotency-Key header optional) -> {"ok":true}
//	POST /api/v1/me/rewards/equip      {"slot","item"}; item "" empties the slot
//	POST /api/v1/me/rewards/settings   {"showcase","paused"}
//
// A theme preview needs no account: GET /themes/{id} already serves any shop theme's tokens.

func (s *Server) registerRewards(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	g("/me/rewards", s.auth(s.handleRewards))
	g("/me/rewards/theme", s.auth(s.handleRewardsTheme))
	h("/me/rewards/claim", s.auth(s.handleRewardsClaim))
	h("/me/rewards/redeem", s.auth(s.handleRewardsRedeem))
	h("/me/rewards/equip", s.auth(s.handleRewardsEquip))
	h("/me/rewards/settings", s.auth(s.handleRewardsSettings))
}

func apiLang(r *http.Request) string {
	if l := i18n.Match(r.URL.Query().Get("lang")); l != "" {
		return l
	}
	return i18n.Lang(r.Header.Get("Accept-Language"))
}

// rewardsOff answers 503 when this deployment has no rewards service.
func (s *Server) rewardsOff(w http.ResponseWriter) bool {
	if s.Pages == nil || s.Rewards == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rewards are not available on this deployment", "code": "rewards_unavailable"})
		return true
	}
	return false
}

func (s *Server) rewardsFailed(w http.ResponseWriter, what string, err error) {
	status, code := pages.RewardsFailure(err)
	if status == http.StatusBadGateway {
		s.log.Error("api: rewards "+what, "err", err)
	}
	writeJSON(w, status, map[string]string{"error": "rewards: " + what + " failed", "code": code})
}

func (s *Server) handleRewards(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	v, err := s.Pages.RewardsFor(r.Context(), apiLang(r), tok.CoreSessionID)
	if err != nil {
		s.rewardsFailed(w, "read", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRewardsTheme(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"theme": s.Pages.EquippedThemeTokens(r.Context(), tok.CoreSessionID)})
}

func (s *Server) handleRewardsClaim(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	var in struct{ Mission, Period string }
	if !decodeSmall(w, r, &in) {
		return
	}
	n, err := s.Rewards.Claim(r.Context(), tok.CoreSessionID, in.Mission, in.Period)
	if err != nil {
		s.rewardsFailed(w, "claim", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"points": n})
}

func (s *Server) handleRewardsRedeem(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	var in struct{ Item string }
	if !decodeSmall(w, r, &in) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		key = hex.EncodeToString(b)
	}
	if err := s.Rewards.Redeem(r.Context(), tok.CoreSessionID, in.Item, key); err != nil {
		s.rewardsFailed(w, "purchase", err)
		return
	}
	s.Pages.ForgetTheme(tok.AccountID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRewardsEquip(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	var in struct{ Slot, Item string }
	if !decodeSmall(w, r, &in) {
		return
	}
	if in.Slot == "" {
		writeError(w, http.StatusBadRequest, "slot is required")
		return
	}
	if err := s.Rewards.Equip(r.Context(), tok.CoreSessionID, in.Slot, in.Item); err != nil {
		s.rewardsFailed(w, "equip", err)
		return
	}
	s.Pages.ForgetTheme(tok.AccountID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRewardsSettings(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.rewardsOff(w) {
		return
	}
	var in struct{ Showcase, Paused bool }
	if !decodeSmall(w, r, &in) {
		return
	}
	if err := s.Rewards.Settings(r.Context(), tok.CoreSessionID, in.Showcase, in.Paused); err != nil {
		s.rewardsFailed(w, "settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
