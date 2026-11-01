package admin

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/rewards"
)

// The rewards abuse queue and totals (/admin/rewards), through the rewards service's admin routes
// (X-Admin-Key plus the acting admin's account id, which the service writes to its audit trail).
// Rewards is optional: without REWARDS_URL the section is left out of the nav, like the store.

type rewardsView struct {
	base
	Failed     bool
	Stats      rewards.Stats
	Flags      []flagRow
	All        bool
	Redemption []redemptionRow
}

type flagRow struct {
	rewards.Flag
	ReasonLabel string
}

type redemptionRow struct {
	Item  string
	Count int64
}

func (s *Server) handleRewards(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.Rewards == nil {
		http.NotFound(w, r)
		return
	}
	v := rewardsView{base: s.page(r, ws, i18n.T("en", "admin.rewards")), All: r.URL.Query().Get("all") == "1"}
	v.Flash = rewardsFlash(r.URL.Query().Get("m"))
	var err error
	if v.Stats, err = s.Rewards.AdminStats(r.Context(), ws.AccountID); err != nil {
		s.log.Warn("rewards admin stats", "err", err)
		v.Failed = true
	} else {
		var flags []rewards.Flag
		if flags, err = s.Rewards.AdminFlags(r.Context(), ws.AccountID, v.All); err != nil {
			s.log.Warn("rewards admin flags", "err", err)
			v.Failed = true
		}
		for _, f := range flags {
			label := i18n.T("en", "admin.rewards.reason."+f.Reason)
			if label == "admin.rewards.reason."+f.Reason {
				label = f.Reason
			}
			v.Flags = append(v.Flags, flagRow{f, label})
		}
	}
	for item, n := range v.Stats.Redemptions {
		v.Redemption = append(v.Redemption, redemptionRow{item, n})
	}
	sort.Slice(v.Redemption, func(a, b int) bool {
		if v.Redemption[a].Count != v.Redemption[b].Count {
			return v.Redemption[a].Count > v.Redemption[b].Count
		}
		return v.Redemption[a].Item < v.Redemption[b].Item
	})
	s.render(w, http.StatusOK, "admin_rewards.html", v)
}

func (s *Server) handleRewardsResolve(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if id <= 0 || s.Rewards == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	if err := s.Rewards.ResolveFlag(r.Context(), ws.AccountID, id, note); err != nil {
		s.log.Error("rewards resolve flag", "err", err)
		http.Error(w, "the rewards service refused the change", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/admin/rewards?m=resolved", http.StatusSeeOther)
}

func rewardsFlash(code string) string {
	if code == "resolved" {
		return i18n.T("en", "admin.rewards.flash.resolved")
	}
	return ""
}
