package companion

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/openpak/website/rankingsapi"
)

// Contract is a title's Rankings tab over a game server's rankings contract endpoint
// (docs/rankings-contract.md). Names and pictures are resolved here, through People; the server
// only sends ids.
type Contract struct {
	Client *rankingsapi.Client
	People *People
	// Fallback names a player nobody has linked ("Player" in the reader's language is the page's job;
	// this is the API default).
	Fallback string

	mu     sync.Mutex
	boards boardsCache
	pages  map[string]pageCache
}

type boardsCache struct {
	list rankingsapi.BoardList
	err  error
	exp  time.Time
}

type pageCache struct {
	p   rankingsapi.Page
	exp time.Time
}

const (
	boardsTTL = time.Minute
	pageTTL   = 15 * time.Second
)

// NewContract returns nil when the server URL is empty.
func NewContract(baseURL string, people *People) *Contract {
	c := rankingsapi.NewClient(baseURL)
	if c == nil {
		return nil
	}
	return &Contract{Client: c, People: people, Fallback: "Player"}
}

func (c *Contract) list(ctx context.Context) (rankingsapi.BoardList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.boards.exp) {
		return c.boards.list, c.boards.err
	}
	// The answer is shared by every caller for a minute, so it does not depend on one caller's deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer cancel()
	l, err := c.Client.Boards(ctx)
	ttl := boardsTTL
	if err != nil {
		ttl = 20 * time.Second // a server that is down is not asked on every page view
	} else {
		l.Boards = c.withRows(ctx, l.Boards)
	}
	c.boards = boardsCache{l, err, time.Now().Add(ttl)}
	return l, err
}

// Available implements Availability: the server answers and has at least one board.
func (c *Contract) Available(ctx context.Context) bool {
	l, err := c.list(ctx)
	return err == nil && len(l.Boards) > 0
}

// Boards implements RankingSource.
func (c *Contract) Boards(ctx context.Context) ([]Board, error) {
	l, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Board, 0, len(l.Boards))
	for _, b := range l.Boards {
		out = append(out, Board{ID: b.ID, Label: b.Name, Kind: b.Kind, Order: b.Order, Unit: b.Unit, Season: b.Season})
	}
	return out, nil
}

// Ranking implements RankingSource.
func (c *Contract) Ranking(ctx context.Context, board string, q RankQuery) (Ranking, error) {
	l, err := c.list(ctx)
	if err != nil {
		return Ranking{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rankingsapi.DefaultLimit
	}
	around := ""
	if q.AccountID != "" {
		around = c.People.Subject(ctx, l.Network, q.AccountID)
	}
	key := board + "|" + strconv.Itoa(q.Offset) + "|" + strconv.Itoa(limit) + "|" + around
	c.mu.Lock()
	hit, ok := c.pages[key]
	c.mu.Unlock()
	if !ok || time.Now().After(hit.exp) {
		p, err := c.Client.Rows(ctx, board, rankingsapi.Query{Limit: limit, Offset: q.Offset, Around: around})
		if err != nil {
			return Ranking{}, err
		}
		hit = pageCache{p, time.Now().Add(pageTTL)}
		c.mu.Lock()
		if c.pages == nil || len(c.pages) > 500 {
			c.pages = map[string]pageCache{}
		}
		c.pages[key] = hit
		c.mu.Unlock()
	}
	out := Ranking{Entries: c.entries(ctx, l.Network, hit.p.Rows, q.AccountID), Updated: time.Now().UTC(),
		Total: hit.p.Total, Offset: hit.p.Offset, Limit: hit.p.Limit}
	if len(hit.p.Around) > 0 {
		out.Around = c.entries(ctx, l.Network, hit.p.Around, q.AccountID)
	}
	return out, nil
}

func (c *Contract) entries(ctx context.Context, network string, rows []rankingsapi.Row, me string) []Entry {
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		p := c.People.Resolve(ctx, network, r.AccountID, r.PID)
		name := p.Name
		if name == "" {
			name = c.Fallback
		}
		out = append(out, Entry{Rank: r.Rank, Name: name, Value: r.Score, Display: r.Display,
			AccountID: p.AccountID, Avatar: p.Avatar, Updated: r.UpdatedAt, Me: me != "" && p.AccountID == me})
	}
	return out
}

// withRows keeps the boards that have at least one row: a board nobody has set a score on is not
// offered, and a game with none has no Rankings tab. The probes run together; a board that does
// not answer is kept out for this minute.
func (c *Contract) withRows(ctx context.Context, boards []rankingsapi.Board) []rankingsapi.Board {
	const maxProbe = 400
	if len(boards) > maxProbe {
		boards = boards[:maxProbe]
	}
	has := make([]bool, len(boards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, b := range boards {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p, err := c.Client.Rows(ctx, b.ID, rankingsapi.Query{Limit: 1})
			has[i] = err == nil && p.Total > 0
		}()
	}
	wg.Wait()
	out := make([]rankingsapi.Board, 0, len(boards))
	for i, b := range boards {
		if has[i] {
			out = append(out, b)
		}
	}
	return out
}
