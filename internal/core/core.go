// Package core is the website's only doorway to the account core: versioned
// HTTP (/v1/*) and gRPC contracts (Identity, Links, Social, Events). The
// website never reads core storage. Interfaces here exist so handlers and
// tests stay independent of transport details.
package core

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors mapped from core outcomes.
var (
	ErrInvalidCredentials = errors.New("core: invalid credentials")
	ErrSessionInvalid     = errors.New("core: session invalid or expired")
	ErrNotFound           = errors.New("core: not found")
	ErrConflict           = errors.New("core: conflicting state")
	ErrBlocked            = errors.New("core: blocked")
	ErrBanned             = errors.New("core: account banned")
	ErrContractPending    = errors.New("core: contract not yet available (see Contract requests)")
)

// Session is one live core web session as reported by GET /v1/sessions.
type Session struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Account is the website's projection of the signed-in account. It is
// resolved at login and cached for the lifetime of the web request only.
type Account struct {
	ID          string
	Email       string
	DisplayName string
	// Country and Birthday are what the console's user object carries. Empty is normal for
	// accounts created before registration collected them.
	Country  string
	Birthday string
}

// Friend is a friend or pending request counterparty.
type Friend struct {
	AccountID   string
	DisplayName string
}

// Relationship between the viewer and another account.
type Relationship int

const (
	RelNone Relationship = iota
	RelPendingIncoming
	RelPendingOutgoing
	RelFriends
	RelBlockedBySelf
	RelBlockedByOther
)

// Link is an active adapter link (e.g. the Switch console link).
type Link struct {
	LinkID    string
	Namespace string
	SubjectID string // adapter-owned subject; website treats it as opaque
}

// Event is a durable core invalidation event (audit trail for moderators).
type Event struct {
	Version   uint64
	EventID   string
	Type      string
	AccountID string
	CreatedAt time.Time
}

// HTTP is the core's public HTTP /v1/* surface used for the web journeys
// (WEB-01): registration, verification, login, logout, recovery, sessions.
type HTTP interface {
	Register(ctx context.Context, email, password, displayName string) error
	RegisterWithProfile(ctx context.Context, email, password, displayName, country, birthday string) error
	VerifyEmail(ctx context.Context, token string) error
	Login(ctx context.Context, email, password string) (coreSessionID string, err error)
	Logout(ctx context.Context, coreSessionID string) error
	ListSessions(ctx context.Context, coreSessionID string) ([]Session, error)
	RevokeSession(ctx context.Context, coreSessionID, sessionID string) error
	ChangePassword(ctx context.Context, coreSessionID, current, next string) error
	UpdateProfile(ctx context.Context, coreSessionID, country, birthday string) error
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, password string) error
	Healthy(ctx context.Context) error
}

// Identity resolves the account behind a just-established core session and
// projects minimal account facts. VerifyPassword is the interim whoami
// mechanism (contract request CR-4: GET /v1/whoami).
type Identity interface {
	ResolveAccount(ctx context.Context, email, password string) (Account, error)
	GetAccount(ctx context.Context, accountID string) (Account, error)
}

// Social is the core's canonical friend graph (WEB-04). Every write goes
// through it.
type Social interface {
	RequestFriend(ctx context.Context, requesterID, addresseeID string) error
	AcceptFriend(ctx context.Context, addresseeID, requesterID string) error
	RemoveFriend(ctx context.Context, accountID, otherID string) error // also declines pending
	ListFriends(ctx context.Context, accountID string) ([]Friend, error)
	ListIncoming(ctx context.Context, accountID string) ([]Friend, error)
	ListOutgoing(ctx context.Context, accountID string) ([]Friend, error)
	Relationship(ctx context.Context, accountID, otherID string) (Relationship, error)
	Block(ctx context.Context, blockerID, blockedID string) error
	Unblock(ctx context.Context, blockerID, blockedID string) error
}

// Links projects adapter links owned by the core. The friend code shown on
// the website is the adapter-owned subject id, read through this contract
// (contract request CR-3 records the long-term shape).
type Links interface {
	ActiveLinkByAccount(ctx context.Context, namespace, accountID string) (Link, error)
	AccountBySubject(ctx context.Context, namespace, subjectID string) (string, error)
	// Unlink revokes the account's console link (journey 1: revoke the
	// console-created link). IdempotencyKey must be unique per attempt.
	Unlink(ctx context.Context, linkID, idempotencyKey string) error
}

// Events is the core's durable invalidation stream, surfaced read-only to
// moderators as the audit log (WEB-06).
type Events interface {
	Poll(ctx context.Context, sinceVersion uint64, limit int32) ([]Event, uint64, error)
}

// InvitationsView is WEB-05. The core's PollInvitations is adapter-scoped
// with delivery side effects, so a read-only website view needs CR-1
// (Invitations.ListForAccount). Until then the implementation returns
// ErrContractPending and pages render a clear notice.
type InvitationsView interface {
	ListPending(ctx context.Context, accountID string) ([]PendingInvitation, error)
}

type PendingInvitation struct {
	ID        string
	FromName  string
	TitleID   string
	ExpiresAt time.Time
}

// ErrIs reports whether err wraps target.
func ErrIs(err, target error) bool { return errors.Is(err, target) }
