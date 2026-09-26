package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// ShopLink is one title's product page at one retailer, so the titles page can
// link the game itself rather than a search.
type ShopLink struct {
	RequestID int64
	Shop      string // "cex", "gamestop", "micromania"
	URL       string
}

// ShopStore holds the retailer links. The page checks the URL; the store keeps it.
type ShopStore interface {
	ListShopLinks(ctx context.Context) ([]ShopLink, error)
	// SetShopLink stores a title's link at a shop; an empty url removes it.
	SetShopLink(ctx context.Context, requestID int64, shop, url string) error
}

func init() {
	migrations["0014_title_shop_links"] = `CREATE TABLE IF NOT EXISTS title_shop_links (
		request_id bigint NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
		shop text NOT NULL,
		url text NOT NULL,
		PRIMARY KEY (request_id, shop)
	);`
}

func (s *PG) ListShopLinks(ctx context.Context) ([]ShopLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT request_id, shop, url FROM title_shop_links ORDER BY request_id, shop`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShopLink
	for rows.Next() {
		var l ShopLink
		if err := rows.Scan(&l.RequestID, &l.Shop, &l.URL); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *PG) SetShopLink(ctx context.Context, requestID int64, shop, url string) error {
	if url == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM title_shop_links WHERE request_id=$1 AND shop=$2`, requestID, shop)
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO title_shop_links(request_id, shop, url) VALUES($1,$2,$3)
		ON CONFLICT (request_id, shop) DO UPDATE SET url=EXCLUDED.url`, requestID, shop, url)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrNotFound
	}
	return err
}

func (m *Memory) ListShopLinks(_ context.Context) ([]ShopLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ShopLink(nil), m.shopLinks...), nil
}

func (m *Memory) SetShopLink(_ context.Context, requestID int64, shop, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, r := range m.requests {
		found = found || r.ID == requestID
	}
	if !found {
		return ErrNotFound
	}
	out := m.shopLinks[:0]
	for _, l := range m.shopLinks {
		if l.RequestID != requestID || l.Shop != shop {
			out = append(out, l)
		}
	}
	if url != "" {
		out = append(out, ShopLink{requestID, shop, url})
	}
	m.shopLinks = out
	return nil
}
