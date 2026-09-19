package pages

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// fakeCore implements every core interface in memory, mirroring the
// contract semantics the website relies on.
type fakeCore struct {
	accounts       map[string]string // email -> password
	verified       map[string]bool
	sessions       map[string]string // coreSessionID -> email
	friends        []friendRow
	blocks         [][2]string
	profile        [2]string         // country, birthday as last saved
	names          map[string]string // accountID -> display name
	invitations    []core.PendingInvitation
	presence       map[string]core.Presence
	players        []core.TitlePlayers
	networkPlayers map[string]int
	totalPlayers   int
	links          map[string]string // accountID -> subject (switch)
	wfcLinks       map[string]string // accountID -> subject (wfc, linked from the website)
	subjectToAc    map[string]string // subject -> accountID
	publicCodes    map[string]string // published friend code -> accountID
	deleted        []string          // accounts whose deletion was requested
	twoFactor      map[string]bool   // email -> 2FA on (code fakeTOTP, recovery fakeRecovery)
	pending2FA     map[string]bool   // email -> setup started, not yet enabled
	banned         map[string]bool   // email -> banned
	resent         int               // verification resends asked for
	emailChange    map[string]string // email -> new address awaiting confirmation
}

const (
	fakeTOTP     = "123456"
	fakeRecovery = "abcde-fghij"
	// fakeEmailToken is the one confirmation token the fake core issues.
	fakeEmailToken = "email-tok"
)

type friendRow struct {
	requester, addressee string
	accepted             bool
	tombstoned           bool
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		accounts:    map[string]string{},
		verified:    map[string]bool{},
		sessions:    map[string]string{},
		links:       map[string]string{},
		wfcLinks:    map[string]string{},
		subjectToAc: map[string]string{},
		publicCodes: map[string]string{},
		names:       map[string]string{},
		twoFactor:   map[string]bool{},
		pending2FA:  map[string]bool{},
		banned:      map[string]bool{},
		emailChange: map[string]string{},
	}
}

func (f *fakeCore) secondFactorOK(code, recovery string) bool {
	return code == fakeTOTP || strings.EqualFold(recovery, fakeRecovery)
}

func (f *fakeCore) Login2FA(_ context.Context, challenge, code, recovery string) (string, error) {
	email, ok := strings.CutPrefix(challenge, "ch-")
	if !ok || !f.twoFactor[email] || !f.secondFactorOK(code, recovery) {
		return "", core.ErrInvalidCode
	}
	id := "sess-" + email
	f.sessions[id] = email
	return id, nil
}

func (f *fakeCore) Whoami(_ context.Context, sid string) (core.Account, error) {
	email, ok := f.sessions[sid]
	if !ok {
		return core.Account{}, core.ErrSessionInvalid
	}
	status := "active"
	if f.banned[email] {
		status = "banned"
	}
	id := "acct-" + email
	return core.Account{ID: id, Email: email, DisplayName: f.names[id], Status: status,
		EmailVerified: f.verified[email], TwoFactorEnabled: f.twoFactor[email]}, nil
}

func (f *fakeCore) ResendVerification(_ context.Context, _ string) error {
	f.resent++
	return nil
}

func (f *fakeCore) ChangeEmail(_ context.Context, sid, newEmail, password string) error {
	email := f.sessions[sid]
	if f.accounts[email] != password {
		return core.ErrInvalidCredentials
	}
	if !strings.Contains(newEmail, "@") {
		return core.ErrConflict
	}
	f.emailChange[email] = newEmail
	return nil
}

// ConfirmEmail moves every pending change: the fake issues one token.
func (f *fakeCore) ConfirmEmail(_ context.Context, token string) error {
	if token != fakeEmailToken || len(f.emailChange) == 0 {
		return core.ErrNotFound
	}
	for old, next := range f.emailChange {
		f.accounts[next], f.verified[next] = f.accounts[old], true
		for sid, e := range f.sessions {
			if e == old {
				f.sessions[sid] = next
			}
		}
		delete(f.emailChange, old)
	}
	return nil
}

