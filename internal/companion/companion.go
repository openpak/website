// Package companion is the shared core of the title companion features: the
// pages under /titles/<slug>/ and the JSON API beside them. A title opts in by
// registering a Source; the shell (pages) and the API (api) read the same
// Service, so a web page and the app show the same data.
//
// Data always comes through a backend's own API (a Source wraps a client);
// nothing here reads another service's database.
package companion

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// Feature names a tab on a title page.
type Feature string

const (
	FeatureRankings Feature = "rankings"
	FeatureStats    Feature = "stats" // "my stats": needs a signed-in account
	// FeatureListings is a public list of things players put up (GTS listings, user content), as a card.
	FeatureListings Feature = "listings"
)

// Entry is one row of a ranking. Value is the sortable number (a time in
// milliseconds, points); Display is how the title shows it.
type Entry struct {
	Rank    int    `json:"rank"`
	Name    string `json:"name"`
	Value   int64  `json:"value"`
	Display string `json:"display"`
	Region  string `json:"region,omitempty"`
	Note    string `json:"note,omitempty"`
	Me      bool   `json:"me,omitempty"` // the signed-in player
	// AccountID and Avatar are set when the player is an OpenPak account whose name and picture
	// the website resolved; Avatar is a site-relative URL.
	AccountID string    `json:"account_id,omitempty"`
	Avatar    string    `json:"avatar,omitempty"`
	Updated   time.Time `json:"updated_at,omitempty"`
}

// Board is one selectable ranking: a course, a mode, a season.
type Board struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Kind is time, score or rating; Order is asc or desc; Unit and Season are optional.
	Kind   string `json:"kind,omitempty"`
	Order  string `json:"order,omitempty"`
	Unit   string `json:"unit,omitempty"`
	Season string `json:"season,omitempty"`
	// NoScores is set when the board exists but nobody has a score on it yet.
	NoScores bool `json:"no_scores,omitempty"`
}

// Ranking is one board's table. Around is the signed-in player's slice of the
// board when they are not in the top rows.
type Ranking struct {
	Boards  []Board   `json:"boards"`
	Board   string    `json:"board"`
	Label   string    `json:"label"`
	Entries []Entry   `json:"entries"`
	Around  []Entry   `json:"around,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
	// Total, Offset and Limit describe the window of Entries when the board is paged.
	Total  int `json:"total,omitempty"`
	Offset int `json:"offset"`
	Limit  int `json:"limit,omitempty"`
	// NoScores is true when no board of the title has a score yet (the page shows the "be the
	// first" state with the board names).
	NoScores bool `json:"no_scores,omitempty"`
}

// RankQuery picks the window of a board. AccountID is "" when nobody is signed in.
type RankQuery struct {
	AccountID     string
	Offset, Limit int
}

// RankingSource is what a title implements to have a Rankings tab. board is a
// Board.ID, or "" for the first.
type RankingSource interface {
	Boards(ctx context.Context) ([]Board, error)
	Ranking(ctx context.Context, board string, q RankQuery) (Ranking, error)
}

// Availability is optionally implemented by a RankingSource that can be reachable without having
// any board yet (a game server that has not stored a score). A source that is not reachable has no
// Rankings tab; one that is reachable has it, empty or not.
type Availability interface {
	Available(ctx context.Context) bool
}

// Stat is one figure of a card. Key is a stable id (and the i18n key the page
// renders); N is the number when it is one.
type Stat struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	N     int64  `json:"n"`
}

// Row is one line of a card's list: a game, a trophy set, a session.
type Row struct {
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`
	Value string `json:"value,omitempty"`
	// Percent, when 0..100, draws a progress bar under the row; -1 draws none.
	Percent int `json:"percent"`
}

// Card is the shared "my stats" block: headline figures and a list. The same
// card serves a title's own stats, trophies and play activity.
type Card struct {
	Heading string `json:"heading,omitempty"`
	Stats   []Stat `json:"stats"`
	Rows    []Row  `json:"rows"`
	// Linked is false when the account has no identity on this title's
	// network yet, so the page can say how to link one instead of showing zeros.
	Linked bool `json:"linked"`
}

// StatsSource is what a title implements to have a "my stats" tab.
type StatsSource interface {
	Stats(ctx context.Context, accountID string) (Card, error)
}

// ListingsSource is what a title implements to have a public Listings tab.
type ListingsSource interface {
	Listings(ctx context.Context) (Card, error)
}

