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
	ErrNotAllowed         = errors.New("core: interaction not allowed")
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
	Version uint64
	EventID string
	Type    string
	// AccountID is who the event is about — and, for the notifiable types, who
	// gets told. SubjectID is who or what it concerns: the requester of a
	// friend request, the id of an invitation.
	AccountID string
	Namespace string
	SubjectID string
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
	// An empty field means "leave it alone", so the phone and the site can each
	// send only what their screen edits.
	UpdateProfile(ctx context.Context, coreSessionID, country, birthday, displayName string) error
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
	// VerifyPasswordByID re-checks the password of a signed-in account before
	// something that cannot be undone. A wrong one is ErrInvalidCredentials.
	VerifyPasswordByID(ctx context.Context, accountID, password string) error
	// RequestDeletion starts deleting the account: the core disables it at once
	// and revokes every one of its sessions.
	RequestDeletion(ctx context.Context, accountID string) error
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
	// CanMessage is the core's enforcement answer for actorID messaging targetID
	// (CheckInteraction, kind MESSAGE). A block in either direction is
	// ErrBlocked; any other refusal (not friends, an inactive account) is
	// ErrNotAllowed.
	CanMessage(ctx context.Context, actorID, targetID string) error
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
	// Link claims subjectID in namespace for the account and activates it: the website-side
	// link for consoles that cannot link themselves (Wii and DS, namespace "wfc"). A subject
	// already held by another account is ErrConflict.
	Link(ctx context.Context, accountID, namespace, subjectID, idempotencyKey string) error
}

// Events is the core's durable invalidation stream, surfaced read-only to
// moderators as the audit log (WEB-06).
type Events interface {
	Poll(ctx context.Context, sinceVersion uint64, limit int32) ([]Event, uint64, error)
}

// InvitationsView is WEB-05: a read of what is waiting for someone, plus the
// decline that a person can act on. Both go to the core's non-consuming
// ListInvitations/RejectInvitation, never to the adapter-scoped delivery path.
type InvitationsView interface {
	ListPending(ctx context.Context, accountID string) ([]PendingInvitation, error)
	Decline(ctx context.Context, accountID, invitationID string) (bool, error)
	Presence(ctx context.Context, accountIDs []string) (map[string]Presence, error)
	// PlayerCounts is who is playing what, right now. Accounts, not sessions.
	PlayerCounts(ctx context.Context) ([]TitlePlayers, map[string]int, int, error)
}

// TitlePlayers is how many people are in one title. The second return of
// PlayerCounts is players per adapter namespace, keyed by namespace.
type TitlePlayers struct {
	TitleID   string
	Namespace string
	Players   int
}

type PendingInvitation struct {
	ID        string
	FromName  string
	TitleID   string
	Namespace string
	ExpiresAt time.Time
}

// Presence is what is known about someone who is playing right now. Absence
// from a presence map means offline; there is no explicit offline state.
type Presence struct {
	Namespace string
	TitleID   string
	Since     time.Time
	NodeID    string // federation node the session came through; "home" for the core's own
}

// Node is a federation region: a relay tier somewhere that announces its
// addresses. Matchmaking hands them out per room, so a joiner lands where
// the host's room lives.
type Node struct {
	ID       string
	Name     string
	Country  string
	Services map[string]string
	LastSeen time.Time
	Approved bool
	Players  int
}

// Federation is the node register in the core.
type Federation interface {
	// ListNodes is what may be handed out: approved and recently seen, with
	// live player counts.
	ListNodes(ctx context.Context) ([]Node, error)
	ListAllNodes(ctx context.Context) ([]Node, error)
	CreateNode(ctx context.Context, id, name, country string) (key string, err error)
	SetNodeApproved(ctx context.Context, id string, approved bool) error
	// AnnounceAs relays a node's own announce, authenticated with the node's
	// key rather than the website's.
	AnnounceAs(ctx context.Context, nodeKey string, services map[string]string) (nodeID string, approved bool, err error)
}

// ErrIs reports whether err wraps target.
func ErrIs(err, target error) bool { return errors.Is(err, target) }
