package pages

import (
	"context"
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
	accounts    map[string]string // email -> password
	verified    map[string]bool
	sessions    map[string]string // coreSessionID -> email
	friends     []friendRow
	blocks      [][2]string
	links       map[string]string // accountID -> subject (switch)
	subjectToAc map[string]string // subject -> accountID
}

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
		subjectToAc: map[string]string{},
	}
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
func (f *fakeCore) ForgotPassword(_ context.Context, _ string) error { return nil }
func (f *fakeCore) ResetPassword(_ context.Context, _, password string) error {
	f.accounts["a@x.test"] = password
	return nil
}
func (f *fakeCore) Healthy(_ context.Context) error { return nil }

func (f *fakeCore) ResolveAccount(_ context.Context, email, password string) (core.Account, error) {
	if pw, ok := f.accounts[email]; !ok || pw != password {
		return core.Account{}, core.ErrInvalidCredentials
	}
	return core.Account{ID: "acct-" + email, Email: email}, nil
}
func (f *fakeCore) GetAccount(_ context.Context, accountID string) (core.Account, error) {
	return core.Account{ID: accountID}, nil
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

func (f *fakeCore) ActiveLinkByAccount(_ context.Context, _, accountID string) (core.Link, error) {
	sub, ok := f.links[accountID]
	if !ok {
		return core.Link{}, core.ErrNotFound
	}
	return core.Link{LinkID: "link-" + accountID, SubjectID: sub}, nil
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
	return nil, core.ErrContractPending
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