func (f *fakeCore) TwoFactorSetup(_ context.Context, sid, password string) (core.TwoFactorSecret, error) {
	email := f.sessions[sid]
	if f.accounts[email] != password {
		return core.TwoFactorSecret{}, core.ErrInvalidCredentials
	}
	if f.twoFactor[email] {
		return core.TwoFactorSecret{}, core.ErrConflict
	}
	f.pending2FA[email] = true
	return core.TwoFactorSecret{Secret: "JBSWY3DPEHPK3PXP", OTPAuthURI: "otpauth://totp/OpenPak:" + email + "?secret=JBSWY3DPEHPK3PXP&issuer=OpenPak"}, nil
}

func (f *fakeCore) TwoFactorEnable(_ context.Context, sid, code string) ([]string, error) {
	email := f.sessions[sid]
	if !f.pending2FA[email] {
		return nil, core.ErrConflict
	}
	if code != fakeTOTP {
		return nil, core.ErrInvalidCode
	}
	delete(f.pending2FA, email)
	f.twoFactor[email] = true
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = fmt.Sprintf("rcode-%02d", i)
	}
	return codes, nil
}

func (f *fakeCore) TwoFactorDisable(_ context.Context, sid, password, code string) error {
	email := f.sessions[sid]
	switch {
	case f.accounts[email] != password:
		return core.ErrInvalidCredentials
	case !f.twoFactor[email]:
		return core.ErrConflict
	case !f.secondFactorOK(code, code):
		return core.ErrInvalidCode
	}
	delete(f.twoFactor, email)
	return nil
}

func (f *fakeCore) VerifySecondFactor(_ context.Context, accountID, code, recovery string) error {
	if !f.twoFactor[strings.TrimPrefix(accountID, "acct-")] {
		return core.ErrConflict
	}
	if !f.secondFactorOK(code, recovery) {
		return core.ErrInvalidCode
	}
	return nil
}

func (f *fakeCore) ListBlocks(_ context.Context, accountID string) ([]core.Blocked, error) {
	var out []core.Blocked
	for _, b := range f.blocks {
		if b[0] == accountID {
			out = append(out, core.Blocked{AccountID: b[1], DisplayName: "name-" + b[1], BlockedAt: time.Unix(1700000000, 0)})
		}
	}
	return out, nil
}

func (f *fakeCore) AccountByPublicCode(_ context.Context, _, code string) (string, error) {
	if id, ok := f.publicCodes[code]; ok {
		return id, nil
	}
	return "", core.ErrNotFound
}

func (f *fakeCore) Register(_ context.Context, email, password, _ string) error {
	if _, ok := f.accounts[email]; ok {
		return nil // masked re-registration (enumeration resistance)
	}
	f.accounts[email] = password
	return nil
}
func (f *fakeCore) VerifyEmail(_ context.Context, token string) error {
	f.verified[tokenForEmail(token)] = true
	return nil
}
func tokenForEmail(email string) string { return "tok-" + email }

func (f *fakeCore) Login(_ context.Context, email, password string) (string, error) {
	if pw, ok := f.accounts[email]; !ok || pw != password || !f.verified[email] {
		return "", core.ErrInvalidCredentials
	}
	if f.twoFactor[email] {
		return "", &core.LoginChallenge{Challenge: "ch-" + email}
	}
	id := "sess-" + email
	f.sessions[id] = email
	return id, nil
}
func (f *fakeCore) Logout(_ context.Context, sid string) error {
	delete(f.sessions, sid)
	return nil
}
func (f *fakeCore) ListSessions(_ context.Context, sid string) ([]core.Session, error) {
	if _, ok := f.sessions[sid]; !ok {
		return nil, core.ErrSessionInvalid
	}
	return []core.Session{{ID: sid, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}}, nil
}
func (f *fakeCore) RevokeSession(_ context.Context, sid, id string) error {
	if _, ok := f.sessions[sid]; !ok {
		return core.ErrNotFound
	}
	delete(f.sessions, id)
	return nil
}

