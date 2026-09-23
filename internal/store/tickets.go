package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Help tickets: a signed-in person opens one on the website or in the app and
// talks to the operators in its thread. Staff can reply (the person sees it)
// or leave an internal note (they never do).

// Ticket statuses. open: waiting on staff; waiting: staff answered and wait on
// the person; closed: done (a reply from the person reopens it).
const (
	TicketOpen    = "open"
	TicketWaiting = "waiting"
	TicketClosed  = "closed"
)

// TicketStatuses in the order the admin queue offers them.
var TicketStatuses = []string{TicketOpen, TicketWaiting, TicketClosed}

// TicketCategories are what the forms offer; anything else is refused.
var TicketCategories = []string{"account", "network", "switch", "wiiu-3ds", "emulator", "saves", "app", "other"}

// Ticket is one support thread.
type Ticket struct {
	ID          int64
	AccountID   string
	DisplayName string // as the person was called when they opened it
	Email       string
	Subject     string
	Category    string
	Status      string
	Assignee    string // admin account id; empty = unassigned
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Messages    int // public messages, for the lists
}

// Attachment is a file kept on disk by internal/attachments; the row holds
// only its key and what to call it.
type Attachment struct {
	Key         string
	Name        string
	ContentType string
	Size        int64
}

// TicketMessage is one entry in a thread.
type TicketMessage struct {
	ID       int64
	TicketID int64
	AuthorID string
	// Staff is true for an operator's message, Internal for a note only staff see.
	Staff      bool
	Internal   bool
	Body       string
	Attachment Attachment
	CreatedAt  time.Time
}

// TicketFilter narrows ListTickets. Empty fields do not filter.
type TicketFilter struct {
	AccountID string
	Status    string
	Assignee  string
}

// TicketStore is the ticket half of Store.
type TicketStore interface {
	CreateTicket(ctx context.Context, t Ticket, first TicketMessage) (Ticket, error)
	GetTicket(ctx context.Context, id int64) (Ticket, error)
	ListTickets(ctx context.Context, f TicketFilter, limit int) ([]Ticket, error)
	// TicketMessages lists a thread oldest first; internal notes only when asked.
	TicketMessages(ctx context.Context, ticketID int64, withInternal bool) ([]TicketMessage, error)
	GetTicketMessage(ctx context.Context, id int64) (TicketMessage, error)
	// AddTicketMessage appends to a thread and, when status is not empty,
	// moves the ticket to it in the same transaction.
	AddTicketMessage(ctx context.Context, m TicketMessage, status string) (TicketMessage, error)
	// UpdateTicket sets status and/or assignee (nil leaves a field alone).
	UpdateTicket(ctx context.Context, id int64, status, assignee *string) (Ticket, error)
}

// ValidateTicket checks what a person typed; the message names the field.
func ValidateTicket(subject, category, body string) error {
	switch {
	case strings.TrimSpace(subject) == "" || len(subject) > 200:
		return fmt.Errorf("%w: subject is required, at most 200 characters", ErrInvalid)
	case !slices.Contains(TicketCategories, category):
		return fmt.Errorf("%w: category must be one of %s", ErrInvalid, strings.Join(TicketCategories, ", "))
	}
	return ValidateTicketBody(body)
}

// ValidateTicketBody checks one message.
func ValidateTicketBody(body string) error {
	if strings.TrimSpace(body) == "" || len(body) > 20000 {
		return fmt.Errorf("%w: message is required, at most 20000 characters", ErrInvalid)
	}
	return nil
}

func init() {
	migrations["0010_tickets"] = `CREATE TABLE IF NOT EXISTS tickets (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		account_id text NOT NULL,
		display_name text NOT NULL DEFAULT '',
		email text NOT NULL DEFAULT '',
		subject text NOT NULL,
		category text NOT NULL,
		status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','waiting','closed')),
		assignee text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS tickets_account_idx ON tickets(account_id, updated_at DESC);
	CREATE INDEX IF NOT EXISTS tickets_status_idx ON tickets(status, updated_at);

	CREATE TABLE IF NOT EXISTS ticket_messages (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		ticket_id bigint NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
		author_id text NOT NULL,
		staff boolean NOT NULL DEFAULT false,
		internal boolean NOT NULL DEFAULT false,
		body text NOT NULL,
		attachment_key text NOT NULL DEFAULT '',
		attachment_name text NOT NULL DEFAULT '',
		attachment_type text NOT NULL DEFAULT '',
		attachment_size bigint NOT NULL DEFAULT 0,
		created_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS ticket_messages_ticket_idx ON ticket_messages(ticket_id, id);`
}

