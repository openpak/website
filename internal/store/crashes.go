package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Crash reports: emulators, the homebrew, the app and (through nx-baas) the
// consoles themselves send what went wrong. Reports that share a signature
// are one problem, counted.

// CrashSources are the accepted `source` values; "console" arrives only
// through the internal endpoint.
var CrashSources = []string{"ryujinx", "citron", "eden", "nro", "app", "console"}

// CrashReport is one received report.
type CrashReport struct {
	ID         int64
	Source     string
	Version    string
	OS         string
	TitleID    string
	ErrorCode  string
	Message    string
	Fields     json.RawMessage // a JSON object, as sent
	AccountID  string          // empty when anonymous
	DeviceID   string
	Firmware   string
	Signature  string
	ReceivedAt time.Time // when the reporter says it happened (console reports), else CreatedAt
	CreatedAt  time.Time
	Attachment Attachment
	Resolved   bool
	ResolvedBy string
	ResolvedAt *time.Time
	TicketID   int64 // linked help ticket, 0 for none
}

// CrashFilter narrows the list and the groups. Empty fields do not filter.
type CrashFilter struct {
	Source, Version, TitleID, ErrorCode, Signature string
	// Status is "open", "resolved" or "" for both.
	Status   string
	From, To time.Time // on CreatedAt; zero = unbounded
}

// CrashGroup is every report sharing a signature.
type CrashGroup struct {
	Signature string
	Source    string
	ErrorCode string
	TitleID   string
	Message   string // the newest report's
	Count     int
	Open      int
	FirstSeen time.Time
	LastSeen  time.Time
	Versions  []string
	LatestID  int64
}

// CrashStore is the crash half of Store.
type CrashStore interface {
	CreateCrash(ctx context.Context, c CrashReport) (CrashReport, error)
	GetCrash(ctx context.Context, id int64) (CrashReport, error)
	ListCrashes(ctx context.Context, f CrashFilter, limit int) ([]CrashReport, error)
	CrashGroups(ctx context.Context, f CrashFilter, limit int) ([]CrashGroup, error)
	// ResolveCrashes marks one report (id > 0) or a whole signature resolved
	// or open again; it answers how many rows changed.
	ResolveCrashes(ctx context.Context, id int64, signature string, resolved bool, actor string) (int, error)
	LinkCrashTicket(ctx context.Context, id, ticketID int64) error
}

var (
	crashHex    = regexp.MustCompile(`0x[0-9a-fA-F]+|\b[0-9a-fA-F]{8,}\b`)
	crashDigits = regexp.MustCompile(`[0-9]+`)
	crashSpace  = regexp.MustCompile(`\s+`)
)

// CrashFrame is the part of a report that tells one bug from another: a
// top_frame field, else the first line of a stack/backtrace field, else the
// first line of the message. Addresses and numbers are blanked so two runs
// of the same bug land in one group.
func CrashFrame(message string, fields json.RawMessage) string {
	var f map[string]any
	_ = json.Unmarshal(fields, &f)
	pick := ""
	if v, ok := f["top_frame"].(string); ok && strings.TrimSpace(v) != "" {
		pick = v
	}
	for _, k := range []string{"stack", "stacktrace", "backtrace", "stack_trace"} {
		if pick != "" {
			break
		}
		if v, ok := f[k].(string); ok {
			pick = firstLine(v)
		}
	}
	if pick == "" {
		pick = firstLine(message)
	}
	pick = crashHex.ReplaceAllString(pick, "#")
	pick = crashDigits.ReplaceAllString(pick, "#")
	return strings.ToLower(strings.TrimSpace(crashSpace.ReplaceAllString(pick, " ")))
}

func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// CrashSignature is source + error code + title + frame, hashed: sixteen hex
// digits, stable across versions so a fix shows as a group going quiet.
func CrashSignature(source, errorCode, titleID, frame string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(source) + "\x00" + strings.ToLower(errorCode) + "\x00" +
		strings.ToLower(titleID) + "\x00" + frame))
	return hex.EncodeToString(sum[:8])
}