// Registration with a profile goes through the same fake path; the profile itself is the
// core's business, not this package's.
func (f *fakeCore) RegisterWithProfile(ctx context.Context, email, password, displayName, _, _ string) error {
	return f.Register(ctx, email, password, displayName)
}

// The core checks the current password; the fake mirrors that so the handler's failure path
// is exercised too.
func (f *fakeCore) ChangePassword(_ context.Context, _, current, next string) error {
	if f.accounts["a@x.test"] != current {
		return core.ErrInvalidCredentials
	}
	f.accounts["a@x.test"] = next
	return nil
}
func (f *fakeCore) UpdateProfile(_ context.Context, sid, country, birthday, displayName string) error {
	// Mirror the core's own rule: an ISO date or nothing.
	if birthday != "" {
		if _, err := time.Parse("2006-01-02", birthday); err != nil {
			return core.ErrNotFound
		}
	}
	f.profile = [2]string{country, birthday}
	// Like the core, an empty name keeps the current one.
	if displayName != "" {
		f.names["acct-"+f.sessions[sid]] = displayName
	}
	return nil
}
func (f *fakeCore) ForgotPassword(_ context.Context, _ string) error { return nil }
func (f *fakeCore) ResetPassword(_ context.Context, _, password string) error {
	f.accounts["a@x.test"] = password
	return nil
}
func (f *fakeCore) Healthy(_ context.Context) error { return nil }

func (f *fakeCore) GetAccount(_ context.Context, accountID string) (core.Account, error) {
	email := strings.TrimPrefix(accountID, "acct-")
	return core.Account{ID: accountID, DisplayName: f.names[accountID],
		EmailVerified: f.verified[email], TwoFactorEnabled: f.twoFactor[email]}, nil
}

// The accounts are keyed by email and their ids are "acct-" + email, so the
// password check by id looks the email back up. Like the core with
// second_factor_supported set, a 2FA account passes with the flag raised.
func (f *fakeCore) VerifyPasswordByID(_ context.Context, accountID, password string) (bool, error) {
	email := strings.TrimPrefix(accountID, "acct-")
	if pw, ok := f.accounts[email]; !ok || pw != password {
		return false, core.ErrInvalidCredentials
	}
	return f.twoFactor[email], nil
}
func (f *fakeCore) RequestDeletion(_ context.Context, accountID string) error {
	f.deleted = append(f.deleted, accountID)
	return nil
}

func (f *fakeCore) findFriend(a, b string) *friendRow {
	for i := range f.friends {
		r := &f.friends[i]
		if !r.tombstoned &&
			((r.requester == a && r.addressee == b) || (r.requester == b && r.addressee == a)) {
			return r
		}
	}
	return nil
}

