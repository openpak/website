package pages

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/i18n"
)

// rankView and cardView are what the shared template blocks (_companion.html) take.
type rankView struct {
	Lang, Slug string
	R          companion.Ranking
}

type cardView struct {
	Lang string
	C    companion.Card
}

// companionPage is a title's companion page: one of overview, rankings or stats.
type companionPage struct {
	baseData
	Title  companion.Title
	Tab    string
	Board  rankView
	Card   cardView
	Failed bool // the backend did not answer; the page says so instead of showing nothing
}

// handleCompanion serves /titles/<slug> and /titles/<slug>/<tab>. A title is found by its slug
// in the catalogue and has a page only when it registered a feature in the companion service.
func (s *Server) handleCompanion(w http.ResponseWriter, r *http.Request) {
	if s.Companion == nil {
		http.NotFound(w, r)
		return
	}
	t, err := s.Companion.Resolve(r.Context(), r.PathValue("slug"))
	if err != nil {
		if !errors.Is(err, companion.ErrNotFound) {
			s.log.Error("companion resolve", "err", err)
		}
		http.NotFound(w, r)
		return
	}
	tab := r.PathValue("tab")
	if tab == "" {
		tab = "overview"
	}
	if tab != "overview" && !t.Has(companion.Feature(tab)) {
		http.NotFound(w, r)
		return
	}
	v := s.viewerOf(w, r)
	d := s.data(r, v, t.Name)
	page := companionPage{baseData: d, Title: t, Tab: tab}
	var accountID string
	if v != nil {
		accountID = v.AccountID
	}
	switch tab {
	case "rankings":
		rk, err := s.Companion.Ranking(r.Context(), t, r.URL.Query().Get("board"), rankQuery(r, accountID))
		switch {
		case errors.Is(err, companion.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			s.log.Warn("companion rankings", "title", t.Name, "err", err)
			page.Failed = true
		}
		page.Board = rankView{Lang: d.Lang, Slug: t.Slug, R: rk}
	case "listings":
		card, err := s.Companion.Listings(r.Context(), t)
		if err != nil {
			s.log.Warn("companion listings", "title", t.Name, "err", err)
			page.Failed = true
		}
		page.Card = cardView{Lang: d.Lang, C: card}
	case "stats":
		if v != nil {
			card, err := s.Companion.Stats(r.Context(), t, accountID)
			if err != nil {
				s.log.Warn("companion stats", "title", t.Name, "err", err)
				page.Failed = true
			}
			page.Card = cardView{Lang: d.Lang, C: card}
		}
	}
	s.render(w, http.StatusOK, "companion.html", page)
}

// companionSlugs maps the catalogue titles that have a companion page to its slug.
func (s *Server) companionSlugs(ctx context.Context) map[int64]string {
	if s.Companion == nil {
		return nil
	}
	with, err := s.Companion.WithFeatures(ctx)
	if err != nil {
		s.log.Warn("companion titles", "err", err)
		return nil
	}
	out := make(map[int64]string, len(with))
	for id, t := range with {
		out[id] = t.Slug
	}
	return out
}

// activityPage is /account/activity.
type activityPage struct {
	baseData
	Card   cardView
	Failed bool
}

// handleActivity is the signed-in play history: every title the core has a session for, on every
// network, with time played. The core keeps it for all accounts, so this is not limited to
// consoles under parental supervision.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "activity.title"))
	page := activityPage{baseData: d, Card: cardView{Lang: d.Lang}}
	if s.Companion == nil {
		page.Failed = true
	} else if act, err := s.Companion.Activity(r.Context(), v.AccountID); err != nil {
		s.log.Warn("play activity", "err", err)
		page.Failed = true
	} else {
		page.Card.C = ActivityCard(d.Lang, act)
	}
	s.render(w, http.StatusOK, "activity.html", page)
}

// networkNames are the networks as a person reads them; platformLabel covers the three Nintendo ones.
var networkNames = map[string]string{"wfc": "Wii / DS", "xbox": "Xbox", "x360": "Xbox 360", "np": "PlayStation 3"}

func networkLabel(ns string) string {
	if n, ok := networkNames[ns]; ok {
		return n
	}
	return platformLabel(ns)
}

// ActivityCard lays a play history out as the shared stat card: three figures, then the titles,
// newest first, each with a bar for its share of the most-played one.
func ActivityCard(lang string, a companion.Activity) companion.Card {
	c := companion.Card{Linked: true, Stats: []companion.Stat{
		{Key: "activity.total", Value: Duration(lang, a.TotalSeconds), N: a.TotalSeconds},
		{Key: "activity.games", Value: fmt.Sprint(len(a.Titles)), N: int64(len(a.Titles))},
		{Key: "activity.sessions", Value: fmt.Sprint(a.Sessions), N: int64(a.Sessions)},
	}}
	var top int64 = 1
	for _, t := range a.Titles {
		top = max(top, t.Seconds)
	}
	titles := append([]companion.ActivityTitle(nil), a.Titles...)
	sort.SliceStable(titles, func(i, j int) bool { return titles[i].Last.After(titles[j].Last) })
	for _, t := range titles {
		name := t.Title
		if name == "" {
			name = i18n.T(lang, "activity.unknown")
		}
		sub := networkLabel(t.Network) + " · " + fmt.Sprintf(i18n.T(lang, "activity.sessions.n"), t.Sessions) +
			" · " + i18n.T(lang, "activity.last") + " " + i18n.Date(lang, t.Last)
		c.Rows = append(c.Rows, companion.Row{Title: name, Sub: sub, Value: Duration(lang, t.Seconds),
			Percent: int(t.Seconds * 100 / top)})
	}
	return c
}

// Duration is a play time as "3 h 20 min" or "45 min" in the reader's language.
func Duration(lang string, seconds int64) string {
	d := time.Duration(seconds) * time.Second
	h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
	if h == 0 {
		return fmt.Sprintf(i18n.T(lang, "activity.m"), m)
	}
	return fmt.Sprintf(i18n.T(lang, "activity.hm"), h, m)
}

// trophiesPage is /account/trophies.
type trophiesPage struct {
	baseData
	Card   cardView
	Failed bool
}

// handleTrophies is the PS3 trophy page of the signed-in account, from the trophy service.
func (s *Server) handleTrophies(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "trophies.title"))
	page := trophiesPage{baseData: d, Card: cardView{Lang: d.Lang}}
	if s.Companion == nil {
		page.Failed = true
	} else if card, err := s.Companion.Trophies(r.Context(), v.AccountID); err != nil {
		s.log.Warn("trophies", "err", err)
		page.Failed = true
	} else {
		page.Card.C = card
	}
	s.render(w, http.StatusOK, "trophies.html", page)
}

// rankQuery is the board window the request asks for (?offset=), for the viewer.
func rankQuery(r *http.Request, accountID string) companion.RankQuery {
	q := companion.QueryOf(r.URL.Query(), accountID)
	q.Limit = 25
	return q
}
