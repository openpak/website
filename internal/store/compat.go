package store

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgconn"
)

// CompatTally is what players reported for one title on one client and OS: a
// console, or an emulator on an operating system. No row means untested.
type CompatTally struct {
	RequestID     int64
	Client, OS    string
	Works, Broken int
}

// CompatStore is the player compatibility ledger behind the titles page.
type CompatStore interface {
	// ReportCompat records one account's verdict for one (title, client, OS); a
	// second report on the same combination replaces the first, so nobody counts twice.
	ReportCompat(ctx context.Context, requestID int64, accountID, client, os string, works bool) error
	ListCompat(ctx context.Context) ([]CompatTally, error)
}

func init() {
	migrations["0013_title_reports"] = `CREATE TABLE IF NOT EXISTS title_reports (
		request_id bigint NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
		account_id text NOT NULL,
		client text NOT NULL,
		os text NOT NULL,
		works boolean NOT NULL,
		reported_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (request_id, account_id, client, os)
	);`
}

func (s *PG) ReportCompat(ctx context.Context, requestID int64, accountID, client, os string, works bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO title_reports(request_id, account_id, client, os, works)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (request_id, account_id, client, os) DO UPDATE SET works=EXCLUDED.works, reported_at=now()`,
		requestID, accountID, client, os, works)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign key: no such title
		return ErrNotFound
	}
	return err
}

func (s *PG) ListCompat(ctx context.Context) ([]CompatTally, error) {
	rows, err := s.pool.Query(ctx, `SELECT request_id, client, os,
			count(*) FILTER (WHERE works), count(*) FILTER (WHERE NOT works)
		FROM title_reports GROUP BY request_id, client, os ORDER BY request_id, client, os`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompatTally
	for rows.Next() {
		var c CompatTally
		if err := rows.Scan(&c.RequestID, &c.Client, &c.OS, &c.Works, &c.Broken); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type compatKey struct {
	requestID           int64
	account, client, os string
}

func (m *Memory) ReportCompat(_ context.Context, requestID int64, accountID, client, os string, works bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, r := range m.requests {
		found = found || r.ID == requestID
	}
	if !found {
		return ErrNotFound
	}
	if m.compat == nil {
		m.compat = map[compatKey]bool{}
	}
	m.compat[compatKey{requestID, accountID, client, os}] = works
	return nil
}

func (m *Memory) ListCompat(_ context.Context) ([]CompatTally, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := map[compatKey]*CompatTally{}
	var out []*CompatTally
	for k, works := range m.compat {
		g := compatKey{k.requestID, "", k.client, k.os}
		t := idx[g]
		if t == nil {
			t = &CompatTally{RequestID: k.requestID, Client: k.client, OS: k.os}
			idx[g] = t
			out = append(out, t)
		}
		if works {
			t.Works++
		} else {
			t.Broken++
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.RequestID != b.RequestID {
			return a.RequestID < b.RequestID
		}
		if a.Client != b.Client {
			return a.Client < b.Client
		}
		return a.OS < b.OS
	})
	res := make([]CompatTally, len(out))
	for i, t := range out {
		res[i] = *t
	}
	return res, nil
}
