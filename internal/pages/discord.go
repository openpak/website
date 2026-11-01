package pages

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/website/internal/discordlink"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// Linking a Discord account (docs/discord-link.md), so Pakka on the OpenPak Discord server can
// apply the account's rules: Discord OAuth2 with the identify scope, nothing else.
//
//	POST /account/link/discord           CSRF-checked; sends the person to Discord
//	GET  /account/link/discord/callback  Discord returns here with ?code&state
//	POST /account/link/discord/revoke    CSRF-checked; unlinks

const discordCookie = "opak_discord_state"

// discordRow is the Discord entry of the Linked platforms section.
type discordRow struct {
	Configured bool
	Linked     bool
	Username   string
	Msg        string // flash text for this section
	MsgOK      bool
}

func (s *Server) discordRedirectURI(r *http.Request) string {
	scheme := "https"
	if !s.Production {
		scheme = "http"
	}
	return scheme + "://" + r.Host + discordlink.CallbackPath
}

// discordState binds the OAuth round trip to this browser (the cookie nonce) and this account.
func (s *Server) discordState(nonce, accountID string) string {
	return nonce + "." + s.Sessions.HashToken("discord:"+nonce+":"+accountID)
}

// discordView fills the section for the account page.
func (s *Server) discordView(r *http.Request, accountID, lang string) discordRow {
	row := discordRow{Configured: s.Discord != nil}
	if l, err := s.DB.DiscordLinkByAccount(r.Context(), accountID); err == nil {
		row.Linked, row.Username = true, l.Username
	}
	switch r.URL.Query().Get("dm") {
	case "linked":
		row.Msg, row.MsgOK = i18n.T(lang, "discord.ok"), true
	case "unlinked":
		row.Msg, row.MsgOK = i18n.T(lang, "discord.removed"), true
	case "taken":
		row.Msg = i18n.T(lang, "discord.taken")
	case "cancelled":
		row.Msg = i18n.T(lang, "discord.cancelled")
	case "failed":
		row.Msg = i18n.T(lang, "discord.failed")
	}
	return row
}

func discordBack(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/account?dm="+url.QueryEscape(code)+"#devices", http.StatusSeeOther)
}

func (s *Server) handleDiscordStart(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.Discord == nil {
		http.Error(w, "Discord linking is not available", http.StatusServiceUnavailable)
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: discordCookie, Value: nonce, Path: discordlink.CallbackPath, MaxAge: 600,
		HttpOnly: true, Secure: s.Production, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.Discord.AuthURL(s.discordRedirectURI(r), s.discordState(nonce, v.AccountID)), http.StatusSeeOther)
}

func (s *Server) handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.Discord == nil {
		http.NotFound(w, r)
		return
	}
	// The cookie is spent whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: discordCookie, Value: "", Path: discordlink.CallbackPath, MaxAge: -1,
		HttpOnly: true, Secure: s.Production, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	c, err := r.Cookie(discordCookie)
	state := q.Get("state")
	nonce, _, _ := strings.Cut(state, ".")
	if err != nil || nonce == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(nonce)) != 1 ||
		subtle.ConstantTimeCompare([]byte(state), []byte(s.discordState(nonce, v.AccountID))) != 1 {
		discordBack(w, r, "failed")
		return
	}
	if q.Get("error") != "" {
		discordBack(w, r, "cancelled")
		return
	}
	ctx := r.Context()
	u, err := s.Discord.Identify(ctx, q.Get("code"), s.discordRedirectURI(r))
	if err != nil {
		s.log.Error("discord link: identify", "err", err)
		discordBack(w, r, "failed")
		return
	}
	switch err := s.DB.LinkDiscord(ctx, store.DiscordLink{AccountID: v.AccountID, DiscordID: u.ID, Username: u.Username}); {
	case errors.Is(err, store.ErrDiscordTaken):
		_ = s.DB.AddAudit(ctx, v.AccountID, "discord.link.refused", "discord_id="+u.ID+" already linked to another account")
		discordBack(w, r, "taken")
	case err != nil:
		s.log.Error("discord link: store", "err", err)
		discordBack(w, r, "failed")
	default:
		_ = s.DB.AddAudit(ctx, v.AccountID, "discord.link", "discord_id="+u.ID)
		discordBack(w, r, "linked")
	}
}

func (s *Server) handleDiscordRevoke(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	l, err := s.DB.UnlinkDiscord(r.Context(), v.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		discordBack(w, r, "unlinked")
	case err != nil:
		s.log.Error("discord unlink", "err", err)
		discordBack(w, r, "failed")
	default:
		_ = s.DB.AddAudit(r.Context(), v.AccountID, "discord.unlink", "discord_id="+l.DiscordID+" after "+time.Since(l.LinkedAt).Round(time.Second).String())
		discordBack(w, r, "unlinked")
	}
}
