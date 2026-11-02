package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/discordlink"
	"github.com/openpak/website/internal/store"
)

// PakkaNotice answers whether an account accepted Pakka's data-use notice.
// The acceptance is kept by the Pakka chat service, which is not merged yet;
// until a source is set on Server.PakkaNotice the lookup answers null for it,
// and Pakka's gate treats null as "not accepted".
type PakkaNotice interface {
	NoticeAccepted(ctx context.Context, accountID string) (bool, error)
}

// The Discord lookup (docs/discord-link.md): Pakka on Discord asks who a
// Discord user is before it answers them. Internal, so not in the OpenAPI.
func (s *Server) registerDiscord(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/discord/{discord_id}", s.handleDiscordLookup)
}

// handleDiscordLookup answers {linked:false} for a Discord id nobody linked,
// and the account facts Pakka needs otherwise. Every failure to find out is a
// non-200 (or linked:false for an account that is not active), never a guess
// that lets someone through.
func (s *Server) handleDiscordLookup(w http.ResponseWriter, r *http.Request) {
	if !internalAuthorized(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("discord_id")
	if !discordlink.ValidID(id) {
		writeError(w, http.StatusBadRequest, "not a Discord user id")
		return
	}
	ctx := r.Context()
	unlinked := map[string]any{"linked": false}
	l, err := s.DB.DiscordLinkByDiscordID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, unlinked)
		return
	}
	if err != nil {
		s.log.Error("discord lookup: link", "err", err)
		writeError(w, http.StatusBadGateway, "the link store did not answer")
		return
	}
	acct, err := s.Identity.GetAccount(ctx, l.AccountID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeJSON(w, http.StatusOK, unlinked)
		return
	case err != nil:
		s.log.Error("discord lookup: account", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
		return
	case acct.Status != "" && acct.Status != "active":
		// Banned or closing accounts have no Pakka, as on the web.
		writeJSON(w, http.StatusOK, unlinked)
		return
	}
	var notice any // null: not known (see PakkaNotice)
	if s.PakkaNotice != nil {
		if ok, err := s.PakkaNotice.NoticeAccepted(ctx, l.AccountID); err != nil {
			s.log.Error("discord lookup: notice", "err", err)
		} else {
			notice = ok
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":       true,
		"account_id":   l.AccountID,
		"display_name": acct.DisplayName,
		// A child is an account that is not provably 18 or over (unknown or
		// invalid birthday included) or that a parent supervises; a parental
		// controls core that cannot answer counts as supervised.
		// Pakka also refuses anything this misses; see docs/discord-link.md.
		"supervised_or_minor":   !accounts.IsAdult(acct.Birthday, time.Now()) || s.Supervision.SupervisedOrUnknown(ctx, l.AccountID),
		"pakka_notice_accepted": notice,
	})
}
