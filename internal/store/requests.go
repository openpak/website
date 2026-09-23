package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotVotable is returned when a vote targets a request that cannot be voted
// on: any feature, or a game that is already playable (live/beta). Votes are a
// signal for what to build next, so counting them on finished work would only
// pollute the scoreboard.
var ErrNotVotable = errors.New("store: request is not votable")

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
	Sort     int    // catalog order; lower first. Negative on create: after every other row.
	TitleID  string // the console's 16-hex title id, upper case; "" when unknown or a feature
	// TitleIDs is every id the game has (one per region, both halves of a pair like
	// Pokémon X / Y), primary first: TitleID is always TitleIDs[0].
	TitleIDs []string
	Votes    int
	Voted    bool // did the querying account vote for this one
	// DateAdded is set once when the row is created; LastUpdated moves with every change.
	DateAdded   time.Time
	LastUpdated time.Time
}

// Votable reports whether this request accepts votes: only games (kind=title)
// that are not yet playable — requestable, alpha or planned. Live and beta
// games are already available, and features are display-only. This is the one
// definition of the rule; the UI and the vote handlers both read it.
func (r Request) Votable() bool {
	if r.Kind != "title" {
		return false
	}
	switch r.Status {
	case "requestable", "alpha", "planned":
		return true
	}
	return false
}

// statusRank orders statuses: what can be played now first, then what is being
// built, then the long tail. The console grouping and alphabet come after it.
func statusRank(status string) int {
	switch status {
	case "live", "working":
		return 0
	case "beta":
		return 1
	case "alpha", "partial":
		return 2
	case "scaffold":
		return 3
	case "requestable", "planned":
		return 4
	default: // not-feasible, broken, anything new
		return 5
	}
}

// playable is the top block of the list: live, beta, alpha and the rest in flight.
func playable(status string) bool { return statusRank(status) <= 3 }

