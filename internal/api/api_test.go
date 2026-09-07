package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	accounts    map[string]string
	verified    map[string]bool
	sessions    map[string]string
	friends     map[string][]string
	links       map[string]string
	subjectToAc map[string]string
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
func (f *fakeCore) ForgotPassword(_ context.Context, _ string) error       { return nil }
func (f *fakeCore) ResetPassword(_ context.Context, _, _ string) error     { return nil }
func (f *fakeCore) Healthy(_ context.Context) error                        { return nil }
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
	srv := New(fc, fc, fc, fc, db, sess, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
