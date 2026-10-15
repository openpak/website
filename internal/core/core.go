// Package core is the website's only doorway to the account core: versioned
// HTTP (/v1/*) and gRPC contracts (Identity, Links, Social, Events,
// Moderation, Media). The
// website never reads core storage. Interfaces here exist so handlers and
// tests stay independent of transport details.
package core

import (
	"context"
	"errors"
	"strings"
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
	// ErrInvalidCode is a wrong or reused second-factor code (TOTP or recovery),
	// or a sign-in challenge that is no longer valid.
	ErrInvalidCode = errors.New("core: invalid code")
	// ErrTwoFactorLocked is the core's per-account lock after five wrong codes
	// in 15 minutes (HTTP 429, gRPC ResourceExhausted).
	ErrTwoFactorLocked = errors.New("core: too many wrong codes; try again in 15 minutes")
	// ErrSecondFactorRequired is a step-up on a 2FA account that came without
	// its code.
	ErrSecondFactorRequired = errors.New("core: second factor required")
)

// LoginChallenge is what Login returns when the password was right but the
// account has two-factor authentication: no session yet, only a challenge
// (10 minutes, single use) for Login2FA.
type LoginChallenge struct{ Challenge string }

func (e *LoginChallenge) Error() string { return "core: second factor required to sign in" }

// TwoFactorSecret is a pending enrolment: the base32 secret for an
// authenticator app and the same thing as an otpauth:// URI.
type TwoFactorSecret struct {
	Secret     string
	OTPAuthURI string
}

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
	// Status is active, banned, deletion-pending or deleted.
	Status           string
	EmailVerified    bool
	TwoFactorEnabled bool
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
	// PublicCode is the human code the adapter published for the link (the
	// Switch friend code); empty until the adapter publishes one.
	PublicCode string
}

// Blocked is one account the viewer has blocked.
type Blocked struct {
	AccountID   string
	DisplayName string
	BlockedAt   time.Time
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
	// Login signs in. A 2FA account answers *LoginChallenge instead of a
	// session; Login2FA finishes it.
	Login(ctx context.Context, email, password string) (coreSessionID string, err error)
	// Login2FA completes a challenged sign-in with a TOTP code or a recovery
	// code. A wrong code or a dead challenge is ErrInvalidCode; the core's lock
	// is ErrTwoFactorLocked.
	Login2FA(ctx context.Context, challenge, code, recoveryCode string) (coreSessionID string, err error)
	// Whoami names the account behind a core session (CR-4).
	Whoami(ctx context.Context, coreSessionID string) (Account, error)
	// ResendVerification mails a fresh verification link; the answer is the
	// same whether or not one was sent.
	ResendVerification(ctx context.Context, coreSessionID string) error
	// ChangeEmail starts moving the account to a new address; nothing changes
	// until the link mailed there is confirmed. A wrong password is
	// ErrInvalidCredentials.
	ChangeEmail(ctx context.Context, coreSessionID, newEmail, password string) error
	// ConfirmEmail applies an email change from its mailed token; a bad token
	// is ErrNotFound.
	ConfirmEmail(ctx context.Context, token string) error
	// TwoFactorSetup starts enrolment (password required); ErrConflict when
	// 2FA is already on.
	TwoFactorSetup(ctx context.Context, coreSessionID, password string) (TwoFactorSecret, error)
	// TwoFactorEnable finishes enrolment with a first code and returns the ten
	// recovery codes, which are never shown again.
	TwoFactorEnable(ctx context.Context, coreSessionID, code string) (recoveryCodes []string, err error)
	// TwoFactorDisable takes the password and a current code (a recovery code counts).
	TwoFactorDisable(ctx context.Context, coreSessionID, password, code string) error
	Logout(ctx context.Context, coreSessionID string) error
	ListSessions(ctx context.Context, coreSessionID string) ([]Session, error)
	RevokeSession(ctx context.Context, coreSessionID, sessionID string) error
	ChangePassword(ctx context.Context, coreSessionID, current, next string) error
	// An empty display name leaves the current one. Country and birthday are
	// stored as sent, so an empty one clears it.
	UpdateProfile(ctx context.Context, coreSessionID, country, birthday, displayName string) error
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, password string) error
	Healthy(ctx context.Context) error
}

