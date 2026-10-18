package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// fakeCore mirrors contract semantics the API relies on.
type fakeCore struct {
	accounts       map[string]string
	verified       map[string]bool
	sessions       map[string]string
	friends        map[string][]string
	links          map[string]string
	wfcLinks       map[string]string // namespace "wfc": the console a typed friend code resolved to
	subjectToAc    map[string]string
	deleted        []string // accounts whose deletion was requested
	blocks         [][2]string
	invitations    []core.PendingInvitation
	presence       map[string]core.Presence
	players        []core.TitlePlayers
	networkPlayers map[string]int
	totalPlayers   int
	profile        [3]string         // country, birthday, display name as last saved
	down           error             // when set, the core answers nothing at all
	twoFactor      map[string]bool   // email -> 2FA on
	publicCodes    map[string]string // published friend code -> account id
	resent         int
	newEmail       string
	status         map[string]string // account id -> core status; absent = active
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		accounts:    map[string]string{},
		verified:    map[string]bool{},
		sessions:    map[string]string{},
		friends:     map[string][]string{},
		links:       map[string]string{},
		subjectToAc: map[string]string{},
		twoFactor:   map[string]bool{},
		publicCodes: map[string]string{},
	}
}

func (f *fakeCore) Register(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeCore) VerifyEmail(_ context.Context, _ string) error    { return nil }
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
	if f.down != nil {
		return nil, f.down
	}
	if _, ok := f.sessions[sid]; !ok {
		return nil, core.ErrSessionInvalid
	}
	return []core.Session{{ID: sid, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}}, nil
}
func (f *fakeCore) RevokeSession(_ context.Context, _, _ string) error     { return nil }
func (f *fakeCore) ChangePassword(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeCore) ListPending(_ context.Context, _ string) ([]core.PendingInvitation, error) {
	return f.invitations, nil
}
func (f *fakeCore) Decline(_ context.Context, _, id string) (bool, error) {
	for i, in := range f.invitations {
		if in.ID == id {
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
func (f *fakeCore) UpdateProfile(_ context.Context, _, country, birthday, displayName string) error {
	// Mirror the core's own validation: the API must pass its complaint on
	// rather than flatten it.
	if birthday != "" {
		if _, err := time.Parse("2006-01-02", birthday); err != nil {
			return errors.New("identity: birthday must be YYYY-MM-DD")
		}
	}
	f.profile = [3]string{country, birthday, displayName}
	return nil
}
func (f *fakeCore) RegisterWithProfile(ctx context.Context, email, password, displayName, _, _, _ string) error {
	return f.Register(ctx, email, password, displayName)
}
func (f *fakeCore) ForgotPassword(_ context.Context, _ string) error { return nil }
func (f *fakeCore) ResetPassword(_ context.Context, token, password string) error {
	// Mirror the core: a token it did not issue is refused.
	if token != "reset-a" {
		return core.ErrNotFound
	}
	f.accounts["a@x.test"] = password
	return nil
}
func (f *fakeCore) Healthy(_ context.Context) error { return nil }
func (f *fakeCore) GetAccount(_ context.Context, id string) (core.Account, error) {
	email := strings.TrimPrefix(id, "acct-")
	st := f.status[id]
	if st == "" {
		st = "active"
	}
	if st == "missing" {
		return core.Account{}, core.ErrNotFound
	}
	return core.Account{ID: id, Country: f.profile[0], Birthday: f.profile[1], Status: st,
		EmailVerified: f.verified[email], TwoFactorEnabled: f.twoFactor[email]}, nil
}

// The accounts are keyed by email and their ids are "acct-" + email, so the
// password check by id looks the email back up. As the core does for a caller
// that sets second_factor_supported, a 2FA account passes with the flag up.
func (f *fakeCore) VerifyPasswordByID(_ context.Context, accountID, password string) (bool, error) {
	email := strings.TrimPrefix(accountID, "acct-")
	if pw, ok := f.accounts[email]; !ok || pw != password {
		return false, core.ErrInvalidCredentials
	}
	return f.twoFactor[email], nil
}

const (
	fakeTOTP     = "123456"
	fakeRecovery = "abcde-fghij"
)

func (f *fakeCore) VerifySecondFactor(_ context.Context, accountID, code, recovery string) error {
	if code == fakeTOTP || recovery == fakeRecovery {
		return nil
	}
	return core.ErrInvalidCode
}
func (f *fakeCore) Login2FA(_ context.Context, challenge, code, recovery string) (string, error) {
	email, ok := strings.CutPrefix(challenge, "ch-")
	if !ok || !f.twoFactor[email] || (code != fakeTOTP && recovery != fakeRecovery) {
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
	return core.Account{ID: "acct-" + email, Email: email, Status: "active",
		EmailVerified: f.verified[email], TwoFactorEnabled: f.twoFactor[email]}, nil
}
func (f *fakeCore) ResendVerification(_ context.Context, _ string) error {
	f.resent++
	return nil
}
func (f *fakeCore) ChangeEmail(_ context.Context, sid, newEmail, password string) error {
	if f.accounts[f.sessions[sid]] != password {
		return core.ErrInvalidCredentials
	}
	f.newEmail = newEmail
	return nil
}
func (f *fakeCore) ConfirmEmail(_ context.Context, token string) error {
	if token != "email-tok" {
		return core.ErrNotFound
	}
	return nil
}
func (f *fakeCore) TwoFactorSetup(_ context.Context, sid, password string) (core.TwoFactorSecret, error) {
	if f.accounts[f.sessions[sid]] != password {
		return core.TwoFactorSecret{}, core.ErrInvalidCredentials
	}
	return core.TwoFactorSecret{Secret: "JBSWY3DPEHPK3PXP", OTPAuthURI: "otpauth://totp/OpenPak?secret=JBSWY3DPEHPK3PXP"}, nil
}
func (f *fakeCore) TwoFactorEnable(_ context.Context, sid, code string) ([]string, error) {
	if code != fakeTOTP {
		return nil, core.ErrInvalidCode
	}
	f.twoFactor[f.sessions[sid]] = true
	return []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "r10"}, nil
}
func (f *fakeCore) TwoFactorDisable(_ context.Context, sid, password, code string) error {
	email := f.sessions[sid]
	switch {
	case f.accounts[email] != password:
		return core.ErrInvalidCredentials
	case !f.twoFactor[email]:
		return fmt.Errorf("%w: two-factor authentication is not enabled", core.ErrConflict)
	case code != fakeTOTP && code != fakeRecovery:
		return core.ErrInvalidCode
	}
	delete(f.twoFactor, email)
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
func (f *fakeCore) RequestDeletion(_ context.Context, accountID string) error {
	f.deleted = append(f.deleted, accountID)
	return nil
}
func (f *fakeCore) RequestFriend(_ context.Context, requester, addressee string) error {
	f.friends[requester] = append(f.friends[requester], addressee)
	return nil
}
func (f *fakeCore) AcceptFriend(_ context.Context, _, _ string) error { return nil }
func (f *fakeCore) RemoveFriend(_ context.Context, accountID, otherID string) error {
	// Either direction, as the core's pending and accepted rows can be.
	f.friends[accountID] = slices.DeleteFunc(f.friends[accountID], func(id string) bool { return id == otherID })
	f.friends[otherID] = slices.DeleteFunc(f.friends[otherID], func(id string) bool { return id == accountID })
	return nil
}
func (f *fakeCore) ListFriends(_ context.Context, id string) ([]core.Friend, error) {
	var out []core.Friend
	for _, other := range f.friends[id] {
		out = append(out, core.Friend{AccountID: other, DisplayName: other})
	}
	return out, nil
}
func (f *fakeCore) ListIncoming(_ context.Context, _ string) ([]core.Friend, error) {
	return nil, nil
}
func (f *fakeCore) ListOutgoing(_ context.Context, _ string) ([]core.Friend, error) {
	return nil, nil
}
func (f *fakeCore) Relationship(_ context.Context, _, _ string) (core.Relationship, error) {
	return core.RelNone, nil
}
func (f *fakeCore) Block(_ context.Context, blocker, blocked string) error {
	f.blocks = append(f.blocks, [2]string{blocker, blocked})
	return nil
}
func (f *fakeCore) Unblock(_ context.Context, _, _ string) error { return nil }
func (f *fakeCore) CanMessage(_ context.Context, actorID, targetID string) error {
	for _, b := range f.blocks {
		if (b[0] == actorID && b[1] == targetID) || (b[0] == targetID && b[1] == actorID) {
			return core.ErrBlocked
		}
	}
	if !slices.Contains(f.friends[actorID], targetID) && !slices.Contains(f.friends[targetID], actorID) {
		return core.ErrNotAllowed
	}
	return nil
}
func (f *fakeCore) ActiveLinkByAccount(_ context.Context, namespace, accountID string) (core.Link, error) {
	links := f.links
	if namespace == "wfc" {
		links = f.wfcLinks
	}
	sub, ok := links[accountID]
	if !ok {
		return core.Link{}, core.ErrNotFound
	}
	return core.Link{LinkID: "link-" + namespace + "-" + accountID, Namespace: namespace, SubjectID: sub}, nil
}
func (f *fakeCore) AccountBySubject(_ context.Context, _, subject string) (string, error) {
	id, ok := f.subjectToAc[subject]
	if !ok {
		return "", core.ErrNotFound
	}
	return id, nil
}
func (f *fakeCore) Unlink(_ context.Context, linkID, _ string) error {
	delete(f.wfcLinks, strings.TrimPrefix(linkID, "link-wfc-"))
	delete(f.links, strings.TrimPrefix(linkID, "link-switch-"))
	return nil
}
func (f *fakeCore) Link(_ context.Context, accountID, namespace, subject, _ string) error {
	if namespace == "wfc" {
		f.wfcLinks[accountID] = subject
	}
	return nil
}

type apiDeps struct {
	Core     core.HTTP
	Identity core.Identity
	Social   core.Social
	Links    core.Links
	DB       store.Store
	Sessions *session.Manager
}

func newTestAPI(t *testing.T) (*Server, *fakeCore, *store.Memory) {
	t.Helper()
	fc := &fakeCore{
		accounts:    map[string]string{"a@x.test": "password-A-1"},
		verified:    map[string]bool{"a@x.test": true},
		sessions:    map[string]string{},
		friends:     map[string][]string{},
		links:       map[string]string{"acct-b@x.test": "0123456789ab"},
		wfcLinks:    map[string]string{},
		subjectToAc: map[string]string{"0123456789ab": "acct-b@x.test"},
		twoFactor:   map[string]bool{},
		publicCodes: map[string]string{},
	}
	db := store.NewMemory()
	sess := session.New("opak_session", "test-secret-0123456789abcdef-test!", false, time.Hour)
	srv := New(fc, fc, fc, fc, fc, db, sess, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv, fc, db
}

func do(t *testing.T, s *Server, method, target, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	var rdr *bytes.Reader
	switch v := body.(type) {
	case nil:
		rdr = bytes.NewReader(nil)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// doRaw sends the Authorization header exactly as given, so a test can send a
// wrong or absent credential rather than a well-formed bearer.
func doRaw(t *testing.T, s *Server, method, target, auth string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTokenLifecycle(t *testing.T) {
	s, fc, _ := newTestAPI(t)

	// Bad credentials → 401, no token.
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", rec.Code)
	}

	// Good credentials mint a bearer bound to a live core session.
	rec = do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)
	if !strings.HasPrefix(minted.Token, "opk_") {
		t.Fatalf("token shape: %q", minted.Token)
	}

	// /me works with the bearer; A has no console link, so no friend code.
	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked_platforms":[]`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"email":"a@x.test"`) {
		t.Errorf("me should carry the address (from whoami): %s", rec.Body.String())
	}

	// Core session revocation (ban/logout) kills the bearer.
	for sid := range fc.sessions {
		delete(fc.sessions, sid)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer must die with its core session, got %d", rec.Code)
	}
}

// TestFriendRequestByCode covers the W4 contract shape: add by friend code.
func TestFriendRequestByCode(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.accounts["b@x.test"] = "password-B-1"
	fc.verified["b@x.test"] = true

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	// Formatted code accepted.
	rec = do(t, s, http.MethodPost, "/api/v1/me/friends/requests", minted.Token,
		map[string]string{"friend_code": "SW-0123-4567-89ab"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("request by code: %d %s", rec.Code, rec.Body.String())
	}
	// Unknown code → 404.
	rec = do(t, s, http.MethodPost, "/api/v1/me/friends/requests", minted.Token,
		map[string]string{"friend_code": "999999999999"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", rec.Code)
	}
	// List friends.
	rec = do(t, s, http.MethodGet, "/api/v1/me/friends", minted.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("friends: %d", rec.Code)
	}
}

func TestFriendCodeFormatting(t *testing.T) {
	if got := friendCode("0123456789ab"); got != "SW-0123-4567-89ab" {
		t.Errorf("friendCode = %q", got)
	}
	if got := normalizeCode("SW-0123-4567-89ab"); got != "0123456789ab" {
		t.Errorf("normalizeCode = %q", got)
	}
}

// The two surfaces a companion app is built around: a friends list that says
// who can be played with right now, and an inbox that can be read and declined.
func TestPresenceAndInvitationsInbox(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.accounts["b@x.test"] = "password-B-1"
	fc.verified["b@x.test"] = true
	// The fake keys accounts as "acct-<email>", the way ResolveAccount mints them.
	fc.friends["acct-a@x.test"] = []string{"acct-b@x.test"}
	fc.presence = map[string]core.Presence{
		"acct-b@x.test": {Namespace: "switch", TitleID: "0100E65002BB8000", Since: time.Now()},
	}
	fc.invitations = []core.PendingInvitation{{
		ID: "inv-1", FromName: "Host", TitleID: "0100E65002BB8000",
		Namespace: "switch", ExpiresAt: time.Now().Add(2 * time.Minute),
	}}

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	// A friend with a live session is online and names the title.
	rec = do(t, s, http.MethodGet, "/api/v1/me/friends", minted.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("friends: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"online":true`) || !strings.Contains(rec.Body.String(), "0100E65002BB8000") {
		t.Fatalf("presence missing from the friends list: %s", rec.Body.String())
	}

	// An offline friend is present in the list and plainly not online, rather
	// than missing: the phone shows every friend, greyed.
	fc.presence = nil
	rec = do(t, s, http.MethodGet, "/api/v1/me/friends", minted.Token, nil)
	if !strings.Contains(rec.Body.String(), `"online":false`) {
		t.Fatalf("offline friend dropped from the list: %s", rec.Body.String())
	}

	// The inbox.
	rec = do(t, s, http.MethodGet, "/api/v1/me/invitations", minted.Token, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "inv-1") {
		t.Fatalf("inbox: %d %s", rec.Code, rec.Body.String())
	}
	// Nothing adapter-owned leaks to the phone.
	if strings.Contains(rec.Body.String(), "nonce") || strings.Contains(rec.Body.String(), "payload") {
		t.Errorf("inbox exposes adapter-owned fields: %s", rec.Body.String())
	}

	// Decline, and declining again is calm rather than an error.
	rec = do(t, s, http.MethodPost, "/api/v1/me/invitations/decline", minted.Token,
		map[string]string{"invitation_id": "inv-1"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"declined":true`) {
		t.Fatalf("decline: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodPost, "/api/v1/me/invitations/decline", minted.Token,
		map[string]string{"invitation_id": "inv-1"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"declined":false`) {
		t.Fatalf("second decline: %d %s", rec.Code, rec.Body.String())
	}
	// A missing id is a client error, not a silent no-op.
	rec = do(t, s, http.MethodPost, "/api/v1/me/invitations/decline", minted.Token, map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty decline: %d", rec.Code)
	}
}

// Registering an install for notifications, and the rule that keeps a shared
// phone honest: the token identifies the install, so signing in as someone else
// moves it rather than leaving two accounts notified on one device.
func TestPushRegistration(t *testing.T) {
	s, fc, db := newTestAPI(t)
	fc.accounts["b@x.test"] = "password-B-1"
	fc.verified["b@x.test"] = true

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var a struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &a)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/push", a.Token,
		map[string]string{"token": "device-1", "platform": "fcm"}); rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	toks, _ := db.PushTokensForAccount(context.Background(), "acct-a@x.test")
	if len(toks) != 1 || toks[0].Token != "device-1" {
		t.Fatalf("stored: %+v", toks)
	}

	// The same device, now signed in as B.
	rec = do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "b@x.test", Password: "password-B-1"})
	var b struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec := do(t, s, http.MethodPost, "/api/v1/me/push", b.Token,
		map[string]string{"token": "device-1"}); rec.Code != http.StatusOK {
		t.Fatalf("re-register: %d", rec.Code)
	}
	if toks, _ := db.PushTokensForAccount(context.Background(), "acct-a@x.test"); len(toks) != 0 {
		t.Errorf("the previous account still gets this phone's notifications: %+v", toks)
	}
	if toks, _ := db.PushTokensForAccount(context.Background(), "acct-b@x.test"); len(toks) != 1 {
		t.Errorf("the new account was not registered: %+v", toks)
	}

	// A token is required, and sign-out forgets the install.
	if rec := do(t, s, http.MethodPost, "/api/v1/me/push", b.Token, map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty token: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/push?token=device-1", b.Token, nil); rec.Code != http.StatusOK {
		t.Errorf("forget: %d %s", rec.Code, rec.Body.String())
	}
	if toks, _ := db.PushTokensForAccount(context.Background(), "acct-b@x.test"); len(toks) != 0 {
		t.Errorf("token survived sign-out: %+v", toks)
	}
}

// Editing a profile from the phone: each field optional, and the core's
// complaint passed through rather than flattened into "internal error".
func TestProfileEditing(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var a struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &a)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/profile", a.Token,
		map[string]string{"country": "IE"}); rec.Code != http.StatusOK {
		t.Fatalf("country only: %d %s", rec.Code, rec.Body.String())
	}
	if fc.profile[0] != "IE" {
		t.Errorf("core received %v", fc.profile)
	}
	// A screen that edits only one field must not erase the others: the core stores
	// country and birthday exactly as sent.
	_ = do(t, s, http.MethodPost, "/api/v1/me/profile", a.Token, map[string]string{"birthday": "1990-01-02"})
	if rec := do(t, s, http.MethodPost, "/api/v1/me/profile", a.Token,
		map[string]string{"display_name": "Ana"}); rec.Code != http.StatusOK {
		t.Fatalf("name only: %d %s", rec.Code, rec.Body.String())
	}
	if fc.profile != [3]string{"IE", "1990-01-02", "Ana"} {
		t.Errorf("a name-only edit erased the rest: core received %v", fc.profile)
	}
	// A bad date is the user's problem to fix, so the reason has to survive.
	rec = do(t, s, http.MethodPost, "/api/v1/me/profile", a.Token,
		map[string]string{"birthday": "not-a-date"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad date: %d %s", rec.Code, rec.Body.String())
	}
}

// Avatars are unavailable rather than broken when the media service is absent,
// which is how the test server runs.
func TestAvatarChoicesAreListedAndSettable(t *testing.T) {
	s, _, _ := newTestAPI(t)
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var a struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &a)

	rec = do(t, s, http.MethodGet, "/api/v1/avatars", a.Token, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "aurora") {
		t.Fatalf("avatars: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/avatar", a.Token,
		map[string]string{"preset": "aurora"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no media service should be 503, got %d", rec.Code)
	}
}

// The status endpoint is public: an account is exactly what a person checking
// status may be unable to use.
func TestStatusNeedsNoToken(t *testing.T) {
	s, fc, db := newTestAPI(t)
	if err := db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Outbound", TitleID: "0100ED9024EB8000"},
	}); err != nil {
		t.Fatal(err)
	}
	fc.players = []core.TitlePlayers{
		{TitleID: "0100ED9024EB8000", Namespace: "switch", Players: 2},
		{TitleID: "010025901a268000", Namespace: "switch", Players: 1},
	}
	fc.networkPlayers = map[string]int{"switch": 3}
	fc.totalPlayers = 3

	rec := do(t, s, http.MethodGet, "/api/v1/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"players_online":3`, "0100ED9024EB8000", `"networks":[{"namespace":"switch","players":3}]`} {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing from %s", want, body)
		}
	}
	if strings.Contains(strings.ToUpper(body), "010025901A268000") {
		t.Error("unknown titles must not appear in the status API")
	}
}

// The internal surface carries a bearer secret and is not published, so the
// two ways it can go wrong are accepting an unauthenticated call and existing
// at all on a deployment that never configured it.
func TestInternalAvatarRefusesWithoutTheKey(t *testing.T) {
	s, _, _ := newTestAPI(t)
	img := map[string]string{
		"namespace": "switch", "subject": "0123456789ab",
		"content_base64": base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xD9}),
	}

	// No key configured: refuse rather than accept from anybody.
	if rec := doRaw(t, s, http.MethodPost, "/internal/avatar", "", img); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured = %d, want 503", rec.Code)
	}

	t.Setenv("WEBSITE_INTERNAL_KEY", "shared-secret-value")
	if rec := doRaw(t, s, http.MethodPost, "/internal/avatar", "", img); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credential = %d, want 401", rec.Code)
	}
	if rec := doRaw(t, s, http.MethodPost, "/internal/avatar", "Bearer wrong", img); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong credential = %d, want 401", rec.Code)
	}
	// With the key it gets as far as needing a media service, which the test
	// server has none of — past authentication is what this test is for.
	rec := doRaw(t, s, http.MethodPost, "/internal/avatar", "Bearer shared-secret-value", img)
	if rec.Code == http.StatusUnauthorized {
		t.Error("the right credential was refused")
	}
}

