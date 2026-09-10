package store

import (
	"context"
	_ "embed"
	"encoding/json"
)

// catalogJSON is the honest status source of truth (docs/catalog/catalog.json).
// Embedded, not read from disk: the container build copies only the repo, so
// the file must ship inside the binary.
//
//go:embed catalog.json
var catalogJSON []byte

// Request is one votable item: a game (kind=title) or a network feature
// (kind=feature). Votes/Voted are per-query projections, not columns.
type Request struct {
	ID       int64
	Kind     string // "title" | "feature"
	Name     string
	Console  string
	Category string
	Backend  string
	Status   string
	Notes    string
	Sort     int // catalog order; lower first
	Votes    int
	Voted    bool // did the querying account vote for this one
}

// CatalogRequests parses the embedded catalog. Sort follows catalog order,
// which is already curated (live titles first, then the long tail).
func CatalogRequests() ([]Request, error) {
	var raw []struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Console  string `json:"console"`
		Category string `json:"category"`
		Backend  string `json:"backend"`
		Status   string `json:"status"`
		Notes    string `json:"notes"`
	}
	if err := json.Unmarshal(catalogJSON, &raw); err != nil {
		return nil, err
	}
	out := make([]Request, len(raw))
	for i, e := range raw {
		out[i] = Request{Kind: e.Kind, Name: e.Name, Console: e.Console, Category: e.Category,
			Backend: e.Backend, Status: e.Status, Notes: e.Notes, Sort: i}
	}
	return out, nil
}

// SeedRequests upserts catalog rows by (kind, name): new rows inserted, existing
// rows refreshed to the catalog's current status/notes/etc. It never touches
// request_votes — votes are user data and survive every reseed.
func (s *PG) SeedRequests(ctx context.Context, reqs []Request) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, r := range reqs {
		if _, err := tx.Exec(ctx, `INSERT INTO requests(kind, name, console, category, backend, status, notes, sort)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (kind, name) DO UPDATE SET
				console=EXCLUDED.console, category=EXCLUDED.category, backend=EXCLUDED.backend,
				status=EXCLUDED.status, notes=EXCLUDED.notes, sort=EXCLUDED.sort`,
			r.Kind, r.Name, r.Console, r.Category, r.Backend, r.Status, r.Notes, r.Sort); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListRequests returns requests (kind="" for all) with a live vote count and,
// for a signed-in viewer, whether they voted. Ordered by catalog sort then name.
func (s *PG) ListRequests(ctx context.Context, kind, viewerAccountID string) ([]Request, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.kind, r.name, r.console, r.category, r.backend, r.status, r.notes, r.sort,
			count(v.account_id) AS votes,
			COALESCE(bool_or(v.account_id = $2), false) AS voted
		FROM requests r
		LEFT JOIN request_votes v ON v.request_id = r.id
		WHERE ($1 = '' OR r.kind = $1)
		GROUP BY r.id
		ORDER BY r.sort, r.name`, kind, viewerAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		var r Request
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.Console, &r.Category, &r.Backend,
			&r.Status, &r.Notes, &r.Sort, &r.Votes, &r.Voted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ToggleVote flips one account's vote for one request and returns the new state.
// Idempotent per account: a second identical click removes the vote. A double
// submit that races is absorbed by the DELETE/ON CONFLICT.
func (s *PG) ToggleVote(ctx context.Context, requestID int64, accountID string) (bool, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM request_votes WHERE request_id=$1 AND account_id=$2`, requestID, accountID)
	if err != nil {
		return false, err
	}
	if ct.RowsAffected() > 0 {
		return false, nil
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO request_votes(request_id, account_id) VALUES($1,$2)
		ON CONFLICT DO NOTHING`, requestID, accountID); err != nil {
		return false, err
	}
	return true, nil
}
