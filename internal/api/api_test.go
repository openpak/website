package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// fakeCore mirrors contract semantics the API relies on.
type fakeCore struct {
	accounts     map[string]string
	verified     map[string]bool
	sessions     map[string]string
	friends      map[string][]string
	links        map[string]string
	subjectToAc  map[string]string
	invitations  []core.PendingInvitation
	presence     map[string]core.Presence
	players      []core.TitlePlayers
	totalPlayers int
	profile      [3]string // country, birthday, display name as last saved
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		accounts:    map[string]string{},
		verified:    map[string]bool{},
		sessions:    map[string]string{},
		friends:     map[string][]string{},
		links:       map[string]string{},
		subjectToAc: map[string]string{},
	}
}

func (f *fakeCore) Register(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeCore) VerifyEmail(_ context.Context, _ string) error    { return nil }
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
func (f *fakeCore) PlayerCounts(_ context.Context) ([]core.TitlePlayers, int, error) {
	return f.players, f.totalPlayers, nil
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
func (f *fakeCore) RegisterWithProfile(ctx context.Context, email, password, displayName, _, _ string) error {
	return f.Register(ctx, email, password, displayName)
}
func (f *fakeCore) ForgotPassword(_ context.Context, _ string) error   { return nil }
func (f *fakeCore) ResetPassword(_ context.Context, _, _ string) error { return nil }
func (f *fakeCore) Healthy(_ context.Context) error                    { return nil }
func (f *fakeCore) ResolveAccount(_ context.Context, email, password string) (core.Account, error) {
	if pw, ok := f.accounts[email]; !ok || pw != password {
		return core.Account{}, core.ErrInvalidCredentials
	}
	return core.Account{ID: "acct-" + email, Email: email}, nil
}
func (f *fakeCore) GetAccount(_ context.Context, id string) (core.Account, error) {
	return core.Account{ID: id}, nil
}
func (f *fakeCore) RequestFriend(_ context.Context, requester, addressee string) error {
	f.friends[requester] = append(f.friends[requester], addressee)
	return nil
}
func (f *fakeCore) AcceptFriend(_ context.Context, _, _ string) error { return nil }
func (f *fakeCore) RemoveFriend(_ context.Context, _, _ string) error { return nil }
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
func (f *fakeCore) Block(_ context.Context, _, _ string) error   { return nil }
func (f *fakeCore) Unblock(_ context.Context, _, _ string) error { return nil }
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
func (f *fakeCore) Unlink(_ context.Context, _, _ string) error { return nil }

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
		subjectToAc: map[string]string{"0123456789ab": "acct-b@x.test"},
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
	if rec := do(t, s, http.MethodPost, "/api/v1/me/avatar", a.Token, map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty preset: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/avatar", a.Token,
		map[string]string{"preset": "aurora"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no media service should be 503, got %d", rec.Code)
	}
}