// A core that cannot answer must not cost anyone their sign-in. On 2026-09-08
// a phone was signed out permanently by one failed call: the middleware
// revoked the bearer for any error, including "the core is restarting".
func TestCoreOutageDoesNotRevokeTheBearer(t *testing.T) {
	s, fc, _ := newTestAPI(t)

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	fc.down = errors.New("dial tcp: connection refused")
	if rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("core down should be 503, got %d %s", rec.Code, rec.Body.String())
	}

	// The outage passes; the same bearer still works.
	fc.down = nil
	if rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil); rec.Code != http.StatusOK {
		t.Fatalf("bearer must survive an outage, got %d %s", rec.Code, rec.Body.String())
	}
}

// The sessions list is JSON for a phone, not a Go struct dump: a client
// reading "id" must not get an empty string because the field is called "ID".
func TestSessionsAreSnakeCase(t *testing.T) {
	s, _, _ := newTestAPI(t)

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	rec = do(t, s, http.MethodGet, "/api/v1/me/sessions", minted.Token, nil)
	var got struct {
		Sessions []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID == "" || got.Sessions[0].CreatedAt == "" {
		t.Fatalf("session fields did not survive the wire: %s", rec.Body.String())
	}
}

// The console's picture reaches the site through /internal/avatar, and the
// site's caches key off the media row, not the file. Writing bytes without
// stamping the row leaves every browser showing the old avatar against a
// validator that never moves -- observed on 2026-09-08, file changed at
// 18:49, row still the day before.
func TestInternalAvatarStampsTheMediaRow(t *testing.T) {
	s, _, db := newTestAPI(t)
	dir := t.TempDir()
	st, err := media.NewFSStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.New(st)
	t.Setenv("WEBSITE_INTERNAL_KEY", "shared-secret-value")

	var png bytes.Buffer
	if err := pngEncode(&png); err != nil {
		t.Fatal(err)
	}
	img := map[string]string{
		"namespace": "switch", "subject": "0123456789ab",
		"content_base64": base64.StdEncoding.EncodeToString(png.Bytes()),
	}
	before := time.Now().Add(-time.Hour).UTC()
	if err := db.UpsertMedia(context.Background(), store.MediaRecord{
		AccountID: "acct-b@x.test", Kind: "avatar", UpdatedAt: before,
	}); err != nil {
		t.Fatal(err)
	}

	if rec := doRaw(t, s, http.MethodPost, "/internal/avatar", "Bearer shared-secret-value", img); rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetMedia(context.Background(), "acct-b@x.test", "avatar")
	if err != nil {
		t.Fatalf("media row: %v", err)
	}
	if !got.UpdatedAt.After(before) {
		t.Fatalf("row was not stamped: still %s", got.UpdatedAt)
	}
}

func pngEncode(w io.Writer) error {
	m := image.NewRGBA(image.Rect(0, 0, 8, 8))
	return png.Encode(w, m)
}

// An account that exists has a face. Linking a console to an account with no
// picture used to fall back to the adapter's generic default, and the site
// showed a blank initial; the preset an account's id picks is served until
// somebody chooses another, so "no avatar" is a state only the picker knows.
func TestEveryAccountHasAnAvatar(t *testing.T) {
	s, _, _ := newTestAPI(t)
	st, err := media.NewFSStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.New(st) // empty store: nobody has uploaded anything

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	var me struct {
		AvatarURL *string `json:"avatar_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.AvatarURL == nil || *me.AvatarURL == "" {
		t.Fatal("an account with nothing uploaded was given no picture at all")
	}
}

// A Wii/DS console linked by friend code (WD-0) is part of the person: the same
// linked_platforms list the app reads carries it beside the Switch link.
func TestMeListsWiiDSLink(t *testing.T) {
	s, fc, _ := newTestAPI(t)

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)

	// No console of either kind: the empty list, as before.
	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked_platforms":[]`) {
		t.Fatalf("empty me: %d %s", rec.Code, rec.Body.String())
	}

	// Link a Wii by friend code; it appears beside nothing else.
	fc.wfcLinks["acct-a@x.test"] = "123456789" // nn-wfc console-level user id
	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"namespace":"wfc"`) {
		t.Fatalf("wfc link missing from %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"friend_code"`) {
		t.Fatal("a per-title friend code must not be projected as the account's friend code")
	}

	// Both families at once: two rows, switch first.
	fc.links["acct-a@x.test"] = "3000000000000003"
	rec = do(t, s, http.MethodGet, "/api/v1/me", minted.Token, nil)
	body := rec.Body.String()
	if !strings.Contains(body, `"namespace":"switch"`) || !strings.Contains(body, `"namespace":"wfc"`) {
		t.Fatalf("both links expected, got %s", body)
	}
	if strings.Index(body, `"namespace":"switch"`) > strings.Index(body, `"namespace":"wfc"`) {
		t.Fatalf("switch link should come first, got %s", body)
	}
}
