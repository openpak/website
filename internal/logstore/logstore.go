// Package logstore is the Postgres side of the service logs: the host
// collector (cmd/openpak-logd) ships every service's output here, one
// partition per UTC day, and /admin/logs reads it back. Partitions older than
// Keep days are dropped whole, which is the retention. The same lines are also
// kept as day files on the host (docs/logs.md).
package logstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keep is how many days of logs are kept.
const Keep = 90

// Levels, as stored.
const (
	Debug = iota
	Info
	Warn
	Error
)

var LevelNames = [...]string{"DEBUG", "INFO", "WARN", "ERROR"}

const schema = `
CREATE TABLE IF NOT EXISTS log_lines (
  id      bigint GENERATED ALWAYS AS IDENTITY,
  ts      timestamptz NOT NULL,
  service text        NOT NULL,
  level   smallint    NOT NULL,
  msg     text        NOT NULL
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS log_lines_service_id ON log_lines (service, id);
`

// Line is one log line.
type Line struct {
	ID      int64     `json:"id"`
	TS      time.Time `json:"ts"`
	Service string    `json:"service"`
	Level   int       `json:"level"`
	Msg     string    `json:"msg"`
}

// LevelName is the line's level as text.
func (l Line) LevelName() string { return LevelNames[min(max(l.Level, 0), Error)] }

// Store is the logs database. A small pool: the viewer and the ingest share
// three connections.
type Store struct {
	pool  *pgxpool.Pool
	mu    sync.Mutex
	parts map[string]bool
}

// Open connects, creates the table and the next days' partitions.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, parts: map[string]bool{}}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.Maintain(ctx, time.Now()); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func day(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func partName(d time.Time) string { return "log_lines_" + d.Format("20060102") }

func (s *Store) ensure(ctx context.Context, d time.Time) error {
	d = day(d)
	n := partName(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parts[n] {
		return nil
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF log_lines FOR VALUES FROM ('%s') TO ('%s')",
		n, d.Format("2006-01-02"), d.Add(24*time.Hour).Format("2006-01-02")))
	if err == nil {
		s.parts[n] = true
	}
	return err
}

var partRe = regexp.MustCompile(`^log_lines_(\d{8})$`)

// Maintain makes today's and the next two days' partitions and drops those
// older than Keep days. Run at start and every few hours.
func (s *Store) Maintain(ctx context.Context, now time.Time) error {
	for i := range 3 {
		if err := s.ensure(ctx, now.AddDate(0, 0, i)); err != nil {
			return err
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'log_lines'`)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	before := day(now).AddDate(0, 0, -Keep)
	for _, n := range names {
		m := partRe.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		d, err := time.Parse("20060102", m[1])
		if err != nil || !d.Before(before) {
			continue
		}
		if _, err := s.pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{n}.Sanitize()); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.parts, n)
		s.mu.Unlock()
	}
	return nil
}

// Ingest stores a batch. Partitions for the lines' days are made first.
func (s *Store) Ingest(ctx context.Context, lines []Line) error {
	for _, l := range lines {
		if err := s.ensure(ctx, l.TS); err != nil {
			return err
		}
	}
	rows := make([][]any, len(lines))
	for i, l := range lines {
		rows[i] = []any{l.TS, l.Service, int16(l.Level), l.Msg}
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"log_lines"}, []string{"ts", "service", "level", "msg"}, pgx.CopyFromRows(rows))
	return err
}

// Query selects lines of one service on one UTC day, newest first.
type Query struct {
	Service  string
	Day      time.Time
	MinLevel int    // lines at this level or above
	Text     string // case-insensitive substring of the message
	BeforeID int64  // older than this id (paging); 0 = from the newest
	AfterID  int64  // newer than this id (tail); 0 = unset
	Limit    int
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (q Query) sql() (string, []any) {
	d := day(q.Day)
	args := []any{q.Service, d, d.Add(24 * time.Hour), q.MinLevel}
	w := "service = $1 AND ts >= $2 AND ts < $3 AND level >= $4"
	if q.Text != "" {
		args = append(args, "%"+escapeLike(q.Text)+"%")
		w += fmt.Sprintf(" AND msg ILIKE $%d", len(args))
	}
	if q.BeforeID > 0 {
		args = append(args, q.BeforeID)
		w += fmt.Sprintf(" AND id < $%d", len(args))
	}
	if q.AfterID > 0 {
		args = append(args, q.AfterID)
		w += fmt.Sprintf(" AND id > $%d", len(args))
	}
	return w, args
}

// Lines runs the query. Limit is capped at 2000.
func (s *Store) Lines(ctx context.Context, q Query) ([]Line, error) {
	w, args := q.sql()
	n := min(max(q.Limit, 1), 2000)
	rows, err := s.pool.Query(ctx,
		"SELECT id, ts, service, level, msg FROM log_lines WHERE "+w+fmt.Sprintf(" ORDER BY id DESC LIMIT %d", n), args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Line, error) {
		var l Line
		var lv int16
		err := r.Scan(&l.ID, &l.TS, &l.Service, &lv, &l.Msg)
		l.Level = int(lv)
		return l, err
	})
}

// Download streams one service-day, oldest first, one line each in the
// same format as the host's day file; fn gets each formatted line.
func (s *Store) Download(ctx context.Context, service string, d time.Time, fn func(string) error) error {
	d = day(d)
	rows, err := s.pool.Query(ctx,
		"SELECT ts, level, msg FROM log_lines WHERE service = $1 AND ts >= $2 AND ts < $3 ORDER BY id",
		service, d, d.Add(24*time.Hour))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts time.Time
		var lv int16
		var msg string
		if err := rows.Scan(&ts, &lv, &msg); err != nil {
			return err
		}
		if err := fn(FormatLine(Line{TS: ts, Level: int(lv), Msg: msg})); err != nil {
			return err
		}
	}
	return rows.Err()
}

// FormatLine is the day-file line format.
func FormatLine(l Line) string {
	return l.TS.UTC().Format("2006-01-02T15:04:05.000Z") + " " + l.LevelName() + " " + l.Msg
}

// ServiceCount is a service's line and error count for a day.
type ServiceCount struct {
	Service       string
	Lines, Errors int
}

// Services lists the services that logged on the day.
func (s *Store) Services(ctx context.Context, d time.Time) ([]ServiceCount, error) {
	d = day(d)
	rows, err := s.pool.Query(ctx,
		`SELECT service, count(*), count(*) FILTER (WHERE level >= 3) FROM log_lines
		 WHERE ts >= $1 AND ts < $2 GROUP BY service ORDER BY service`, d, d.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ServiceCount, error) {
		var c ServiceCount
		err := r.Scan(&c.Service, &c.Lines, &c.Errors)
		return c, err
	})
}

// Newest is the timestamp of the newest stored line from the last day; zero when there is none.
func (s *Store) Newest(ctx context.Context) (time.Time, error) {
	var ts *time.Time
	err := s.pool.QueryRow(ctx, "SELECT max(ts) FROM log_lines WHERE ts > now() - interval '1 day'").Scan(&ts)
	if err != nil || ts == nil {
		return time.Time{}, err
	}
	return *ts, nil
}
