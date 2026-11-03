package pages

import (
	"context"
	"errors"
	"net/http"

	"github.com/openpak/website/internal/rewards"
)

// The Rewards screens as JSON, for the app (GET /api/v1/me/rewards). Everything the web page prints is already
// worded in the caller's language by buildRewards, so the app draws text it is given and never rebuilds
// the mission or ladder logic. Pictures are paths on this site ("/account/rewards/preview/<id>" and
// "/static/rewards/<art>.png"), public, so an image widget needs no credential.

// RewardsTile is one mission tile: a single mission, or a ladder showing the tier being worked on.
type RewardsTile struct {
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	Img         string       `json:"img"`
	State       string       `json:"state"` // progress, ready, earned or done
	Percent     int          `json:"percent"`
	Progress    string       `json:"progress"`
	Reward      string       `json:"reward,omitempty"`
	ClaimText   string       `json:"claim_text,omitempty"`
	MissionID   string       `json:"mission_id,omitempty"`
	Period      string       `json:"period,omitempty"`
	Pips        []bool       `json:"pips,omitempty"`
	Earned      int          `json:"earned"`
	Tiers       []RewardTier `json:"tiers,omitempty"`
	Single      bool         `json:"single"`
	Equipped    bool         `json:"equipped"`
	EquipItem   string       `json:"equip_item,omitempty"`
	Complete    bool         `json:"complete"`
}

// RewardTier is one rung of a ladder.
type RewardTier struct {
	Finish string `json:"finish"`
	Img    string `json:"img,omitempty"`
	Target string `json:"target"`
	Points string `json:"points,omitempty"`
	State  string `json:"state"` // earned, current or locked
	Ready  bool   `json:"ready"`
}

// RewardsItem is a shop or collection item.
type RewardsItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	KindLabel  string `json:"kind_label"`
	Name       string `json:"name"`
	Desc       string `json:"description"`
	Img        string `json:"img"`
	Wide       bool   `json:"wide"`
	Price      *int   `json:"price"`
	Need       int    `json:"need"`
	After      int    `json:"after"`
	Affordable bool   `json:"affordable"`
	Owned      bool   `json:"owned"`
	Equipped   bool   `json:"equipped"`
	Limited    bool   `json:"limited"`
	Where      string `json:"where"`
	Soon       bool   `json:"soon"`
	When       string `json:"when,omitempty"`
}

// RewardsGroup is a shelf of one kind.
type RewardsGroup struct {
	Kind  string        `json:"kind"`
	Label string        `json:"label"`
	Wide  bool          `json:"wide"`
	Tiles []RewardsTile `json:"tiles,omitempty"`
	Items []RewardsItem `json:"items"`
}

// RewardsEntry is one line of the points history.
type RewardsEntry struct {
	Img    string `json:"img"`
	Title  string `json:"title"`
	Sub    string `json:"sub"`
	Delta  int    `json:"delta"`
	Points string `json:"points"`
}

// RewardsSlot is an equip slot.
type RewardsSlot struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Img   string `json:"img,omitempty"`
}

// RewardsView is the whole Rewards screen set.
type RewardsView struct {
	Balance        int            `json:"balance"`
	Frozen         bool           `json:"frozen"`
	Paused         bool           `json:"paused"`
	ShowcasePublic bool           `json:"showcase_public"`
	TodayPoints    int            `json:"today_points"`
	TodayCap       int            `json:"today_cap"`
	TodayPercent   int            `json:"today_percent"`
	CapReached     bool           `json:"cap_reached"`
	More           int            `json:"more"`
	PlayTime       string         `json:"play_time"`
	Owned          int            `json:"owned"`
	Ready          int            `json:"ready"`
	Affordable     int            `json:"affordable"`
	NextKind       string         `json:"next_kind,omitempty"`
	NextText       string         `json:"next_text,omitempty"`
	Badge          *RewardsSlot   `json:"badge,omitempty"`
	Slots          []RewardsSlot  `json:"slots"`
	ReadyTiles     []RewardsTile  `json:"ready_tiles"`
	Tiles          []RewardsTile  `json:"tiles"`
	Done           []RewardsTile  `json:"done"`
	Ledger         []RewardsEntry `json:"ledger"`
	Shop           []RewardsGroup `json:"shop"`
	Upcoming       []RewardsItem  `json:"upcoming"`
	Mine           []RewardsGroup `json:"mine"`
}

func tileJSON(t tileView) RewardsTile {
	out := RewardsTile{Key: t.Key, Name: t.Name, Label: t.Label, Description: t.Description, Img: t.Img, State: t.State,
		Percent: t.Percent, Progress: t.Progress, Reward: t.Reward, ClaimText: t.ClaimText, MissionID: t.MissionID,
		Period: t.Period, Pips: t.Pips, Earned: t.Earned, Single: t.Single, Equipped: t.Equipped,
		EquipItem: t.EquipItem, Complete: t.Complete}
	for _, tr := range t.Tiers {
		rt := RewardTier{Finish: tr.Finish, Target: tr.Target, Points: tr.Points, State: tr.State, Ready: tr.Ready}
		if tr.Item != "" {
			rt.Img = previewURL(tr.Item)
		}
		out.Tiers = append(out.Tiers, rt)
	}
	return out
}