// CatalogOrder is the one ordering every view of the title list shares, the /titles page and
// GET /api/v1/titles alike: playable titles on top, grouped by console and alphabetical inside
// each; then the long tail the same way. The keys are chosen so that hiding rows for a console filter leaves the rest ordered by status
// then name, and hiding for a status filter leaves them ordered by console then name.
func CatalogOrder(reqs []Request) []Request {
	out := append([]Request(nil), reqs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if playable(a.Status) != playable(b.Status) {
			return playable(a.Status)
		}
		if a.Console != b.Console {
			return a.Console < b.Console
		}
		if statusRank(a.Status) != statusRank(b.Status) {
			return statusRank(a.Status) < statusRank(b.Status)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// ListRequests returns requests (kind="" for all) with a live vote count and,
// for a signed-in viewer, whether they voted. Ordered by catalog sort then name.
func (s *PG) ListRequests(ctx context.Context, kind, viewerAccountID string) ([]Request, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.kind, r.name, r.console, r.category, r.backend, r.status, r.notes, r.sort,
			r.title_id, r.title_ids, r.date_added, r.last_updated, count(v.account_id) AS votes,
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
			&r.Status, &r.Notes, &r.Sort, &r.TitleID, &r.TitleIDs, &r.DateAdded, &r.LastUpdated, &r.Votes, &r.Voted); err != nil {
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
	// The votable rule is enforced here, not just in the UI: a direct API call
	// or a stale client must not inflate a live/beta title or a feature.
	var r Request
	err := s.pool.QueryRow(ctx, `SELECT kind, status FROM requests WHERE id=$1`, requestID).Scan(&r.Kind, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !r.Votable() {
		return false, ErrNotVotable
	}
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

// RequestVotes is one request's current count and whether the account voted —
// the fresh numbers to hand back after a toggle. A missing request reads 0/false.
func (s *PG) RequestVotes(ctx context.Context, requestID int64, accountID string) (int, bool, error) {
	var votes int
	var voted bool
	err := s.pool.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(account_id = $2), false)
		FROM request_votes WHERE request_id = $1`, requestID, accountID).Scan(&votes, &voted)
	return votes, voted, err
}

// Statuses is every status a request may hold. A write naming anything else is refused.
var Statuses = []string{"live", "working", "beta", "alpha", "partial", "planned", "requestable", "not-feasible"}

var (
	// ErrInvalid is a request that fails validation; the wrapped message says why.
	ErrInvalid = errors.New("invalid request")
	// ErrDuplicate is a create or change onto an existing (kind, name, console) or title id.
	ErrDuplicate = errors.New("a request with that kind, name and console, or one of those title ids, exists")
	// ErrHasVotes refuses deleting a request someone voted for: votes are user data.
	ErrHasVotes = errors.New("the request has votes")
)

// RequestPatch is a partial update: nil fields stay as they are. Kind is fixed at creation.
// TitleIDs replaces the whole list; TitleID alone replaces the primary and keeps the others
// ("" clears them all). Both together: TitleID first, then TitleIDs.
type RequestPatch struct {
	Name, Console, Category, Backend, Status, Notes, TitleID *string
	TitleIDs                                                 *[]string
	Sort                                                     *int
}

// RequestChange is one row of request_changes: who created, updated or deleted a request,
// when, and each changed field's old and new value (nil on the side that did not exist).
type RequestChange struct {
	RequestID int64
	Actor     string // an admin account id, or "api-key"
	Action    string // create | update | delete
	At        time.Time
	Changes   map[string]FieldChange
}

type FieldChange struct {
	Old any `json:"old"`
	New any `json:"new"`
}

func (p RequestPatch) apply(r Request) Request {
	for _, f := range []struct{ dst, src *string }{{&r.Name, p.Name}, {&r.Console, p.Console},
		{&r.Category, p.Category}, {&r.Backend, p.Backend}, {&r.Status, p.Status}, {&r.Notes, p.Notes}} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	switch {
	case p.TitleIDs != nil:
		r.TitleIDs = slices.Clone(*p.TitleIDs)
		if p.TitleID != nil && *p.TitleID != "" {
			r.TitleIDs = append([]string{*p.TitleID}, r.TitleIDs...)
		}
		r.TitleID = ""
	case p.TitleID != nil && *p.TitleID == "":
		r.TitleID, r.TitleIDs = "", nil
	case p.TitleID != nil:
		rest := slices.DeleteFunc(slices.Clone(r.TitleIDs), func(id string) bool { return strings.EqualFold(id, r.TitleID) })
		r.TitleID, r.TitleIDs = *p.TitleID, append([]string{*p.TitleID}, rest...)
	}
	if p.Sort != nil {
		r.Sort = *p.Sort
	}
	return r
}

// normalize trims the text fields and validates the row. Every write goes through it.
func normalize(r Request) (Request, error) {
	for _, f := range []*string{&r.Kind, &r.Name, &r.Console, &r.Category, &r.Backend, &r.Status, &r.Notes, &r.TitleID} {
		*f = strings.TrimSpace(*f)
	}
	// The list is the truth; a bare TitleID (a create that names one id) seeds it.
	ids := r.TitleIDs
	if len(ids) == 0 && r.TitleID != "" {
		ids = []string{r.TitleID}
	}
	r.TitleIDs = []string{}
	for _, id := range ids {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id == "" || slices.Contains(r.TitleIDs, id) {
			continue
		}
		if !titleIDPattern.MatchString(id) {
			return r, fmt.Errorf("%w: title ids must be 16 hex digits", ErrInvalid)
		}
		r.TitleIDs = append(r.TitleIDs, id)
	}
	r.TitleID = ""
	if len(r.TitleIDs) > 0 {
		r.TitleID = r.TitleIDs[0]
	}
	switch {
	case r.Kind != "title" && r.Kind != "feature":
		return r, fmt.Errorf("%w: kind must be title or feature", ErrInvalid)
	case r.Name == "":
		return r, fmt.Errorf("%w: name is required", ErrInvalid)
	case !slices.Contains(Statuses, r.Status):
		return r, fmt.Errorf("%w: status must be one of %s", ErrInvalid, strings.Join(Statuses, ", "))
	}
	return r, nil
}

// fields is what request_changes compares and records.
func fields(r Request) map[string]any {
	return map[string]any{"kind": r.Kind, "name": r.Name, "console": r.Console, "category": r.Category,
		"backend": r.Backend, "status": r.Status, "notes": r.Notes, "sort": r.Sort, "title_id": r.TitleID,
		"title_ids": strings.Join(r.TitleIDs, ",")}
}

// diff is every field whose value differs; a nil map is the side that does not exist.
func diff(old, new map[string]any) map[string]FieldChange {
	out := map[string]FieldChange{}
	for _, k := range []string{"kind", "name", "console", "category", "backend", "status", "notes", "sort", "title_id", "title_ids"} {
		if old[k] != new[k] {
			out[k] = FieldChange{old[k], new[k]}
		}
	}
	return out
}

// statusAudit is the admin_audit line for a title's status change, which /feed/titles.xml
// and the admin events page read; empty when there is nothing to say.
func statusAudit(id int64, old, new Request) string {
	if new.Kind != "title" || old.Status == new.Status {
		return ""
	}
	return fmt.Sprintf("%d %s: %s -> %s", id, new.Name, old.Status, new.Status)
}

var titleIDPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

const requestCols = `id, kind, name, console, category, backend, status, notes, sort, title_id, title_ids, date_added, last_updated`

func scanRequest(row pgx.Row) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.Kind, &r.Name, &r.Console, &r.Category, &r.Backend, &r.Status,
		&r.Notes, &r.Sort, &r.TitleID, &r.TitleIDs, &r.DateAdded, &r.LastUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func logChange(ctx context.Context, tx pgx.Tx, id int64, actor, action string, ch map[string]FieldChange) error {
	b, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO request_changes(request_id, actor, action, changes) VALUES($1,$2,$3,$4)`,
		id, actor, action, b)
	return err
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// titleIDsTaken refuses ids another request already holds: one id is one game. An array
// cannot carry a unique index, so the check takes a lock that orders it against every other
// catalog write (votes and reads do not wait on it).
func titleIDsTaken(ctx context.Context, tx pgx.Tx, except int64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE requests IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM requests WHERE id <> $1 AND title_ids && $2::text[])`,
		except, ids).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrDuplicate
	}
	return nil
}

// CreateRequest adds a title or feature and logs it, in one transaction.
func (s *PG) CreateRequest(ctx context.Context, r Request, actor string) (Request, error) {
	r, err := normalize(r)
	if err != nil {
		return r, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx)
	if err := titleIDsTaken(ctx, tx, 0, r.TitleIDs); err != nil {
		return r, err
	}
	r, err = scanRequest(tx.QueryRow(ctx, `INSERT INTO requests(kind, name, console, category, backend, status, notes, sort, title_id, title_ids)
		VALUES($1,$2,$3,$4,$5,$6,$7, CASE WHEN $8 < 0 THEN (SELECT COALESCE(max(sort)+1, 0) FROM requests) ELSE $8 END, $9, $10)
		RETURNING `+requestCols, r.Kind, r.Name, r.Console, r.Category, r.Backend, r.Status, r.Notes, r.Sort, r.TitleID, r.TitleIDs))
	if isUniqueViolation(err) {
		return r, ErrDuplicate
	}
	if err != nil {
		return r, err
	}
	if err := logChange(ctx, tx, r.ID, actor, "create", diff(nil, fields(r))); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}

// UpdateRequest applies a partial update, bumps last_updated and logs the changed fields, in
// one transaction. This is the only way a request changes: the API and the admin forms both
// call it. A patch that changes nothing writes nothing.
func (s *PG) UpdateRequest(ctx context.Context, id int64, p RequestPatch, actor string) (Request, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	old, err := scanRequest(tx.QueryRow(ctx, `SELECT `+requestCols+` FROM requests WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return old, err
	}
	r, err := normalize(p.apply(old))
	if err != nil {
		return old, err
	}
	ch := diff(fields(old), fields(r))
	if len(ch) == 0 {
		return old, nil
	}
	if err := titleIDsTaken(ctx, tx, id, r.TitleIDs); err != nil {
		return old, err
	}
	r, err = scanRequest(tx.QueryRow(ctx, `UPDATE requests SET name=$2, console=$3, category=$4, backend=$5,
		status=$6, notes=$7, sort=$8, title_id=$9, title_ids=$10, last_updated=now() WHERE id=$1 RETURNING `+requestCols,
		id, r.Name, r.Console, r.Category, r.Backend, r.Status, r.Notes, r.Sort, r.TitleID, r.TitleIDs))
	if isUniqueViolation(err) {
		return old, ErrDuplicate
	}
	if err != nil {
		return old, err
	}
	if err := logChange(ctx, tx, id, actor, "update", ch); err != nil {
		return r, err
	}
	if d := statusAudit(id, old, r); d != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO admin_audit(actor_id, action, detail) VALUES($1,'title.status',$2)`, actor, d); err != nil {
			return r, err
		}
	}
	return r, tx.Commit(ctx)
}

// DeleteRequest removes a request nobody voted for, and logs it, in one transaction.
func (s *PG) DeleteRequest(ctx context.Context, id int64, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// FOR UPDATE also holds off a vote: its foreign key needs a lock this one blocks.
	old, err := scanRequest(tx.QueryRow(ctx, `SELECT `+requestCols+` FROM requests WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	var voted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM request_votes WHERE request_id=$1)`, id).Scan(&voted); err != nil {
		return err
	}
	if voted {
		return ErrHasVotes
	}
	if _, err := tx.Exec(ctx, `DELETE FROM requests WHERE id=$1`, id); err != nil {
		return err
	}
	if err := logChange(ctx, tx, id, actor, "delete", diff(fields(old), nil)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