const ticketCols = `t.id, t.account_id, t.display_name, t.email, t.subject, t.category, t.status, t.assignee,
	t.created_at, t.updated_at, (SELECT count(*) FROM ticket_messages m WHERE m.ticket_id = t.id AND NOT m.internal)`

func scanTicket(row pgx.Row) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.AccountID, &t.DisplayName, &t.Email, &t.Subject, &t.Category, &t.Status, &t.Assignee,
		&t.CreatedAt, &t.UpdatedAt, &t.Messages)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

const messageCols = `id, ticket_id, author_id, staff, internal, body, attachment_key, attachment_name,
	attachment_type, attachment_size, created_at`

func scanMessage(row pgx.Row) (TicketMessage, error) {
	var m TicketMessage
	err := row.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.Staff, &m.Internal, &m.Body, &m.Attachment.Key,
		&m.Attachment.Name, &m.Attachment.ContentType, &m.Attachment.Size, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func insertMessage(ctx context.Context, tx pgx.Tx, m TicketMessage) (TicketMessage, error) {
	return scanMessage(tx.QueryRow(ctx, `INSERT INTO ticket_messages(ticket_id, author_id, staff, internal, body,
		attachment_key, attachment_name, attachment_type, attachment_size) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING `+messageCols, m.TicketID, m.AuthorID, m.Staff, m.Internal, m.Body, m.Attachment.Key,
		m.Attachment.Name, m.Attachment.ContentType, m.Attachment.Size))
}

func (s *PG) CreateTicket(ctx context.Context, t Ticket, first TicketMessage) (Ticket, error) {
	if err := ValidateTicket(t.Subject, t.Category, first.Body); err != nil {
		return t, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return t, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO tickets(account_id, display_name, email, subject, category)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, t.AccountID, t.DisplayName, t.Email, strings.TrimSpace(t.Subject), t.Category).Scan(&t.ID); err != nil {
		return t, err
	}
	first.TicketID, first.AuthorID, first.Staff, first.Internal = t.ID, t.AccountID, false, false
	if _, err := insertMessage(ctx, tx, first); err != nil {
		return t, err
	}
	if err := tx.Commit(ctx); err != nil {
		return t, err
	}
	return s.GetTicket(ctx, t.ID)
}

func (s *PG) GetTicket(ctx context.Context, id int64) (Ticket, error) {
	return scanTicket(s.pool.QueryRow(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id=$1`, id))
}

// ListTickets: the person's own list newest first; the admin queue oldest
// activity first for open work, newest first otherwise.
func (s *PG) ListTickets(ctx context.Context, f TicketFilter, limit int) ([]Ticket, error) {
	order := "t.updated_at DESC"
	if f.AccountID == "" && f.Status == TicketOpen {
		order = "t.updated_at ASC"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+ticketCols+` FROM tickets t
		WHERE ($1 = '' OR t.account_id = $1) AND ($2 = '' OR t.status = $2) AND ($3 = '' OR t.assignee = $3)
		ORDER BY `+order+`, t.id DESC LIMIT $4`, f.AccountID, f.Status, f.Assignee, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PG) TicketMessages(ctx context.Context, ticketID int64, withInternal bool) ([]TicketMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+messageCols+` FROM ticket_messages
		WHERE ticket_id=$1 AND ($2 OR NOT internal) ORDER BY id`, ticketID, withInternal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PG) GetTicketMessage(ctx context.Context, id int64) (TicketMessage, error) {
	return scanMessage(s.pool.QueryRow(ctx, `SELECT `+messageCols+` FROM ticket_messages WHERE id=$1`, id))
}

func (s *PG) AddTicketMessage(ctx context.Context, m TicketMessage, status string) (TicketMessage, error) {
	if err := ValidateTicketBody(m.Body); err != nil {
		return m, err
	}
	if status != "" && !slices.Contains(TicketStatuses, status) {
		return m, fmt.Errorf("%w: status must be one of %s", ErrInvalid, strings.Join(TicketStatuses, ", "))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return m, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE tickets SET updated_at=now(), status=CASE WHEN $2 = '' THEN status ELSE $2 END
		WHERE id=$1`, m.TicketID, status)
	if err != nil {
		return m, err
	}
	if tag.RowsAffected() == 0 {
		return m, ErrNotFound
	}
	out, err := insertMessage(ctx, tx, m)
	if err != nil {
		return m, err
	}
	return out, tx.Commit(ctx)
}

func (s *PG) UpdateTicket(ctx context.Context, id int64, status, assignee *string) (Ticket, error) {
	if status != nil && !slices.Contains(TicketStatuses, *status) {
		return Ticket{}, fmt.Errorf("%w: status must be one of %s", ErrInvalid, strings.Join(TicketStatuses, ", "))
	}
	tag, err := s.pool.Exec(ctx, `UPDATE tickets SET updated_at=now(),
		status = COALESCE($2, status), assignee = COALESCE($3, assignee) WHERE id=$1`, id, status, assignee)
	if err != nil {
		return Ticket{}, err
	}
	if tag.RowsAffected() == 0 {
		return Ticket{}, ErrNotFound
	}
	return s.GetTicket(ctx, id)
}

// supportMem holds the in-memory tickets and crash reports.
type supportMem struct {
	tickets  []Ticket
	messages []TicketMessage
	crashes  []CrashReport
}

func (m *Memory) ticketLocked(id int64) (int, bool) {
	for i, t := range m.support.tickets {
		if t.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (m *Memory) countLocked(t Ticket) Ticket {
	t.Messages = 0
	for _, msg := range m.support.messages {
		if msg.TicketID == t.ID && !msg.Internal {
			t.Messages++
		}
	}
	return t
}

func (m *Memory) CreateTicket(_ context.Context, t Ticket, first TicketMessage) (Ticket, error) {
	if err := ValidateTicket(t.Subject, t.Category, first.Body); err != nil {
		return t, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	t.ID = int64(len(m.support.tickets) + 1)
	t.Subject, t.Status, t.Assignee, t.CreatedAt, t.UpdatedAt = strings.TrimSpace(t.Subject), TicketOpen, "", now, now
	m.support.tickets = append(m.support.tickets, t)
	first.ID, first.TicketID, first.AuthorID, first.Staff, first.Internal, first.CreatedAt =
		int64(len(m.support.messages)+1), t.ID, t.AccountID, false, false, now
	m.support.messages = append(m.support.messages, first)
	return m.countLocked(t), nil
}

func (m *Memory) GetTicket(_ context.Context, id int64) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ticketLocked(id)
	if !ok {
		return Ticket{}, ErrNotFound
	}
	return m.countLocked(m.support.tickets[i]), nil
}

func (m *Memory) ListTickets(_ context.Context, f TicketFilter, limit int) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Ticket
	for _, t := range m.support.tickets {
		if (f.AccountID == "" || t.AccountID == f.AccountID) && (f.Status == "" || t.Status == f.Status) &&
			(f.Assignee == "" || t.Assignee == f.Assignee) {
			out = append(out, m.countLocked(t))
		}
	}
	asc := f.AccountID == "" && f.Status == TicketOpen
	slices.SortStableFunc(out, func(a, b Ticket) int {
		if c := a.UpdatedAt.Compare(b.UpdatedAt); c != 0 {
			if asc {
				return c
			}
			return -c
		}
		return int(b.ID - a.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) TicketMessages(_ context.Context, ticketID int64, withInternal bool) ([]TicketMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TicketMessage
	for _, msg := range m.support.messages {
		if msg.TicketID == ticketID && (withInternal || !msg.Internal) {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (m *Memory) GetTicketMessage(_ context.Context, id int64) (TicketMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.support.messages {
		if msg.ID == id {
			return msg, nil
		}
	}
	return TicketMessage{}, ErrNotFound
}

func (m *Memory) AddTicketMessage(_ context.Context, msg TicketMessage, status string) (TicketMessage, error) {
	if err := ValidateTicketBody(msg.Body); err != nil {
		return msg, err
	}
	if status != "" && !slices.Contains(TicketStatuses, status) {
		return msg, fmt.Errorf("%w: bad status", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ticketLocked(msg.TicketID)
	if !ok {
		return msg, ErrNotFound
	}
	now := time.Now()
	m.support.tickets[i].UpdatedAt = now
	if status != "" {
		m.support.tickets[i].Status = status
	}
	msg.ID, msg.CreatedAt = int64(len(m.support.messages)+1), now
	m.support.messages = append(m.support.messages, msg)
	return msg, nil
}

func (m *Memory) UpdateTicket(_ context.Context, id int64, status, assignee *string) (Ticket, error) {
	if status != nil && !slices.Contains(TicketStatuses, *status) {
		return Ticket{}, fmt.Errorf("%w: bad status", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ticketLocked(id)
	if !ok {
		return Ticket{}, ErrNotFound
	}
	t := &m.support.tickets[i]
	if status != nil {
		t.Status = *status
	}
	if assignee != nil {
		t.Assignee = *assignee
	}
	t.UpdatedAt = time.Now()
	return m.countLocked(*t), nil
}