// Source is one title's companion data. Names match the catalogue exactly
// (case-insensitive); Console "" matches any console. Match, when set, replaces both
// (a family of titles, such as every DS Pokémon game).
type Source struct {
	Names []string
	// TitleIDs match a catalogue title that carries any of these ids (title_ids, upper-case hex),
	// in addition to Names; Console still applies.
	TitleIDs []string
	Console  string
	Match    func(name, console string) bool
	Rankings RankingSource
	Stats    StatsSource
	Listings ListingsSource
}

func (s Source) matches(r store.Request) bool {
	if s.Match != nil {
		return s.Match(r.Name, r.Console)
	}
	if s.Console != "" && !strings.EqualFold(s.Console, r.Console) {
		return false
	}
	if slices.ContainsFunc(s.TitleIDs, func(id string) bool {
		return slices.ContainsFunc(r.TitleIDs, func(o string) bool { return strings.EqualFold(o, id) })
	}) {
		return true
	}
	return slices.ContainsFunc(s.Names, func(n string) bool { return strings.EqualFold(n, r.Name) })
}

// ErrNotFound is a slug no catalogue title answers to, or a feature the title
// does not have.
var ErrNotFound = errors.New("companion: not found")

// ErrUnavailable is a backend this deployment does not have, or that did not answer.
var ErrUnavailable = errors.New("companion: service not available")

// Title is a catalogue title together with the features it has.
type Title struct {
	store.Request
	Slug     string
	Features []Feature
	source   Source
}

// Has reports whether the title offers the feature.
func (t Title) Has(f Feature) bool { return slices.Contains(t.Features, f) }

// Service resolves slugs and serves the features.
type Service struct {
	DB store.Store
	// History is the core's per-account play history; nil makes Activity ErrUnavailable.
	History core.PlayHistory
	// Catalogue and Namer name the titles in a play history. The pages package knows the
	// catalogue and the live lookups; main wires them. Unset, the catalogue alone names.
	Catalogue func(ctx context.Context) map[string]string
	Namer     func(ctx context.Context, catalogue map[string]string, namespace, titleID string) (name string, icon bool)
	// TrophySource and PSN back the PS3 trophy page (/account/trophies); nil is ErrUnavailable.
	TrophySource TrophySource
	PSN          PSNLinker
	sources      []Source
}

// New returns a Service over the catalogue db. Register adds the titles.
func New(db store.Store) *Service { return &Service{DB: db} }

// Register adds a title's companion data.
func (s *Service) Register(src Source) { s.sources = append(s.sources, src) }

func (s *Service) titleOf(ctx context.Context, r store.Request, slug string) Title {
	t := Title{Request: r, Slug: slug}
	for _, src := range s.sources {
		if !src.matches(r) {
			continue
		}
		t.source = src
		if src.Rankings != nil && rankingsAvailable(ctx, src.Rankings) {
			t.Features = append(t.Features, FeatureRankings)
		}
		if src.Listings != nil {
			t.Features = append(t.Features, FeatureListings)
		}
		if src.Stats != nil {
			t.Features = append(t.Features, FeatureStats)
		}
		break
	}
	return t
}

func rankingsAvailable(ctx context.Context, r RankingSource) bool {
	a, ok := r.(Availability)
	return !ok || a.Available(ctx)
}

// Resolve finds the title a slug names. A title without any companion feature
// is ErrNotFound: it has no page.
func (s *Service) Resolve(ctx context.Context, slug string) (Title, error) {
	reqs, err := s.DB.ListRequests(ctx, "title", "")
	if err != nil {
		return Title{}, err
	}
	slugs := Slugs(reqs)
	for _, r := range reqs {
		if slugs[r.ID] != slug {
			continue
		}
		if t := s.titleOf(ctx, r, slug); len(t.Features) > 0 {
			return t, nil
		}
	}
	return Title{}, ErrNotFound
}

// WithFeatures lists the catalogue titles that have a companion page, by
// request id, for the titles page's links.
func (s *Service) WithFeatures(ctx context.Context) (map[int64]Title, error) {
	reqs, err := s.DB.ListRequests(ctx, "title", "")
	if err != nil {
		return nil, err
	}
	slugs := Slugs(reqs)
	out := map[int64]Title{}
	for _, r := range reqs {
		if t := s.titleOf(ctx, r, slugs[r.ID]); len(t.Features) > 0 {
			out[r.ID] = t
		}
	}
	return out, nil
}