func tilesJSON(in []tileView) []RewardsTile {
	out := make([]RewardsTile, 0, len(in))
	for _, t := range in {
		out = append(out, tileJSON(t))
	}
	return out
}

func itemJSON(it itemView) RewardsItem {
	out := RewardsItem{ID: it.ID, Kind: it.Kind, KindLabel: it.KindLabel, Name: it.Name, Desc: it.Description,
		Img: previewURL(it.ID), Wide: it.Wide, Price: it.Price, Need: it.Need, After: it.After, Affordable: it.Affordable,
		Owned: it.Owned, Equipped: it.Equipped, Limited: it.Limited, Where: it.Where, Soon: it.Soon, When: it.When}
	return out
}

func groupsJSON(in []kindGroup) []RewardsGroup {
	out := make([]RewardsGroup, 0, len(in))
	for _, g := range in {
		rg := RewardsGroup{Kind: g.Kind, Label: g.Label, Wide: g.Wide, Tiles: tilesJSON(g.Tiles), Items: []RewardsItem{}}
		for _, it := range g.Items {
			rg.Items = append(rg.Items, itemJSON(it))
		}
		out = append(out, rg)
	}
	return out
}

// RewardsAvailable says whether this deployment has a rewards service at all.
func (s *Server) RewardsAvailable() bool { return s.Rewards != nil }

// RewardsFor builds the Rewards screens for one core session, worded in lang.
func (s *Server) RewardsFor(ctx context.Context, lang, session string) (RewardsView, error) {
	if s.Rewards == nil {
		return RewardsView{}, rewards.ErrUnavailable
	}
	var page rewardsPage
	if err := s.buildRewards(ctx, lang, session, "", true, &page); err != nil {
		return RewardsView{}, err
	}
	me := page.Me
	v := RewardsView{Balance: me.Account.Balance, Frozen: me.Account.Frozen, Paused: me.Account.Paused,
		ShowcasePublic: me.Account.ShowcasePublic, TodayPoints: me.Today.PlayPoints, TodayCap: me.Today.PlayCap,
		TodayPercent: page.TodayPercent, CapReached: page.CapReached, More: page.More, PlayTime: page.PlayTime,
		Owned: page.Owned, Ready: page.Ready, Affordable: page.Affordable, NextKind: page.NextKind, NextText: page.NextText,
		ReadyTiles: tilesJSON(page.ReadyTiles), Tiles: tilesJSON(page.Tiles), Done: tilesJSON(page.Done),
		Shop: groupsJSON(page.ShopGroups), Mine: groupsJSON(page.Groups), Slots: []RewardsSlot{}, Ledger: []RewardsEntry{},
		Upcoming: []RewardsItem{}}
	for _, sl := range page.Slots {
		rs := RewardsSlot{Kind: sl.Kind, Label: sl.Label, ID: sl.ID, Name: sl.Name}
		if sl.ID != "" {
			rs.Img = previewURL(sl.ID)
		}
		v.Slots = append(v.Slots, rs)
	}
	if page.Medal != nil {
		v.Badge = &RewardsSlot{Kind: "badge", ID: page.Medal.ID, Name: page.Medal.Name, Img: previewURL(page.Medal.ID)}
	}
	for _, e := range page.Ledger {
		v.Ledger = append(v.Ledger, RewardsEntry{Img: e.Img, Title: e.Title, Sub: e.Sub, Delta: e.Delta, Points: e.Points})
	}
	for _, it := range page.Upcoming {
		v.Upcoming = append(v.Upcoming, itemJSON(it))
	}
	return v, nil
}

// ThemeTokens is a validated theme for the app: the same tokens the web layout writes into its style.
type ThemeTokens struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Light map[string]string `json:"light"`
	Dark  map[string]string `json:"dark"`
}

// EquippedThemeTokens is the theme in the account's theme slot, or nil when none is equipped or usable.
func (s *Server) EquippedThemeTokens(ctx context.Context, session string) *ThemeTokens {
	if s.Rewards == nil {
		return nil
	}
	me, err := s.Rewards.Me(ctx, session)
	if err != nil {
		return nil
	}
	id := me.Equipped["theme"]
	if id == "" || !themeID.MatchString(id) {
		return nil
	}
	t := s.themeAsset(ctx, id)
	if t == nil {
		return nil
	}
	name := t.Name
	if it, ok := s.themeList(ctx)[id]; ok {
		name = it.Name
	}
	return &ThemeTokens{ID: id, Name: name, Light: t.Light, Dark: t.Dark}
}

// RewardsFailure maps a rewards-service failure to an HTTP status and a stable error code.
func RewardsFailure(err error) (status int, code string) {
	var re *rewards.Error
	if errors.As(err, &re) {
		switch re.Status {
		case http.StatusPaymentRequired:
			return http.StatusPaymentRequired, "not_enough_points"
		case http.StatusConflict:
			return http.StatusConflict, "already_owned"
		case http.StatusForbidden:
			return http.StatusForbidden, "rewards_frozen"
		case http.StatusNotFound:
			return http.StatusNotFound, "not_found"
		}
	}
	return http.StatusBadGateway, "rewards_unavailable"
}

// Forget drops the cached equipped theme for an account after an equip or a purchase.
func (s *Server) ForgetTheme(account string) { s.themes.forget(account) }