func (f *fakeCore) RequestFriend(_ context.Context, requester, addressee string) error {
	if requester == addressee {
		return core.ErrConflict
	}
	if f.findFriend(requester, addressee) != nil {
		return nil // idempotent while pending
	}
	f.friends = append(f.friends, friendRow{requester: requester, addressee: addressee})
	return nil
}
func (f *fakeCore) AcceptFriend(_ context.Context, addressee, requester string) error {
	r := f.findFriend(requester, addressee)
	if r == nil || r.accepted {
		return core.ErrConflict
	}
	r.accepted = true
	return nil
}
func (f *fakeCore) RemoveFriend(_ context.Context, accountID, otherID string) error {
	if r := f.findFriend(accountID, otherID); r != nil {
		r.tombstoned = true
	}
	return nil
}
func (f *fakeCore) friendsOf(accountID string, accepted bool) []core.Friend {
	var out []core.Friend
	for _, r := range f.friends {
		if r.tombstoned || r.accepted != accepted {
			continue
		}
		switch {
		case accepted && r.requester == accountID:
			out = append(out, core.Friend{AccountID: r.addressee, DisplayName: r.addressee})
		case accepted && r.addressee == accountID:
			out = append(out, core.Friend{AccountID: r.requester, DisplayName: r.requester})
		case !accepted && r.addressee == accountID: // incoming pending
			out = append(out, core.Friend{AccountID: r.requester, DisplayName: r.requester})
		case !accepted && r.requester == accountID: // outgoing pending
			out = append(out, core.Friend{AccountID: r.addressee, DisplayName: r.addressee})
		}
	}
	return out
}
func (f *fakeCore) ListFriends(_ context.Context, accountID string) ([]core.Friend, error) {
	return f.friendsOf(accountID, true), nil
}
func (f *fakeCore) ListIncoming(_ context.Context, accountID string) ([]core.Friend, error) {
	var out []core.Friend
	for _, fr := range f.friendsOf(accountID, false) {
		if r := f.findFriend(fr.AccountID, accountID); r != nil && r.requester == fr.AccountID {
			out = append(out, fr)
		}
	}
	return out, nil
}
func (f *fakeCore) ListOutgoing(_ context.Context, accountID string) ([]core.Friend, error) {
	var out []core.Friend
	for _, fr := range f.friendsOf(accountID, false) {
		if r := f.findFriend(accountID, fr.AccountID); r != nil && r.addressee == fr.AccountID {
			out = append(out, fr)
		}
	}
	return out, nil
}
func (f *fakeCore) Relationship(_ context.Context, accountID, otherID string) (core.Relationship, error) {
	r := f.findFriend(accountID, otherID)
	switch {
	case r == nil:
		return core.RelNone, nil
	case r.accepted:
		return core.RelFriends, nil
	case r.requester == accountID:
		return core.RelPendingOutgoing, nil
	default:
		return core.RelPendingIncoming, nil
	}
}
func (f *fakeCore) Block(_ context.Context, blocker, blocked string) error {
	f.blocks = append(f.blocks, [2]string{blocker, blocked})
	return nil
}
func (f *fakeCore) Unblock(_ context.Context, blocker, blocked string) error {
	for i, b := range f.blocks {
		if b[0] == blocker && b[1] == blocked {
			f.blocks = append(f.blocks[:i], f.blocks[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeCore) CanMessage(_ context.Context, actorID, targetID string) error {
	for _, b := range f.blocks {
		if (b[0] == actorID && b[1] == targetID) || (b[0] == targetID && b[1] == actorID) {
			return core.ErrBlocked
		}
	}
	if r := f.findFriend(actorID, targetID); r == nil || !r.accepted {
		return core.ErrNotAllowed
	}
	return nil
}

func (f *fakeCore) ActiveLinkByAccount(_ context.Context, namespace, accountID string) (core.Link, error) {
	if namespace == "wfc" {
		sub, ok := f.wfcLinks[accountID]
		if !ok {
			return core.Link{}, core.ErrNotFound
		}
		return core.Link{LinkID: "wlink-" + accountID, Namespace: namespace, SubjectID: sub}, nil
	}
	sub, ok := f.links[accountID]
	if !ok {
		return core.Link{}, core.ErrNotFound
	}
	return core.Link{LinkID: "link-" + accountID, SubjectID: sub}, nil
}
func (f *fakeCore) Link(_ context.Context, accountID, namespace, subjectID, _ string) error {
	if namespace != "wfc" {
		return errors.New("fake: only wfc links are created from the website")
	}
	for other, sub := range f.wfcLinks {
		if sub == subjectID && other != accountID {
			return core.ErrConflict
		}
	}
	f.wfcLinks[accountID] = subjectID
	return nil
}
func (f *fakeCore) AccountBySubject(_ context.Context, _, subject string) (string, error) {
	id, ok := f.subjectToAc[subject]
	if !ok {
		return "", core.ErrNotFound
	}
	return id, nil
}
func (f *fakeCore) Unlink(_ context.Context, linkID, _ string) error {
	accountID := linkID[len("link-"):]
	sub := f.links[accountID]
	delete(f.links, accountID)
	delete(f.subjectToAc, sub)
	return nil
}

func (f *fakeCore) Poll(_ context.Context, since uint64, _ int32) ([]core.Event, uint64, error) {
	const max = uint64(2)
	if since >= max {
		return nil, max, nil
	}
	return []core.Event{
		{Version: 1, EventID: "e1", Type: "account_banned", AccountID: "acct-b@x.test"},
		{Version: 2, EventID: "e2", Type: "account_unbanned", AccountID: "acct-b@x.test"},
	}, max, nil
}

func (f *fakeCore) ListPending(_ context.Context, _ string) ([]core.PendingInvitation, error) {
	return f.invitations, nil
}

func (f *fakeCore) Decline(_ context.Context, _, invitationID string) (bool, error) {
	for i, in := range f.invitations {
		if in.ID == invitationID {
			f.invitations = append(f.invitations[:i], f.invitations[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeCore) PlayerCounts(_ context.Context) ([]core.TitlePlayers, map[string]int, int, error) {
	return f.players, f.networkPlayers, f.totalPlayers, nil
}
func (f *fakeCore) Presence(_ context.Context, ids []string) (map[string]core.Presence, error) {
	out := map[string]core.Presence{}
	for _, id := range ids {
		if p, ok := f.presence[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

// newTestServer wires a fully in-process pages.Server with templates.
func newTestServer(t *testing.T) (*Server, *fakeCore, *store.Memory) {
	t.Helper()
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	fc := newFakeCore()
	db := store.NewMemory()
	sessions := session.New("opak_session", "test-secret-0123456789abcdef-test!", false, time.Hour)
	st, err := media.NewFSStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(tpl, discardLogger())
	s.Core = fc
	s.Identity = fc
	s.Social = fc
	s.Links = fc
	s.Events = fc
	s.Invites = fc
	s.DB = db
	s.Media = media.New(st)
	s.Sessions = sessions
	s.AuthLimit = ratelimit.New(1000, time.Minute)
	s.MediaLimit = ratelimit.New(1000, time.Hour)
	return s, fc, db
}

// seedTwoAccounts wires two verified accounts; A owns the switch link whose
// subject is its friend code.
func seedTwoAccounts(fc *fakeCore) (emailA, emailB string) {
	emailA, emailB = "a@x.test", "b@x.test"
	fc.accounts[emailA] = "password-A-1"
	fc.verified[emailA] = true
	fc.accounts[emailB] = "password-B-1"
	fc.verified[emailB] = true
	fc.subjectToAc["0123456789ab"] = "acct-" + emailA
	fc.links["acct-"+emailA] = "0123456789ab"
	return emailA, emailB
}

func TestFmtFriendCode(t *testing.T) {
	cases := map[string]string{
		"0123456789ab":      "SW-0123-4567-89ab",
		"SW-0123-4567-89ab": "SW-0123-4567-89ab",
		"not-a-code":        "not-a-code",
		"0123456789ab00":    "0123456789ab00", // wrong length: verbatim
		"zzzz":              "zzzz",
	}
	for in, want := range cases {
		if got := fmtFriendCode(in); got != want {
			t.Errorf("fmtFriendCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeFriendCode(t *testing.T) {
	cases := map[string]string{
		"SW-0123-4567-89ab":  "0123456789ab",
		"0123-4567-89ab":     "0123456789ab",
		"0123456789ab":       "0123456789ab",
		"sw-0123-4567-89ab":  "0123456789ab",
		"SW-0123-4567-89ab-": "0123456789ab", // trailing dash is display noise
	}
	for in, want := range cases {
		if got := normalizeFriendCode(in); got != want {
			t.Errorf("normalizeFriendCode(%q) = %q, want %q", in, got, want)
		}
	}
}
