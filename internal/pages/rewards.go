package pages

import (
	"context"
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
	"time"

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
	Soon      bool   // not for sale yet: shown as a card with its date
	When      string // "Coming 1 Dec"
}

// tileView is one badge being worked towards: a ladder (one tile for the family, showing the tier being
// worked on) or a single badge (a game's first session, a Collector).
type tileView struct {
	Key, Name   string
	Label       string // "Kart Sessions · Silver"
	Description string
	Item        string // the badge art shown: the current tier's; empty for a mission that gives points only
	Art         string // static art (web/static/rewards) a points-only mission is shown with
	Img         string // the picture's URL: the item's preview, or the static art
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

// ledgerView is one entry of the points history, drawn as a small card: a picture, what it was for, when, and the points.
type ledgerView struct {
	Img, Title, Sub string
	Delta           int
	Points          string // "+4 pts"
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

	// Missions: every mission is a tile. Ready to claim, in progress, completed; then the points history as small cards.
	ReadyTiles, Tiles, Done []tileView
	Ledger                  []ledgerView

	// Shop.
	Kind       string // the active filter; empty for all
	Chips      []kindChip
	Upcoming   []itemView  // seasonal items before their date: cards after the shelves, not buyable
	Shop       []itemView  // what the filter shows, flat (the dialogs)
	ShopGroups []kindGroup // the same, by kind (the shelves)
	PlayTime   string      // time played in total

	// My items: what is equipped in each slot, then everything owned by kind (earned ladder badges are one tile
	// per family, in the Badges group).
	Slots  []slotView
	Groups []kindGroup

	names map[string]string // item and mission names by id
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
	if err := s.buildRewards(r.Context(), lang, v.CoreSessionID, r.URL.Query().Get("kind"), tab == "missions", &page); err != nil {
		s.log.Warn("rewards: me", "err", err)
		page.Failed = true
		s.render(w, http.StatusOK, "rewards.html", page)
		return
	}
	page.Notice, page.NoticeBad = rewardsNotice(lang, r.URL.Query(), page.names)
	s.render(w, http.StatusOK, "rewards.html", page)
}

// buildRewards fills page with everything the Rewards screens show for one session: the header, missions as tiles,
// the points history (withLedger), the shop, and My items. The web page and the app's API share it.
func (s *Server) buildRewards(ctx context.Context, lang, sid, kindWant string, withLedger bool, page *rewardsPage) error {
	me, err := s.Rewards.Me(ctx, sid)
	if err != nil {
		return err
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

	// Missions: every mission becomes a tile (a ladder is one tile for the family, showing the tier being worked on).
	// The highest tier earned, and each badge a mission gave, is what My items shows. Every claimable one counts towards "ready".
	var own []tileView
	var work []tileView
	work, own, page.Done = tilesOf(lang, ms, me.Equipped["badge"])
	for _, t := range work {
		if t.State == "ready" {
			page.ReadyTiles = append(page.ReadyTiles, t)
		} else {
			page.Tiles = append(page.Tiles, t)
		}
	}
	handled := map[string]bool{} // badge items a tile stands for: never listed a second time
	for _, t := range append(append(append([]tileView{}, work...), own...), page.Done...) {
		for _, tr := range t.Tiers {
			handled[tr.Item] = true
		}
		handled[t.Item], handled[t.EquipItem] = true, true
	}
	for _, t := range own {
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
	page.Ready = len(page.ReadyTiles)

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
	page.Chips, page.Kind = kindChips(lang, forSale, kindWant)
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
	if up, err := s.Rewards.Upcoming(ctx, sid); err == nil {
		now := time.Now()
		for _, it := range up {
			// Only items dated ahead, priced, not owned and not already closed; the rest is not this card's business.
			if it.AvailableFrom == nil || !it.AvailableFrom.After(now) || it.Price == nil || it.Owned ||
				(it.AvailableUntil != nil && !it.AvailableUntil.After(now)) || (page.Kind != "" && it.Kind != page.Kind) {
				continue
			}
			iv := s.itemOf(lang, me, it)
			iv.Soon = true
			iv.When = fmt.Sprintf(i18n.T(lang, "rewards.coming"), i18n.DateShort(lang, *it.AvailableFrom))
			page.Upcoming = append(page.Upcoming, iv)
		}
		sort.SliceStable(page.Upcoming, func(a, b int) bool { return page.Upcoming[a].AvailableFrom.Before(*page.Upcoming[b].AvailableFrom) })
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

	if withLedger {
		led, _ := s.Rewards.Ledger(ctx, sid, 10)
		imgs := map[string]string{}
		for _, m := range ms {
			imgs[m.ID] = missionImg(m)
		}
		for _, e := range led {
			page.Ledger = append(page.Ledger, ledgerOf(lang, e, names, imgs))
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
	page.names = names
	return nil
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

// staticArt is the picture of a mission that gives points only (no badge): web/static/rewards/<id>.png, drawn in the
// badge system (research/rewards/art/v2/src/activity.py). A mission this site has no picture for gets one for its kind.
var staticArt = map[string]string{
	"daily-play-30":     "act-daily-play",
	"weekly-4-days":     "act-weekly-days",
	"weekly-3-titles":   "act-weekly-titles",
	"weekly-2-networks": "act-weekly-networks",
}

func staticArtOf(m rewards.Mission) string {
	if a, ok := staticArt[m.ID]; ok {
		return a
	}
	switch m.Kind {
	case "daily":
		return "act-daily-play"
	case "weekly":
		return "act-weekly-days"
	}
	return "act-points"
}

func previewURL(item string) string { return "/account/rewards/preview/" + item }
func staticURL(art string) string   { return "/static/rewards/" + art + ".png" }

// missionImg is the picture a mission is shown with: its badge (the service's reward_item, else the art set's), else static art.
func missionImg(m rewards.Mission) string {
	if m.RewardItem != "" {
		return previewURL(m.RewardItem)
	}
	if ref, ok := ladderOf[m.ID]; ok && ref.Item != "" {
		return previewURL(ref.Item)
	}
	return staticURL(staticArtOf(m))
}

// ledgerOf draws a line of the points history as a card: a picture of what the points were for (the mission's badge or
// art, the shop item, or art for play time, the daily check-in and a new game), its name, the date and the signed points.
// Title ids (play time) say nothing to a person and are left out.
func ledgerOf(lang string, e rewards.LedgerEntry, names, imgs map[string]string) ledgerView {
	label := i18n.T(lang, "rewards.src."+e.Source)
	if label == "rewards.src."+e.Source {
		label = e.Source
	}
	lv := ledgerView{Title: label, Delta: e.Delta, Img: staticURL("act-points")}
	date := i18n.Date(lang, e.CreatedAt)
	id, _, _ := strings.Cut(e.Ref, ":")
	var ref string
	switch e.Source {
	case "play", "history":
		lv.Img = staticURL("act-play-time")
	case "checkin":
		lv.Img = staticURL("act-checkin")
	case "new_title":
		lv.Img = staticURL("act-new-game")
	case "friend":
		lv.Img = previewURL("badge-first-friend")
	case "mission":
		ref = names[id]
		if img := imgs[id]; img != "" {
			lv.Img = img
		}
	case "redeem", "revoke":
		ref = names[id]
		if id != "" && ref != "" {
			lv.Img = previewURL(id)
		}
	case "admin":
		ref = e.Ref
	}
	lv.Sub = date
	if ref != "" {
		lv.Title, lv.Sub = ref, label+" · "+date
	}
	lv.Points = strconv.Itoa(e.Delta)
	if e.Delta > 0 {
		lv.Points = "+" + lv.Points
	}
	lv.Points += " " + strings.TrimSpace(strings.TrimPrefix(fmt.Sprintf(i18n.T(lang, "rewards.pts"), 0), "0")) // the unit, as the shop writes it
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
	s.themes.forget(v.AccountID) // an equip or a purchase changes the theme
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

// tilesOf turns every mission into a tile; nothing is left as a row. A ladder is the family's tier missions in order of
// target: the tile shows the first tier not yet claimed, and counts towards that tier's target. Any other mission is a
// tile of its own, showing its badge, or static art when it gives points only. Order: ready first, then by how far
// along. work is what is still to do, own the badges earned (My items), done every finished ladder and mission.
func tilesOf(lang string, ms []rewards.Mission, equippedBadge string) (work, own, done []tileView) {
	ladders := map[string][]rewards.Mission{}
	var singles []rewards.Mission
	for _, m := range ms {
		if ref, ok := ladderOf[m.ID]; ok && ref.Finish != "" {
			ladders[ref.Family] = append(ladders[ref.Family], m)
		} else {
			singles = append(singles, m)
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
			tv := tierView{Finish: i18n.T(lang, "rewards.finish."+ref.Finish), Item: item, Target: targetText(lang, m), State: "locked"}
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
			o.Img = previewURL(o.Item)
			o.Label = t.Name + " · " + t.Tiers[best].Finish
			o.Progress = fmt.Sprintf("%s / %s", t.Tiers[best].Target, t.Tiers[best].Target)
			o.Percent, o.State = 100, "earned"
			o.Equipped = equippedBadge == o.EquipItem
			if o.Complete {
				o.State = "done"
				done = append(done, o)
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
		item := ladderOf[m.ID].Item
		if m.RewardItem != "" {
			item = m.RewardItem
		}
		t := tileView{Key: m.ID, Name: m.Title, Label: m.Title, Single: true, Description: m.Description, Art: staticArtOf(m)}
		fillTile(lang, &t, m, item)
		if strings.HasPrefix(m.Metric, "family_games:") {
			t.Progress = fmt.Sprintf(i18n.T(lang, "rewards.games"), min(m.Progress, m.Target), m.Target)
		}
		if m.Claimed {
			t.State, t.Earned, t.Complete, t.Percent = "done", 1, true, 100
			t.Progress = targetText(lang, m) + " / " + targetText(lang, m)
			if strings.HasPrefix(m.Metric, "family_games:") {
				t.Progress = fmt.Sprintf(i18n.T(lang, "rewards.games"), m.Target, m.Target)
			}
			done = append(done, t)
			if item != "" { // a badge: it goes in My items too
				t.EquipItem, t.Equipped = item, equippedBadge == item
				own = append(own, t)
			}
			continue
		}
		work = append(work, t)
	}
	rank := map[string]int{"ready": 0, "progress": 1}
	byProgress := func(l []tileView) {
		sort.SliceStable(l, func(a, b int) bool {
			x, y := l[a], l[b]
			if rank[x.State] != rank[y.State] {
				return rank[x.State] < rank[y.State]
			}
			return x.Percent > y.Percent
		})
	}
	byProgress(work)
	return work, own, done
}

// playTime is a duration as a person says it: whole hours as "10 h", else minutes ("15 min", "1 h 30 min").
func playTime(lang string, seconds int64) string {
	if seconds >= 3600 && seconds%3600 == 0 {
		return fmt.Sprintf(i18n.T(lang, "rewards.hours"), seconds/3600)
	}
	return Duration(lang, seconds)
}

// targetText writes a mission's target the way its progress is counted: a duration for play time, else the number.
func targetText(lang string, m rewards.Mission) string {
	if strings.HasSuffix(m.Metric, "seconds") {
		return playTime(lang, m.Target)
	}
	return strconv.FormatInt(m.Target, 10)
}

// fillTile sets what a tile shows about the mission it is working on: the art, the progress and the reward.
func fillTile(lang string, t *tileView, m rewards.Mission, item string) {
	t.Item, t.MissionID, t.Period, t.State = item, m.ID, m.Period, "progress"
	if item != "" {
		t.Img = previewURL(item)
	} else {
		art := t.Art
		if art == "" {
			art = staticArtOf(m)
		}
		t.Img = staticURL(art)
	}
	if m.Target > 0 {
		t.Percent = int(min(m.Progress, m.Target) * 100 / m.Target)
	}
	t.Progress = fmt.Sprintf("%d / %d", min(m.Progress, m.Target), m.Target)
	if strings.HasSuffix(m.Metric, "seconds") {
		t.Progress = playTime(lang, min(m.Progress, m.Target)) + " / " + playTime(lang, m.Target)
	}
	t.TargetText = targetText(lang, m)
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
