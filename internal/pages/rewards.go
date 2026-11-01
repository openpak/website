package pages

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/rewards"
)

// /account/rewards: the signed-in account's points, missions, shop, collection and settings, read and
// changed through the rewards service with the person's own session as the credential. The page is one
// URL with four tabs (?tab=), drawn with the strip the title companion pages use, so every tab works
// without script; only the Shop's confirm dialog needs it, like the titles page's details. The admin
// side (/admin/rewards) lives in internal/admin with the other admin pages.

// The tabs, in reading order.
var rewardsTabs = []string{"missions", "shop", "collection", "settings"}

// itemKinds is the order kinds are listed in: the ones people collect first.
var itemKinds = []string{"badge", "profile_icon", "theme", "title", "switch_icon", "x360_gamerpic", "x360_theme", "ps3_avatar", "ps3_theme"}

// platformNames are the proper names a catalogue platform is shown under; none of them is translated.
var platformNames = map[string]string{
	"web": "Web", "app": "App", "switch": "Switch", "xbox360": "Xbox 360", "xbox": "Xbox", "ps3": "PS3",
	"wiiu": "Wii U", "3ds": "3DS", "wii": "Wii", "ds": "DS",
}

type missionView struct {
	rewards.Mission
	Label      string // the progress as "12 min / 30 min"
	Percent    int
	KindLabel  string // Daily, Weekly, One time, Series
	RewardText string // "+2 points", with "plus an item" when one is attached
	ClaimText  string // the Claim button's words
	CanClaim   bool
}

// itemView is a catalogue or inventory item with what the page prints about it.
type itemView struct {
	rewards.Item
	KindLabel string // "Badge"
	HasCost   bool
	Cost      int
	Need      int // points still missing; 0 when affordable
	After     int // the balance if it were bought now
	Key       string
	Wide      bool   // themes show a wide preview, everything else a square one
	Where     string // "Web, App, Switch"
}

// tileView is one badge being worked towards: a ladder (one tile for the family, showing the tier being
// worked on) or a single badge (a game's first session, a Collector).
type tileView struct {
	Key, Name   string
	Label       string // "Kart Sessions · Silver"
	Description string
	Item        string // the badge art shown: the current tier's
	State       string // progress, ready or done
	Percent     int
	Progress    string // "26 / 50"
	Reward      string // "Get 15 points + badge"
	ClaimText   string
	MissionID   string
	Period      string
	Pips        []bool // earned tiers, in order; nil for a single badge
	Earned      int
	Tiers       []tierView
	Single      bool
	TargetText  string
	Equipped    bool   // my items: this is what the badge slot holds
	EquipItem   string // my items: the badge Equip puts in the slot (the highest tier earned)
	Complete    bool   // every tier earned
}

type tierView struct {
	Finish, Item string
	Target       string
	Points       string // "Get 15 points"
	State        string // earned, current or locked
	Ready        bool
}

type kindGroup struct {
	Kind, Label string
	Wide        bool       // themes: wide preview cards, in a grid of their own
	Tiles       []tileView // My items, Badges: the earned ladder and single badges
	Items       []itemView
}

type kindChip struct {
	Kind, Label string
	Current     bool
}

type ledgerView struct {
	Label, Ref, Date string
	Delta            int
}

type rewardsPage struct {
	baseData
	Available, Failed bool
	Tab               string
	Tabs              []string
	Me                rewards.Me
	Notice            string
	NoticeBad         bool

	// The header: today's play points against the daily cap, and what to do next.
	TodayPercent int
	CapReached   bool
	More         int
	Medal        *medalView // the equipped badge, shown beside the balance
	Owned        int
	Ready        int
	Affordable   int
	NextKind     string // claim, buy or play; empty when there is nothing to suggest
	NextText     string

	// Missions: the badges being worked towards as tiles, then the other missions as rows.
	Tiles                       []tileView
	Claimable, InProgress, Done []missionView
	Ledger                      []ledgerView

	// Shop.
	Kind       string // the active filter; empty for all
	Chips      []kindChip
	Shop       []itemView  // what the filter shows, flat (the dialogs)
	ShopGroups []kindGroup // the same, by kind (the shelves)
	PlayTime   string      // time played in total

	// My items: what is equipped in each slot, then everything owned by kind (earned ladder badges are one tile
	// per family, in the Badges group).
	Slots  []slotView
	Groups []kindGroup
}

