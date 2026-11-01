package pages

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/rewards"
)

// /account/rewards: points, missions, the shop and the inventory of the signed-in account, read
// and changed through the rewards service with the person's own session as the credential, and
// /admin/rewards: the abuse flags queue. Rewards is optional; without it the page says so.

type missionView struct {
	rewards.Mission
	Label    string // the progress as "12 min of 30 min"
	Percent  int
	Reward   string
	CanClaim bool
}

type rewardsPage struct {
	baseData
	Available bool
	Failed    bool
	Me        rewards.Me
	Missions  []missionView
	Shop      []rewards.Item
	Inventory []rewards.Item
	Ledger    []rewards.LedgerEntry
}

var rewardsMessages = map[string]string{
	"claimed": "rewards.msg.claimed", "redeemed": "rewards.msg.redeemed", "saved": "rewards.msg.saved",
	"equipped": "rewards.msg.equipped", "nopoints": "rewards.err.points", "owned": "rewards.err.owned",
	"frozen": "rewards.err.frozen", "failed": "rewards.err.failed",
}

func (s *Server) handleRewards(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "rewards.title"))
	if key, ok := rewardsMessages[r.URL.Query().Get("m")]; ok {
		d.Flash = i18n.T(d.Lang, key)
	}
	page := rewardsPage{baseData: d, Available: s.Rewards != nil}
	if s.Rewards != nil {
		ctx, sid := r.Context(), v.CoreSessionID
		me, err := s.Rewards.Me(ctx, sid)
		if err != nil {
			s.log.Warn("rewards: me", "err", err)
			page.Failed = true
		} else {
			page.Me = me
			ms, _ := s.Rewards.Missions(ctx, sid)
			for _, m := range ms {
				page.Missions = append(page.Missions, missionOf(d.Lang, m))
			}
			page.Shop, _ = s.Rewards.Catalog(ctx, sid)
			page.Inventory, _ = s.Rewards.Inventory(ctx, sid)
			page.Ledger, _ = s.Rewards.Ledger(ctx, sid, 15)
			for i := range page.Inventory {
				page.Inventory[i].Equipped = me.Equipped[page.Inventory[i].Kind] == page.Inventory[i].ID
			}
		}
	}
	s.render(w, http.StatusOK, "rewards.html", page)
}

func missionOf(lang string, m rewards.Mission) missionView {
	mv := missionView{Mission: m, CanClaim: m.Claimable && !m.Claimed && !m.Locked}
	if m.Target > 0 {
		mv.Percent = int(min(m.Progress, m.Target) * 100 / m.Target)
	}
	if strings.HasSuffix(m.Metric, "seconds") {
		mv.Label = Duration(lang, m.Progress) + " / " + Duration(lang, m.Target)
	} else {
		mv.Label = fmt.Sprintf("%d / %d", m.Progress, m.Target)
	}
	if m.RewardPoints > 0 {
		mv.Reward = fmt.Sprintf(i18n.T(lang, "rewards.points"), m.RewardPoints)
	}
	return mv
}

// rewardsAction runs one POST against the service and goes back to the page with a message code.
func (s *Server) rewardsAction(w http.ResponseWriter, r *http.Request, do func(sid string) error, ok string) {
	v, good := s.postGuard(w, r)
	if !good {
		return
	}
	if s.Rewards == nil {
		redirectMsg(w, r, "/account/rewards", "failed")
		return
	}
	msg := ok
	if err := do(v.CoreSessionID); err != nil {
		msg = "failed"
		var re *rewards.Error
		if errors.As(err, &re) {
			switch re.Status {
			case http.StatusPaymentRequired:
				msg = "nopoints"
			case http.StatusConflict:
				msg = "owned"
			case http.StatusForbidden:
				msg = "frozen"
			}
		}
		s.log.Info("rewards action", "err", err)
	}
	redirectMsg(w, r, "/account/rewards", msg)
}

func (s *Server) handleRewardsClaim(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, func(sid string) error {
		_, err := s.Rewards.Claim(r.Context(), sid, r.PostFormValue("mission"), r.PostFormValue("period"))
		return err
	}, "claimed")
}

func (s *Server) handleRewardsRedeem(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, func(sid string) error {
		// One key per form render would be ideal; a fresh one per click still makes a double submit of
		// the same request harmless only if the form carries it, so the form does (field "key").
		key := r.PostFormValue("key")
		if key == "" {
			b := make([]byte, 12)
			_, _ = rand.Read(b)
			key = hex.EncodeToString(b)
		}
		return s.Rewards.Redeem(r.Context(), sid, r.PostFormValue("item"), key)
	}, "redeemed")
}

func (s *Server) handleRewardsEquip(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, func(sid string) error {
		return s.Rewards.Equip(r.Context(), sid, r.PostFormValue("slot"), r.PostFormValue("item"))
	}, "equipped")
}

func (s *Server) handleRewardsSettings(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, func(sid string) error {
		return s.Rewards.Settings(r.Context(), sid, r.PostFormValue("showcase") == "1", r.PostFormValue("paused") == "1")
	}, "saved")
}

// handleRewardsPreview proxies an item's preview image: the CSP only lets pages load images from here.
func (s *Server) handleRewardsPreview(w http.ResponseWriter, r *http.Request) {
	body, ct, err := s.Rewards.Preview(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(ct, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

type adminRewardsPage struct {
	baseData
	Available bool
	Failed    bool
	Stats     rewards.Stats
	Flags     []rewards.Flag
	All       bool
}

// handleAdminRewards is the abuse queue first: the open flags with a resolve form, and the totals.
func (s *Server) handleAdminRewards(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	if !slices.Contains(s.AdminAccountIDs, v.AccountID) {
		http.NotFound(w, r)
		return
	}
	d := s.data(r, v, "Rewards")
	page := adminRewardsPage{baseData: d, Available: s.Rewards != nil, All: r.URL.Query().Get("all") == "1"}
	if s.Rewards != nil {
		var err error
		if page.Stats, err = s.Rewards.AdminStats(r.Context(), v.AccountID); err != nil {
			s.log.Warn("rewards admin stats", "err", err)
			page.Failed = true
		} else if page.Flags, err = s.Rewards.AdminFlags(r.Context(), v.AccountID, page.All); err != nil {
			page.Failed = true
		}
	}
	s.render(w, http.StatusOK, "admin_rewards.html", page)
}

func (s *Server) handleAdminRewardsResolve(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if !slices.Contains(s.AdminAccountIDs, v.AccountID) {
		http.Error(w, "admins only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	note := strings.TrimSpace(r.PostFormValue("note"))
	if id <= 0 || s.Rewards == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.Rewards.ResolveFlag(r.Context(), v.AccountID, id, note); err != nil {
		s.log.Error("rewards resolve flag", "err", err)
		http.Error(w, "could not resolve the flag", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/admin/rewards", http.StatusSeeOther)
}
