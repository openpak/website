package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Region-node operator applications. A signed-in user applies from their
// account page; an admin approves (which creates the node in the core and
// shows its key once), rejects with a reason, or asks for more. Applications
// for a place the coverage plan does not want yet can join a per-country
// waitlist instead. The node itself lives in the core; this is the paperwork.

// Application states.
const (
	OpPending  = "pending"  // waiting for an admin
	OpInfo     = "info"     // an admin asked a question (AdminNote); the applicant replies
	OpApproved = "approved" // node created; NodeID set
	OpRejected = "rejected" // AdminNote is the reason
	OpWaitlist = "waitlist" // nobody is wanted there yet; kept to contact later
)

// OperatorServerPlan is one server the applicant intends to run, as they
// describe it. Nothing here is checked: the servers' own reports are.
type OperatorServerPlan struct {
	Cores         int      `json:"cores"`
	RAMGB         int      `json:"ram_gb"`
	DiskGB        int      `json:"disk_gb"`
	DiskSSD       bool     `json:"disk_ssd"`
	BandwidthMbps int      `json:"bandwidth_mbps"`
	IPv6          bool     `json:"ipv6"`
	Roles         []string `json:"roles"`
}

// OperatorApplication is one application.
type OperatorApplication struct {
	ID        int64
	AccountID string
	Email     string // contact for operations and security
	Location  string // coverage-plan code; "" on a waitlist entry
	Country   string
	City      string
	Provider  string
	Servers   []OperatorServerPlan
	Notes     string
	Reply     string // the applicant's answer to an admin question
	Status    string
	AdminNote string // the rejection reason or the question
	NodeID    string
	DecidedBy string
	TermsAt   time.Time // when the operator terms were accepted
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OperatorStore keeps applications.
type OperatorStore interface {
	CreateOperatorApplication(ctx context.Context, a OperatorApplication) (OperatorApplication, error)
	OperatorApplicationsFor(ctx context.Context, accountID string) ([]OperatorApplication, error)
	// ListOperatorApplications lists by status, newest first; "" lists all.
	ListOperatorApplications(ctx context.Context, status string) ([]OperatorApplication, error)
	GetOperatorApplication(ctx context.Context, id int64) (OperatorApplication, error)
	// DecideOperatorApplication is the admin's move: status, note, node.
	DecideOperatorApplication(ctx context.Context, id int64, status, note, nodeID, actor string) error
	// ReplyOperatorApplication is the applicant answering a question; it goes
	// back to pending. Only the applicant's own application in OpInfo moves.
	ReplyOperatorApplication(ctx context.Context, id int64, accountID, reply string) error
}

func init() {
	migrations["0023_operator_applications"] = `CREATE TABLE IF NOT EXISTS operator_applications (
		id bigserial PRIMARY KEY,
		account_id text NOT NULL,
		email text NOT NULL,
		location text NOT NULL DEFAULT '',
		country text NOT NULL,
		city text NOT NULL DEFAULT '',
		provider text NOT NULL DEFAULT '',
		servers jsonb NOT NULL DEFAULT '[]',
		notes text NOT NULL DEFAULT '',
		reply text NOT NULL DEFAULT '',
		status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','info','approved','rejected','waitlist')),
		admin_note text NOT NULL DEFAULT '',
		node_id text NOT NULL DEFAULT '',
		decided_by text NOT NULL DEFAULT '',
		terms_at timestamptz NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS operator_applications_account_idx ON operator_applications(account_id);
	CREATE INDEX IF NOT EXISTS operator_applications_status_idx ON operator_applications(status, created_at DESC);`
}

const opCols = `id, account_id, email, location, country, city, provider, servers, notes, reply, status, admin_note, node_id, decided_by, terms_at, created_at, updated_at`

func scanOp(row pgx.Row) (OperatorApplication, error) {
	var a OperatorApplication
	var servers []byte
	err := row.Scan(&a.ID, &a.AccountID, &a.Email, &a.Location, &a.Country, &a.City, &a.Provider, &servers, &a.Notes, &a.Reply,
		&a.Status, &a.AdminNote, &a.NodeID, &a.DecidedBy, &a.TermsAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(servers, &a.Servers)
	return a, err
}

func (s *PG) CreateOperatorApplication(ctx context.Context, a OperatorApplication) (OperatorApplication, error) {
	servers, err := json.Marshal(a.Servers)
	if err != nil {
		return a, err
	}
	if a.Status == "" {
		a.Status = OpPending
	}
	return scanOp(s.pool.QueryRow(ctx, `INSERT INTO operator_applications
		(account_id, email, location, country, city, provider, servers, notes, status, terms_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+opCols,
		a.AccountID, a.Email, a.Location, a.Country, a.City, a.Provider, servers, a.Notes, a.Status, a.TermsAt))
}

func (s *PG) opQuery(ctx context.Context, where string, args ...any) ([]OperatorApplication, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+opCols+` FROM operator_applications `+where+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperatorApplication
	for rows.Next() {
		a, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PG) OperatorApplicationsFor(ctx context.Context, accountID string) ([]OperatorApplication, error) {
	return s.opQuery(ctx, `WHERE account_id=$1`, accountID)
}

func (s *PG) ListOperatorApplications(ctx context.Context, status string) ([]OperatorApplication, error) {
	if status == "" {
		return s.opQuery(ctx, ``)
	}
	return s.opQuery(ctx, `WHERE status=$1`, status)
}

func (s *PG) GetOperatorApplication(ctx context.Context, id int64) (OperatorApplication, error) {
	return scanOp(s.pool.QueryRow(ctx, `SELECT `+opCols+` FROM operator_applications WHERE id=$1`, id))
}

func (s *PG) DecideOperatorApplication(ctx context.Context, id int64, status, note, nodeID, actor string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE operator_applications SET status=$2, admin_note=$3, node_id=CASE WHEN $4='' THEN node_id ELSE $4 END,
		decided_by=$5, updated_at=now() WHERE id=$1`, id, status, note, nodeID, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) ReplyOperatorApplication(ctx context.Context, id int64, accountID, reply string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE operator_applications SET reply=$3, status='pending', updated_at=now()
		WHERE id=$1 AND account_id=$2 AND status='info'`, id, accountID, reply)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MemoryOperators is the in-memory OperatorStore for tests.
type MemoryOperators struct {
	mu   sync.Mutex
	apps []OperatorApplication
}

func (m *MemoryOperators) CreateOperatorApplication(_ context.Context, a OperatorApplication) (OperatorApplication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = int64(len(m.apps) + 1)
	if a.Status == "" {
		a.Status = OpPending
	}
	a.CreatedAt, a.UpdatedAt = time.Now(), time.Now()
	m.apps = append(m.apps, a)
	return a, nil
}

func (m *MemoryOperators) filter(keep func(OperatorApplication) bool) []OperatorApplication {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OperatorApplication
	for _, a := range slices.Backward(m.apps) {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

func (m *MemoryOperators) OperatorApplicationsFor(_ context.Context, accountID string) ([]OperatorApplication, error) {
	return m.filter(func(a OperatorApplication) bool { return a.AccountID == accountID }), nil
}

func (m *MemoryOperators) ListOperatorApplications(_ context.Context, status string) ([]OperatorApplication, error) {
	return m.filter(func(a OperatorApplication) bool { return status == "" || a.Status == status }), nil
}

func (m *MemoryOperators) GetOperatorApplication(_ context.Context, id int64) (OperatorApplication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.apps) {
		return OperatorApplication{}, ErrNotFound
	}
	return m.apps[id-1], nil
}

func (m *MemoryOperators) DecideOperatorApplication(_ context.Context, id int64, status, note, nodeID, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.apps) {
		return ErrNotFound
	}
	a := &m.apps[id-1]
	a.Status, a.AdminNote, a.DecidedBy, a.UpdatedAt = status, note, actor, time.Now()
	if nodeID != "" {
		a.NodeID = nodeID
	}
	return nil
}

func (m *MemoryOperators) ReplyOperatorApplication(_ context.Context, id int64, accountID, reply string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.apps) || m.apps[id-1].AccountID != accountID || m.apps[id-1].Status != OpInfo {
		return ErrNotFound
	}
	a := &m.apps[id-1]
	a.Reply, a.Status, a.UpdatedAt = reply, OpPending, time.Now()
	return nil
}