type medalView struct{ ID, Name string }

// slotView is one equip slot: the item in it, if any.
type slotView struct {
	Kind, Label string
	ID, Name    string
}

func (s *Server) handleRewards(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	lang := i18n.FromRequest(r)
	d := s.data(r, v, i18n.T(lang, "rewards.title"))
	d.Flash = "" // this page draws its own, typed message (a success and an error look different)
	tab := r.URL.Query().Get("tab")
	if !slices.Contains(rewardsTabs, tab) {
		tab = rewardsTabs[0]
	}
	page := rewardsPage{baseData: d, Available: s.Rewards != nil, Tab: tab, Tabs: rewardsTabs}
	if s.Rewards == nil {
		s.render(w, http.StatusOK, "rewards.html", page)
		return
	}
	ctx, sid := r.Context(), v.CoreSessionID
	me, err := s.Rewards.Me(ctx, sid)
	if err != nil {
		s.log.Warn("rewards: me", "err", err)
		page.Failed = true
		s.render(w, http.StatusOK, "rewards.html", page)
		return
	}
	page.Me = me
	ms, _ := s.Rewards.Missions(ctx, sid)
	catalog, _ := s.Rewards.Catalog(ctx, sid)
	inventory, _ := s.Rewards.Inventory(ctx, sid)

	names := map[string]string{} // item and mission names, for messages and the points history
	for _, m := range ms {
		names[m.ID] = m.Title
	}
	items := func(in []rewards.Item) []itemView {
		out := make([]itemView, 0, len(in))
		for _, it := range in {
			out = append(out, s.itemOf(lang, me, it))
			names[it.ID] = it.Name
		}
		return out
	}
	shop, owned := items(catalog), items(inventory)

	// Missions: the badge ladders and single badges become tiles (the next tier to work on in Missions, the
	// highest tier earned in My items); the rest stay rows. Every claimable one counts towards "ready".
	var rest []rewards.Mission
	var own []tileView
	page.Tiles, own, rest = tilesOf(lang, ms, me.Equipped["badge"])
	handled := map[string]bool{} // badge items a tile stands for: never listed a second time
	for _, t := range append(append([]tileView{}, page.Tiles...), own...) {
		for _, tr := range t.Tiers {
			handled[tr.Item] = true
		}
		handled[t.Item], handled[t.EquipItem] = true, true
		page.Owned += t.Earned
	}
	var mine []itemView
	for _, it := range owned {
		if handled[it.ID] {
			continue
		}
		it.Owned, it.Equipped = true, me.Equipped[it.Kind] == it.ID
		mine = append(mine, it)
	}
	page.Owned += len(mine)
	// The badge beside the balance, and the slots: whatever is equipped, named from what the page knows.
	for _, t := range own {
		names[t.EquipItem] = t.Label
	}
	if id := me.Equipped["badge"]; id != "" {
		page.Medal = &medalView{ID: id, Name: names[id]}
	}
	for _, kind := range []string{"badge", "profile_icon", "theme"} {
		sl := slotView{Kind: kind, Label: kindName(lang, "rewards.k1.", kind), ID: me.Equipped[kind]}
		sl.Name = names[sl.ID]
		page.Slots = append(page.Slots, sl)
	}
	for _, t := range page.Tiles {
		if t.State == "ready" {
			page.Ready++
		}
	}
	for _, m := range rest {
		mv := missionOf(lang, m)
		switch {
		case m.Claimed:
			page.Done = append(page.Done, mv)
		case mv.CanClaim:
			page.Claimable = append(page.Claimable, mv)
		default:
			page.InProgress = append(page.InProgress, mv)
		}
	}
	sort.SliceStable(page.InProgress, func(a, b int) bool {
		x, y := page.InProgress[a], page.InProgress[b]
		if x.Locked != y.Locked {
			return !x.Locked
		}
		return x.Percent > y.Percent
	})
	page.Ready += len(page.Claimable)

	// The shop lists what is for sale and not yet owned, what can be afforded first.
	var forSale []itemView
	for _, it := range shop {
		if it.HasCost && !it.Owned { // what you own is in My items, never in the Shop
			forSale = append(forSale, it)
			if it.Affordable {
				page.Affordable++
			}
		}
	}
	sort.SliceStable(forSale, func(a, b int) bool {
		x, y := forSale[a], forSale[b]
		if x.Affordable != y.Affordable {
			return x.Affordable
		}
		return x.Cost < y.Cost
	})
	page.Chips, page.Kind = kindChips(lang, forSale, r.URL.Query().Get("kind"))
	for _, it := range forSale {
		if page.Kind == "" || it.Kind == page.Kind {
			page.Shop = append(page.Shop, it)
		}
	}
	for _, kind := range orderedKinds(page.Shop) {
		g := kindGroup{Kind: kind, Label: kindName(lang, "rewards.k.", kind), Wide: kind == "theme"}
		for _, it := range page.Shop {
			if it.Kind == kind {
				g.Items = append(g.Items, it)
			}
		}
		page.ShopGroups = append(page.ShopGroups, g)
	}
	if h := me.Account.LifetimePlaySeconds / 3600; h >= 1 {
		page.PlayTime = fmt.Sprintf(i18n.T(lang, "rewards.hours"), h) // whole hours: it sits in a narrow tile
	} else {
		page.PlayTime = Duration(lang, me.Account.LifetimePlaySeconds)
	}

	// My items, grouped by kind; the earned badge tiles lead the Badges group.
	for _, kind := range orderedKinds(mine) {
		g := kindGroup{Kind: kind, Label: kindName(lang, "rewards.k.", kind), Wide: kind == "theme"}
		for _, it := range mine {
			if it.Kind == kind {
				g.Items = append(g.Items, it)
			}
		}
		page.Groups = append(page.Groups, g)
	}
	if len(own) > 0 {
		i := slices.IndexFunc(page.Groups, func(g kindGroup) bool { return g.Kind == "badge" })
		if i < 0 {
			page.Groups = append([]kindGroup{{Kind: "badge", Label: kindName(lang, "rewards.k.", "badge")}}, page.Groups...)
			i = 0
		}
		page.Groups[i].Tiles = own
	}

	if tab == "missions" {
		led, _ := s.Rewards.Ledger(ctx, sid, 10)
		for _, e := range led {
			page.Ledger = append(page.Ledger, ledgerOf(lang, e, names))
		}
	}

	// Today's play points against the cap.
	if c := me.Today.PlayCap; c > 0 {
		page.TodayPercent = min(me.Today.PlayPoints, c) * 100 / c
		page.CapReached = me.Today.PlayPoints >= c
		page.More = max(c-me.Today.PlayPoints, 0)
	}
	// One thing to do next, in order of how much it is worth the player's attention.
	switch {
	case me.Account.Frozen:
	case page.Ready > 0:
		page.NextKind, page.NextText = "claim", fmt.Sprintf(i18n.T(lang, "rewards.next.claim"), page.Ready)
	case page.Affordable > 0:
		page.NextKind, page.NextText = "buy", i18n.T(lang, "rewards.next.buy")
	case !page.CapReached && !me.Account.Paused:
		page.NextKind, page.NextText = "play", i18n.T(lang, "rewards.next.play")
	}

	page.Notice, page.NoticeBad = rewardsNotice(lang, r.URL.Query(), names)
	s.render(w, http.StatusOK, "rewards.html", page)
}