// Identity projects account facts and runs the step-up checks. Who a session
// belongs to is HTTP.Whoami (CR-4).
type Identity interface {
	GetAccount(ctx context.Context, accountID string) (Account, error)
	// VerifyPasswordByID re-checks the password of a signed-in account before
	// something that cannot be undone. A wrong one is ErrInvalidCredentials.
	// twoFactor reports that the account has 2FA, and the caller must then
	// also pass VerifySecondFactor (core docs/two-factor.md).
	VerifyPasswordByID(ctx context.Context, accountID, password string) (twoFactor bool, err error)
	// VerifySecondFactor checks a TOTP code or a recovery code: ErrInvalidCode,
	// ErrTwoFactorLocked.
	VerifySecondFactor(ctx context.Context, accountID, code, recoveryCode string) error
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
	ListBlocks(ctx context.Context, accountID string) ([]Blocked, error)
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
	// AccountByPublicCode resolves a code an adapter published, as typed.
	AccountByPublicCode(ctx context.Context, namespace, code string) (string, error)
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

// EventPage is the core's own cap on one PollEvents page.
const EventPage = 1000

// EventHead walks the core's event stream forward from a hint and returns the
// newest version. The core only pages forward — PollEvents is oldest-first and
// its max_version is the page's, not the stream's — so there is no cheaper way
// to learn the head. The walk is capped: past that the answer is a lower bound,
// which is still more useful than a page that never loads.
func EventHead(ctx context.Context, ev Events, from uint64) (uint64, error) {
	head := from
	for range 100 {
		page, max, err := ev.Poll(ctx, head, EventPage)
		if err != nil {
			return head, err
		}
		if max > head {
			head = max
		}
		if len(page) < EventPage {
			break
		}
	}
	return head, nil
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

// Playtime is how long titles have been played online, per client and OS,
// across every account. Separate from InvitationsView so a core without it (or
// a test fake) simply leaves the titles page on player reports alone.
type Playtime interface {
	Playtime(ctx context.Context) ([]PlayTally, error)
}

// PlayTally is one title on one client and OS: Client and OS are the core's
// tokens ("ryujinx", "macos"; OS "" for a console or an emulator that did not say).
type PlayTally struct {
	Namespace, TitleID, Client, OS string
	Total, Longest                 time.Duration
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
	// Client is what the session runs on inside the namespace: "switch",
	// "ryujinx", "eden", "citron", "wiiu", "3ds", "cemu", "azahar"; "" when
	// the adapter could not tell.
	Client string
}

// Label names what somebody is on, for "Playing Kirby on Ryujinx": the
// client when one is known, the platform's short name otherwise. The same
// table lives in nx-baas and nn-friends.
func (p Presence) Label() string { return ClientLabel(p.Namespace, p.Client) }

// platformLabels is what a real console's network is called, by presence namespace.
var platformLabels = map[string]string{
	"switch": "Switch", "wiiu": "Wii U", "wii": "Wii", "3ds": "3DS", "nds": "NDS", "ds": "NDS",
	"np": "PS3", "ps3": "PS3", "xbox360": "Xbox 360", "xbox-360": "Xbox 360", "xbl": "Xbox 360",
}

// emulatorLabels is what an emulator calls itself, by the client it reports.
var emulatorLabels = map[string]string{
	"harbor": "Harbor", "ryujinx": "Ryujinx", "eden": "Eden", "citron": "Citron",
	"pier": "Pier", "wharf": "Wharf", "cemu": "Cemu", "azahar": "Azahar", "dolphin": "Dolphin",
	"melonds": "melonDS", "xemu": "xemu", "xenia": "Xenia", "pcsx2": "PCSX2", "rpcs3": "RPCS3",
	"ppsspp": "PPSSPP", "vita3k": "Vita3K",
}

// ClientLabel names what somebody is on: the emulator when the client is one, the console's
// platform when it is real hardware (no client, or the console's own), so "PS3" is a PS3 and
// "Pier" is somebody on the emulator. The same table lives in nx-baas, the website, nn-friends
// and np-account.
func ClientLabel(namespace, client string) string {
	c := strings.ToLower(strings.TrimSpace(client))
	if e, ok := emulatorLabels[c]; ok {
		return e
	}
	if p, ok := platformLabels[c]; ok {
		return p
	}
	if c != "" {
		return strings.ToUpper(c[:1]) + c[1:] // an emulator this table has not heard of yet
	}
	ns := strings.ToLower(strings.TrimSpace(namespace))
	if p, ok := platformLabels[ns]; ok {
		return p
	}
	if ns == "" {
		return ""
	}
	return strings.ToUpper(ns[:1]) + ns[1:]
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

// ModeratedAccount is one search hit for moderators.
type ModeratedAccount struct {
	ID              string
	Email           string
	DisplayName     string
	Status          string // active, banned, deletion-pending, deleted
	EmailVerified   bool
	CreatedAt       time.Time
	BanReason       string
	MatchedSubjects []string // "namespace:subject" of links the query matched
}

// Report is one entry of the moderation queue.
type Report struct {
	ID           string
	ReporterID   string
	ReporterName string
	SubjectID    string
	SubjectName  string
	Kind         string
	Detail       string
	Status       string // open, resolved
	Resolution   string
	ResolvedBy   string
	CreatedAt    time.Time
	ResolvedAt   time.Time
}

// Moderation is the core's moderation service (CR-5, CR-6). The core audits
// every ban and resolution under the actor it is given.
type Moderation interface {
	SearchAccounts(ctx context.Context, query string, limit int) ([]ModeratedAccount, error)
	SetBanned(ctx context.Context, accountID string, banned bool, reason, actor string) error
	// ListReports takes "open", "resolved" or "" for every status.
	ListReports(ctx context.Context, status string, limit int) ([]Report, error)
	ResolveReport(ctx context.Context, reportID, resolution, actor string) error
}

// ErrIs reports whether err wraps target.
func ErrIs(err, target error) bool { return errors.Is(err, target) }