// ValidateCrash checks and normalises a report before it is stored, and
// fills its signature.
func ValidateCrash(c *CrashReport) error {
	c.Source = strings.ToLower(strings.TrimSpace(c.Source))
	if !slices.Contains(CrashSources, c.Source) {
		return fmt.Errorf("%w: source must be one of %s", ErrInvalid, strings.Join(CrashSources, ", "))
	}
	for name, v := range map[string]*string{"version": &c.Version, "os": &c.OS, "title_id": &c.TitleID,
		"error_code": &c.ErrorCode, "device_id": &c.DeviceID, "firmware": &c.Firmware} {
		*v = strings.TrimSpace(*v)
		if len(*v) > 200 {
			return fmt.Errorf("%w: %s is longer than 200 characters", ErrInvalid, name)
		}
	}
	if len(c.Message) > 16000 {
		return fmt.Errorf("%w: message is longer than 16000 characters", ErrInvalid)
	}
	c.TitleID = strings.ToUpper(c.TitleID)
	if len(c.Fields) == 0 || string(c.Fields) == "null" {
		c.Fields = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if json.Unmarshal(c.Fields, &obj) != nil {
		return fmt.Errorf("%w: fields must be a JSON object", ErrInvalid)
	}
	if len(c.Fields) > 64<<10 {
		return fmt.Errorf("%w: fields are larger than 64 KiB", ErrInvalid)
	}
	c.Signature = CrashSignature(c.Source, c.ErrorCode, c.TitleID, CrashFrame(c.Message, c.Fields))
	return nil
}

func init() {
	migrations["0011_crash_reports"] = `CREATE TABLE IF NOT EXISTS crash_reports (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		source text NOT NULL,
		version text NOT NULL DEFAULT '',
		os text NOT NULL DEFAULT '',
		title_id text NOT NULL DEFAULT '',
		error_code text NOT NULL DEFAULT '',
		message text NOT NULL DEFAULT '',
		fields jsonb NOT NULL DEFAULT '{}',
		account_id text NOT NULL DEFAULT '',
		device_id text NOT NULL DEFAULT '',
		firmware text NOT NULL DEFAULT '',
		signature text NOT NULL,
		received_at timestamptz NOT NULL DEFAULT now(),
		created_at timestamptz NOT NULL DEFAULT now(),
		attachment_key text NOT NULL DEFAULT '',
		attachment_name text NOT NULL DEFAULT '',
		attachment_type text NOT NULL DEFAULT '',
		attachment_size bigint NOT NULL DEFAULT 0,
		resolved boolean NOT NULL DEFAULT false,
		resolved_by text NOT NULL DEFAULT '',
		resolved_at timestamptz,
		ticket_id bigint REFERENCES tickets(id) ON DELETE SET NULL
	);
	CREATE INDEX IF NOT EXISTS crash_reports_signature_idx ON crash_reports(signature, created_at DESC);
	CREATE INDEX IF NOT EXISTS crash_reports_created_idx ON crash_reports(created_at DESC);`
}

const crashCols = `id, source, version, os, title_id, error_code, message, fields, account_id, device_id, firmware,
	signature, received_at, created_at, attachment_key, attachment_name, attachment_type, attachment_size,
	resolved, resolved_by, resolved_at, COALESCE(ticket_id, 0)`

func scanCrash(row pgx.Row) (CrashReport, error) {
	var c CrashReport
	var fields []byte
	err := row.Scan(&c.ID, &c.Source, &c.Version, &c.OS, &c.TitleID, &c.ErrorCode, &c.Message, &fields, &c.AccountID,
		&c.DeviceID, &c.Firmware, &c.Signature, &c.ReceivedAt, &c.CreatedAt, &c.Attachment.Key, &c.Attachment.Name,
		&c.Attachment.ContentType, &c.Attachment.Size, &c.Resolved, &c.ResolvedBy, &c.ResolvedAt, &c.TicketID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	c.Fields = fields
	return c, err
}

// crashWhere is the filter as SQL; the arguments start at $1.
func crashWhere(f CrashFilter) (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	for col, v := range map[string]string{"source": f.Source, "version": f.Version, "signature": f.Signature,
		"upper(title_id)": strings.ToUpper(f.TitleID), "lower(error_code)": strings.ToLower(f.ErrorCode)} {
		if v != "" {
			add(col+" = $%d", v)
		}
	}
	switch f.Status {
	case "open":
		conds = append(conds, "NOT resolved")
	case "resolved":
		conds = append(conds, "resolved")
	}
	if !f.From.IsZero() {
		add("created_at >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("created_at < $%d", f.To)
	}
	if len(conds) == 0 {
		return "TRUE", args
	}
	slices.Sort(conds) // map order must not change the query text
	return strings.Join(conds, " AND "), args
}

func (s *PG) CreateCrash(ctx context.Context, c CrashReport) (CrashReport, error) {
	if err := ValidateCrash(&c); err != nil {
		return c, err
	}
	if c.ReceivedAt.IsZero() {
		c.ReceivedAt = time.Now()
	}
	return scanCrash(s.pool.QueryRow(ctx, `INSERT INTO crash_reports(source, version, os, title_id, error_code, message,
		fields, account_id, device_id, firmware, signature, received_at, attachment_key, attachment_name,
		attachment_type, attachment_size) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING `+crashCols, c.Source, c.Version, c.OS, c.TitleID, c.ErrorCode, c.Message, []byte(c.Fields),
		c.AccountID, c.DeviceID, c.Firmware, c.Signature, c.ReceivedAt, c.Attachment.Key, c.Attachment.Name,
		c.Attachment.ContentType, c.Attachment.Size))
}

func (s *PG) GetCrash(ctx context.Context, id int64) (CrashReport, error) {
	return scanCrash(s.pool.QueryRow(ctx, `SELECT `+crashCols+` FROM crash_reports WHERE id=$1`, id))
}

func (s *PG) ListCrashes(ctx context.Context, f CrashFilter, limit int) ([]CrashReport, error) {
	where, args := crashWhere(f)
	rows, err := s.pool.Query(ctx, `SELECT `+crashCols+` FROM crash_reports WHERE `+where+
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CrashReport
	for rows.Next() {
		c, err := scanCrash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PG) CrashGroups(ctx context.Context, f CrashFilter, limit int) ([]CrashGroup, error) {
	where, args := crashWhere(f)
	rows, err := s.pool.Query(ctx, `SELECT signature, (array_agg(source ORDER BY id DESC))[1],
		(array_agg(error_code ORDER BY id DESC))[1], (array_agg(title_id ORDER BY id DESC))[1],
		(array_agg(message ORDER BY id DESC))[1], count(*), count(*) FILTER (WHERE NOT resolved),
		min(created_at), max(created_at), array_agg(DISTINCT version), max(id)
		FROM crash_reports WHERE `+where+fmt.Sprintf(` GROUP BY signature ORDER BY max(created_at) DESC LIMIT %d`, limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CrashGroup
	for rows.Next() {
		var g CrashGroup
		if err := rows.Scan(&g.Signature, &g.Source, &g.ErrorCode, &g.TitleID, &g.Message, &g.Count, &g.Open,
			&g.FirstSeen, &g.LastSeen, &g.Versions, &g.LatestID); err != nil {
			return nil, err
		}
		g.Versions = cleanVersions(g.Versions)
		out = append(out, g)
	}
	return out, rows.Err()
}

func cleanVersions(v []string) []string {
	v = slices.DeleteFunc(v, func(s string) bool { return s == "" })
	slices.Sort(v)
	return slices.Compact(v)
}

func (s *PG) ResolveCrashes(ctx context.Context, id int64, signature string, resolved bool, actor string) (int, error) {
	if id <= 0 && signature == "" {
		return 0, fmt.Errorf("%w: an id or a signature is required", ErrInvalid)
	}
	by := actor
	if !resolved {
		by = ""
	}
	tag, err := s.pool.Exec(ctx, `UPDATE crash_reports SET resolved=$3, resolved_by=$4,
		resolved_at = CASE WHEN $3 THEN now() ELSE NULL END
		WHERE (($1 > 0 AND id = $1) OR ($1 <= 0 AND signature = $2)) AND resolved <> $3`, id, signature, resolved, by)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *PG) LinkCrashTicket(ctx context.Context, id, ticketID int64) error {
	var tid any
	if ticketID > 0 {
		tid = ticketID
	}
	tag, err := s.pool.Exec(ctx, `UPDATE crash_reports SET ticket_id=$2 WHERE id=$1`, id, tid)
	if err != nil {
		var fk interface{ SQLState() string }
		if errors.As(err, &fk) && fk.SQLState() == "23503" {
			return ErrNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- in memory ---

func crashMatches(c CrashReport, f CrashFilter) bool {
	return (f.Source == "" || c.Source == f.Source) && (f.Version == "" || c.Version == f.Version) &&
		(f.Signature == "" || c.Signature == f.Signature) &&
		(f.TitleID == "" || strings.EqualFold(c.TitleID, f.TitleID)) &&
		(f.ErrorCode == "" || strings.EqualFold(c.ErrorCode, f.ErrorCode)) &&
		(f.Status == "" || (f.Status == "resolved") == c.Resolved) &&
		(f.From.IsZero() || !c.CreatedAt.Before(f.From)) && (f.To.IsZero() || c.CreatedAt.Before(f.To))
}

func (m *Memory) CreateCrash(_ context.Context, c CrashReport) (CrashReport, error) {
	if err := ValidateCrash(&c); err != nil {
		return c, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID, c.CreatedAt = int64(len(m.support.crashes)+1), time.Now()
	if c.ReceivedAt.IsZero() {
		c.ReceivedAt = c.CreatedAt
	}
	m.support.crashes = append(m.support.crashes, c)
	return c, nil
}

func (m *Memory) GetCrash(_ context.Context, id int64) (CrashReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.support.crashes) {
		return CrashReport{}, ErrNotFound
	}
	return m.support.crashes[id-1], nil
}

func (m *Memory) ListCrashes(_ context.Context, f CrashFilter, limit int) ([]CrashReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CrashReport
	for i := len(m.support.crashes) - 1; i >= 0 && len(out) < limit; i-- {
		if c := m.support.crashes[i]; crashMatches(c, f) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *Memory) CrashGroups(_ context.Context, f CrashFilter, limit int) ([]CrashGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := map[string]int{}
	var out []CrashGroup
	for i := len(m.support.crashes) - 1; i >= 0; i-- { // newest first: the first seen is the latest
		c := m.support.crashes[i]
		if !crashMatches(c, f) {
			continue
		}
		j, ok := idx[c.Signature]
		if !ok {
			idx[c.Signature] = len(out)
			out = append(out, CrashGroup{Signature: c.Signature, Source: c.Source, ErrorCode: c.ErrorCode,
				TitleID: c.TitleID, Message: c.Message, LastSeen: c.CreatedAt, LatestID: c.ID})
			j = len(out) - 1
		}
		g := &out[j]
		g.Count++
		if !c.Resolved {
			g.Open++
		}
		g.FirstSeen = c.CreatedAt
		g.Versions = append(g.Versions, c.Version)
	}
	for i := range out {
		out[i].Versions = cleanVersions(out[i].Versions)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) ResolveCrashes(_ context.Context, id int64, signature string, resolved bool, actor string) (int, error) {
	if id <= 0 && signature == "" {
		return 0, fmt.Errorf("%w: an id or a signature is required", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	now := time.Now()
	for i := range m.support.crashes {
		c := &m.support.crashes[i]
		if ((id > 0 && c.ID == id) || (id <= 0 && c.Signature == signature)) && c.Resolved != resolved {
			c.Resolved, c.ResolvedBy, c.ResolvedAt = resolved, actor, &now
			if !resolved {
				c.ResolvedBy, c.ResolvedAt = "", nil
			}
			n++
		}
	}
	return n, nil
}

func (m *Memory) LinkCrashTicket(_ context.Context, id, ticketID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.support.crashes) {
		return ErrNotFound
	}
	if _, ok := m.ticketLocked(ticketID); ticketID > 0 && !ok {
		return ErrNotFound
	}
	m.support.crashes[id-1].TicketID = ticketID
	return nil
}