// itemOf adds what the page prints to a catalogue or inventory item.
func (s *Server) itemOf(lang string, me rewards.Me, it rewards.Item) itemView {
	iv := itemView{Item: it, KindLabel: kindName(lang, "rewards.k1.", it.Kind), Wide: it.Kind == "theme"}
	if it.Price != nil {
		iv.HasCost, iv.Cost = true, *it.Price
		iv.Need = max(iv.Cost-me.Account.Balance, 0)
		iv.After = me.Account.Balance - iv.Cost
	}
	var where []string
	for _, p := range it.Platforms {
		n, ok := platformNames[p]
		if !ok {
			n = strings.ToUpper(p)
		}
		where = append(where, n)
	}
	iv.Where = strings.Join(where, ", ")
	// One key per rendered form: a double submit of the same form is charged once.
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	iv.Key = hex.EncodeToString(b)
	return iv
}

// kindName is a kind's name from the catalogue; a kind this site has no words for is shown as its id, spaced.
func kindName(lang, prefix, kind string) string {
	if n := i18n.T(lang, prefix+kind); n != prefix+kind {
		return n
	}
	return strings.ReplaceAll(kind, "_", " ")
}

// orderedKinds lists the kinds present, the usual ones first, in a fixed order.
func orderedKinds(in []itemView) []string {
	have := map[string]bool{}
	for _, it := range in {
		have[it.Kind] = true
	}
	var out []string
	for _, k := range itemKinds {
		if have[k] {
			out = append(out, k)
			delete(have, k)
		}
	}
	rest := make([]string, 0, len(have))
	for k := range have {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// kindChips are the Shop's filter links (an "All", then a chip for each kind on sale) and the one chosen.
func kindChips(lang string, in []itemView, want string) ([]kindChip, string) {
	kinds := orderedKinds(in)
	if len(kinds) < 2 {
		return nil, ""
	}
	if !slices.Contains(kinds, want) {
		want = ""
	}
	chips := []kindChip{{Label: i18n.T(lang, "rewards.filter.all"), Current: want == ""}}
	for _, k := range kinds {
		chips = append(chips, kindChip{Kind: k, Label: kindName(lang, "rewards.k.", k), Current: k == want})
	}
	return chips, want
}

func missionOf(lang string, m rewards.Mission) missionView {
	mv := missionView{Mission: m, CanClaim: m.Claimable && !m.Claimed && !m.Locked}
	if m.Target > 0 {
		mv.Percent = int(min(m.Progress, m.Target) * 100 / m.Target)
	}
	if strings.HasSuffix(m.Metric, "seconds") {
		mv.Label = Duration(lang, min(m.Progress, m.Target)) + " / " + Duration(lang, m.Target)
	} else {
		mv.Label = fmt.Sprintf("%d / %d", min(m.Progress, m.Target), m.Target)
	}
	mv.KindLabel = i18n.T(lang, "rewards.mk."+m.Kind)
	if m.RewardPoints > 0 {
		mv.RewardText = fmt.Sprintf(i18n.T(lang, "rewards.reward.pts"), m.RewardPoints)
		mv.ClaimText = fmt.Sprintf(i18n.T(lang, "rewards.claim.pts"), m.RewardPoints)
	} else {
		mv.ClaimText = i18n.T(lang, "rewards.claim")
	}
	if m.RewardItem != "" {
		if mv.RewardText != "" {
			mv.RewardText += " · "
		}
		mv.RewardText += i18n.T(lang, "rewards.reward.item")
	}
	return mv
}

// ledgerOf names a line of the points history: what the points were for, and the mission or item when
// it can be named. Title ids (play time) say nothing to a person and are left out.
func ledgerOf(lang string, e rewards.LedgerEntry, names map[string]string) ledgerView {
	lv := ledgerView{Label: i18n.T(lang, "rewards.src."+e.Source), Delta: e.Delta, Date: i18n.Date(lang, e.CreatedAt)}
	if lv.Label == "rewards.src."+e.Source {
		lv.Label = e.Source
	}
	switch e.Source {
	case "mission", "redeem", "revoke":
		id, _, _ := strings.Cut(e.Ref, ":")
		lv.Ref = names[id]
	case "admin":
		lv.Ref = e.Ref
	}
	return lv
}

// rewardsNotice is the message a redirect asks for, and whether it is an error. Names come from the
// service's own lists, never from the query, so a link cannot make the page say something else.
func rewardsNotice(lang string, q url.Values, names map[string]string) (string, bool) {
	t := func(key string) string { return i18n.T(lang, key) }
	switch q.Get("m") {
	case "claimed":
		if n, _ := strconv.Atoi(q.Get("n")); n > 0 {
			return fmt.Sprintf(t("rewards.msg.claimed.pts"), n), false
		}
		return t("rewards.msg.claimed"), false
	case "redeemed":
		if name := names[q.Get("i")]; name != "" {
			return fmt.Sprintf(t("rewards.msg.redeemed"), name), false
		}
	case "equipped":
		if name := names[q.Get("i")]; name != "" {
			return fmt.Sprintf(t("rewards.msg.equipped"), name), false
		}
	case "unequipped":
		return t("rewards.msg.unequipped"), false
	case "saved":
		return t("rewards.msg.saved"), false
	case "nopoints":
		return t("rewards.err.points"), true
	case "owned":
		return t("rewards.err.owned"), true
	case "frozen":
		return t("rewards.err.frozen"), true
	case "failed":
		return t("rewards.err.failed"), true
	}
	return "", false
}

// rewardsBack goes back to the tab the form came from with a message code (and the claimed points or the
// item the message names).
func rewardsBack(w http.ResponseWriter, r *http.Request, tab, msg string, extra url.Values) {
	if !slices.Contains(rewardsTabs, tab) {
		tab = rewardsTabs[0]
	}
	q := url.Values{"tab": {tab}, "m": {msg}}
	for k, v := range extra {
		q[k] = v
	}
	http.Redirect(w, r, "/account/rewards?"+q.Encode(), http.StatusSeeOther)
}

// rewardsAction runs one POST against the service and goes back to the page with a message code: to
// okTab when it worked, to failTab when it did not, so an error is shown beside what caused it.
func (s *Server) rewardsAction(w http.ResponseWriter, r *http.Request, okTab, failTab string, do func(sid string) (url.Values, error), ok string) {
	v, good := s.postGuard(w, r)
	if !good {
		return
	}
	tab := failTab
	if s.Rewards == nil {
		rewardsBack(w, r, tab, "failed", nil)
		return
	}
	extra, err := do(v.CoreSessionID)
	if err == nil {
		rewardsBack(w, r, okTab, ok, extra)
		return
	}
	msg := "failed"
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
	rewardsBack(w, r, tab, msg, nil)
}

func (s *Server) handleRewardsClaim(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, "missions", "missions", func(sid string) (url.Values, error) {
		n, err := s.Rewards.Claim(r.Context(), sid, r.PostFormValue("mission"), r.PostFormValue("period"))
		return url.Values{"n": {strconv.Itoa(n)}}, err
	}, "claimed")
}

func (s *Server) handleRewardsRedeem(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, "collection", "shop", func(sid string) (url.Values, error) {
		// The form carries a key made when the page was drawn, so submitting the same form twice is
		// charged once; a form without one gets a fresh key.
		key := r.PostFormValue("key")
		if key == "" {
			b := make([]byte, 12)
			_, _ = rand.Read(b)
			key = hex.EncodeToString(b)
		}
		item := r.PostFormValue("item")
		err := s.Rewards.Redeem(r.Context(), sid, item, key)
		return url.Values{"i": {item}}, err
	}, "redeemed")
}