// Ranking serves the Rankings tab. Only Boards and Ranking of the title's
// source are called; the board list is always filled in.
func (s *Service) Ranking(ctx context.Context, t Title, board string, q RankQuery) (Ranking, error) {
	src := t.source.Rankings
	if src == nil {
		return Ranking{}, ErrNotFound
	}
	boards, err := src.Boards(ctx)
	if err != nil {
		return Ranking{}, err
	}
	if len(boards) == 0 {
		return Ranking{Boards: []Board{}, Entries: []Entry{}, NoScores: true}, nil
	}
	noScores := !slices.ContainsFunc(boards, func(b Board) bool { return !b.NoScores })
	if board == "" {
		board = boards[0].ID
		if i := slices.IndexFunc(boards, func(b Board) bool { return !b.NoScores }); i >= 0 {
			board = boards[i].ID // open on a board that has scores
		}
	}
	if !slices.ContainsFunc(boards, func(b Board) bool { return b.ID == board }) {
		return Ranking{}, ErrNotFound
	}
	out, err := src.Ranking(ctx, board, q)
	if err != nil {
		return Ranking{}, err
	}
	out.Boards, out.Board, out.NoScores = boards, board, noScores
	for _, b := range boards {
		if b.ID == board {
			out.Label = b.Label
		}
	}
	for i := range out.Entries {
		if out.Entries[i].Rank == 0 {
			out.Entries[i].Rank = q.Offset + i + 1
		}
	}
	return out, nil
}

// Listings serves the public Listings tab.
func (s *Service) Listings(ctx context.Context, t Title) (Card, error) {
	if t.source.Listings == nil {
		return Card{}, ErrNotFound
	}
	return t.source.Listings.Listings(ctx)
}

// Stats serves the "my stats" tab for a signed-in account.
func (s *Service) Stats(ctx context.Context, t Title, accountID string) (Card, error) {
	if t.source.Stats == nil {
		return Card{}, ErrNotFound
	}
	return t.source.Stats.Stats(ctx, accountID)
}

// Slugs names every title for a URL: the lower-case name with runs of
// anything else as one hyphen. Two titles with one name (a game on two
// consoles) get their console appended, so a slug is unique and readable.
func Slugs(reqs []store.Request) map[int64]string {
	base := make(map[int64]string, len(reqs))
	count := map[string]int{}
	for _, r := range reqs {
		b := slugify(r.Name)
		base[r.ID] = b
		count[b]++
	}
	out := make(map[int64]string, len(reqs))
	seen := map[string]bool{}
	for _, r := range reqs {
		slug := base[r.ID]
		if count[slug] > 1 {
			slug += "-" + slugify(r.Console)
		}
		for n := 2; seen[slug]; n++ { // same name and console twice: number the later one
			slug = base[r.ID] + "-" + slugify(r.Console) + "-" + strconv.Itoa(n)
		}
		seen[slug] = true
		out[r.ID] = slug
	}
	return out
}

var accents = strings.NewReplacer(
	"é", "e", "è", "e", "ê", "e", "ë", "e", "á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a",
	"í", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "ō", "o", "ú", "u", "ü", "u", "ū", "u",
	"ñ", "n", "ç", "c", "ß", "ss", "'", "", "’", "")

func slugify(s string) string {
	s = accents.Replace(strings.ToLower(s))
	var b strings.Builder
	hyphen := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			hyphen = false
		} else if !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// QueryOf reads the window of a board from a request's query (?offset=&limit=) for a viewer
// ("" when nobody is signed in). Bad numbers are the defaults.
func QueryOf(q url.Values, accountID string) RankQuery {
	n := func(k string) int {
		v, err := strconv.Atoi(q.Get(k))
		if err != nil || v < 0 {
			return 0
		}
		return v
	}
	return RankQuery{AccountID: accountID, Offset: n("offset"), Limit: min(n("limit"), 100)}
}

// Ranked lists the catalogue titles that have a Rankings tab, in catalogue order.
func (s *Service) Ranked(ctx context.Context) ([]Title, error) {
	with, err := s.WithFeatures(ctx)
	if err != nil {
		return nil, err
	}
	var out []Title
	for _, t := range with {
		if t.Has(FeatureRankings) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Title) int {
		if c := strings.Compare(a.Console, b.Console); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// BoardsOf lists a title's boards, for the rankings hub and the public API.
func (s *Service) BoardsOf(ctx context.Context, t Title) ([]Board, error) {
	if t.source.Rankings == nil {
		return nil, ErrNotFound
	}
	return t.source.Rankings.Boards(ctx)
}