func (s *Server) handleRewardsEquip(w http.ResponseWriter, r *http.Request) {
	item := r.PostFormValue("item")
	msg := "equipped"
	if item == "" {
		msg = "unequipped"
	}
	s.rewardsAction(w, r, "collection", "collection", func(sid string) (url.Values, error) {
		return url.Values{"i": {item}}, s.Rewards.Equip(r.Context(), sid, r.PostFormValue("slot"), item)
	}, msg)
}

func (s *Server) handleRewardsSettings(w http.ResponseWriter, r *http.Request) {
	s.rewardsAction(w, r, "settings", "settings", func(sid string) (url.Values, error) {
		return nil, s.Rewards.Settings(r.Context(), sid, r.PostFormValue("showcase") == "1", r.PostFormValue("paused") == "1")
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

// tilesOf turns the badge missions into tiles and hands back the others. A ladder is the family's tier
// missions in order of target: the tile shows the first tier not yet claimed (or the top one when all are),
// and counts towards that tier's target. Order: ready first, then by how far along, then completed.
func tilesOf(lang string, ms []rewards.Mission, equippedBadge string) (work, own []tileView, rest []rewards.Mission) {
	ladders := map[string][]rewards.Mission{}
	var singles []rewards.Mission
	for _, m := range ms {
		ref, ok := ladderOf[m.ID]
		switch {
		case !ok:
			rest = append(rest, m)
		case ref.Finish == "":
			singles = append(singles, m)
		default:
			ladders[ref.Family] = append(ladders[ref.Family], m)
		}
	}
	for _, fam := range ladderOrder {
		tiers := ladders[fam]
		if len(tiers) == 0 {
			continue
		}
		sort.Slice(tiers, func(a, b int) bool { return tiers[a].Target < tiers[b].Target })
		t := tileView{Key: fam, Name: i18n.T(lang, "rewards.fam."+fam)}
		cur := len(tiers) - 1
		for i, m := range tiers {
			if !m.Claimed {
				cur = i
				break
			}
		}
		for i, m := range tiers {
			ref := ladderOf[m.ID]
			item := ref.Item
			if m.RewardItem != "" {
				item = m.RewardItem
			}
			tv := tierView{Finish: i18n.T(lang, "rewards.finish."+ref.Finish), Item: item, Target: strconv.FormatInt(m.Target, 10), State: "locked"}
			if m.RewardPoints > 0 {
				tv.Points = fmt.Sprintf(i18n.T(lang, "rewards.tile.get"), m.RewardPoints)
			}
			switch {
			case m.Claimed:
				tv.State = "earned"
				t.Earned++
			case i == cur:
				tv.State, tv.Ready = "current", m.Claimable && !m.Locked
			}
			t.Pips = append(t.Pips, m.Claimed)
			t.Tiers = append(t.Tiers, tv)
		}
		t.Complete = t.Earned == len(tiers)
		if t.Earned > 0 { // the highest tier earned is what My items shows
			best := t.Earned - 1
			o := t
			o.Item, o.EquipItem = t.Tiers[best].Item, t.Tiers[best].Item
			o.Label = t.Name + " · " + t.Tiers[best].Finish
			o.Progress = fmt.Sprintf("%s / %s", t.Tiers[best].Target, t.Tiers[best].Target)
			o.Percent, o.State = 100, "earned"
			o.Equipped = equippedBadge == o.EquipItem
			if o.Complete {
				o.State, o.Label = "done", t.Name+" · "+t.Tiers[best].Finish
			}
			own = append(own, o)
		}
		if !t.Complete { // the next tier is what Missions shows
			fillTile(lang, &t, tiers[cur], t.Tiers[cur].Item)
			t.Label = t.Name + " · " + t.Tiers[cur].Finish
			work = append(work, t)
		}
	}
	for _, m := range singles {
		ref := ladderOf[m.ID]
		item := ref.Item
		if m.RewardItem != "" {
			item = m.RewardItem
		}
		t := tileView{Key: m.ID, Name: m.Title, Label: m.Title, Single: true, Description: m.Description}
		fillTile(lang, &t, m, item)
		if strings.HasPrefix(m.Metric, "family_games:") {
			t.Progress = fmt.Sprintf(i18n.T(lang, "rewards.games"), min(m.Progress, m.Target), m.Target)
		}
		if m.Claimed {
			t.State, t.Earned, t.Complete, t.Percent = "done", 1, true, 100
			t.EquipItem, t.Equipped = item, equippedBadge == item
			t.Progress = fmt.Sprintf("%d / %d", m.Target, m.Target)
			if strings.HasPrefix(m.Metric, "family_games:") {
				t.Progress = fmt.Sprintf(i18n.T(lang, "rewards.games"), m.Target, m.Target)
			}
			own = append(own, t)
			continue
		}
		work = append(work, t)
	}
	rank := map[string]int{"ready": 0, "progress": 1}
	sort.SliceStable(work, func(a, b int) bool {
		x, y := work[a], work[b]
		if rank[x.State] != rank[y.State] {
			return rank[x.State] < rank[y.State]
		}
		return x.Percent > y.Percent
	})
	return work, own, rest
}

// fillTile sets what a tile shows about the mission it is working on: the art, the progress and the reward.
func fillTile(lang string, t *tileView, m rewards.Mission, item string) {
	t.Item, t.MissionID, t.Period, t.State = item, m.ID, m.Period, "progress"
	if m.Target > 0 {
		t.Percent = int(min(m.Progress, m.Target) * 100 / m.Target)
	}
	t.Progress = fmt.Sprintf("%d / %d", min(m.Progress, m.Target), m.Target)
	t.TargetText = strconv.FormatInt(m.Target, 10)
	if m.RewardPoints > 0 {
		t.Reward = fmt.Sprintf(i18n.T(lang, "rewards.tile.get"), m.RewardPoints)
		t.ClaimText = fmt.Sprintf(i18n.T(lang, "rewards.claim.pts"), m.RewardPoints)
	} else {
		t.ClaimText = i18n.T(lang, "rewards.claim")
	}
	if m.RewardItem != "" {
		t.Reward = strings.TrimSpace(t.Reward + " " + i18n.T(lang, "rewards.tile.badge"))
	}
	if m.Claimable && !m.Claimed && !m.Locked {
		t.State = "ready"
	}
}
