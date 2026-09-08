package pages

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"testing"

	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// login posts the login form and returns the response with the session
// cookie attached for follow-up requests.
func login(t *testing.T, s *Server, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {email}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleLoginSubmit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login %s: status %d, want %d; body=%s", email, rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	return rec
}

// authedRequest performs a request with the session cookie from a login.
func authedRequest(s *Server, loginRec *httptest.ResponseRecorder, method, target string, body *url.Values) *httptest.ResponseRecorder {
	var cookie string
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "opak_session" {
			cookie = c.Value
		}
	}
	if body != nil {
		body.Set("csrf", s.Sessions.CSRFToken(cookie))
	}
	var rdr *strings.Reader
	if body != nil {
		rdr = strings.NewReader(body.Encode())
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "opak_session", Value: cookie})
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestW1Journey covers the W1 acceptance flow end to end against the fake
// core: register, verify, login, session binding, logout.
func TestW1Journey(t *testing.T) {
	s, fc, _ := newTestServer(t)
	mux := s.Handler()

	// Landing renders.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "OpenPak") {
		t.Fatalf("landing: %d", rec.Code)
	}

	// Status renders (the only page no other journey touches).
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Account core") {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}

	// Register (masked success response).
	form := url.Values{"email": {"a@x.test"}, "password": {"password-A-1"}, "display_name": {"Alice"}}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/register", form))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "verification email") {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	// Login before verification fails.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {"a@x.test"}, "password": {"password-A-1"}}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pre-verify login must fail, got %d", rec.Code)
	}

	// Verify with the token the core would have emailed.
	fc.verified["a@x.test"] = true // simulate the emailed link being used

	// Login sets the website cookie and redirects to /account.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {"a@x.test"}, "password": {"password-A-1"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
		t.Fatalf("login: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Value != "sess-a@x.test" {
		t.Fatalf("website cookie must wrap the core session id, got %q", cookie.Value)
	}

	// The console creates the link in the core (adapter-owned); the website
	// reads it through the Links contract and shows the friend code.
	fc.links["acct-a@x.test"] = "0123456789ab"
	fc.subjectToAc["0123456789ab"] = "acct-a@x.test"

	// Account page shows the friend code from the switch link.
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SW-0123-4567-89ab") {
		t.Fatalf("account: %d (friend code missing?)", rec.Code)
	}

	// Journey 1 ends by revoking the console-created link.
	req = post("/account/link/revoke", url.Values{"link_id": {"link-acct-a@x.test"}})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.PostForm = url.Values{"csrf": {s.Sessions.CSRFToken("sess-a@x.test")}, "link_id": {"link-acct-a@x.test"}}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || len(fc.links) != 0 {
		t.Fatalf("unlink: %d links=%v", rec.Code, fc.links)
	}

	// Recovery flow renders and resets.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/forgot-password", url.Values{"email": {"a@x.test"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/reset-password", url.Values{"token": {"good"}, "password": {"newpassword-9"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", rec.Code)
	}
	if fc.accounts["a@x.test"] != "newpassword-9" {
		t.Fatal("password not reset through the core contract")
	}

	// Logout revokes the core session.
	req = httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(url.Values{
		"csrf": {s.Sessions.CSRFToken("sess-a@x.test")},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if _, ok := fc.sessions["sess-a@x.test"]; ok {
		t.Fatal("core session must be revoked on logout")
	}
}

// TestW2FriendJourney covers request → accept → visible on both sides →
// remove, plus block/unblock (WEB-04), all through the Social contract.
func TestW2FriendJourney(t *testing.T) {
	s, fc, _ := newTestServer(t)
	_, _ = seedTwoAccounts(fc)

	// Both log in.
	recA := login(t, s, "a@x.test", "password-A-1")
	recB := login(t, s, "b@x.test", "password-B-1")

	// B requests A by friend code.
	rec := authedRequest(s, recB, http.MethodPost, "/account/friends/request",
		&url.Values{"friend_code": {"SW-0123-4567-89ab"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("friend request: %d %s", rec.Code, rec.Body.String())
	}

	// A sees the incoming request and accepts.
	rec = authedRequest(s, recA, http.MethodGet, "/account", nil)
	if !strings.Contains(rec.Body.String(), "acct-b@x.test") {
		t.Fatal("incoming request not visible to addressee")
	}
	rec = authedRequest(s, recA, http.MethodPost, "/account/friends/accept",
		&url.Values{"account_id": {"acct-b@x.test"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d", rec.Code)
	}

	// Both sides now list each other.
	fa, _ := s.Social.ListFriends(context.Background(), "acct-a@x.test")
	fb, _ := s.Social.ListFriends(context.Background(), "acct-b@x.test")
	if len(fa) != 1 || len(fb) != 1 {
		t.Fatalf("friendship not visible on both sides: %v %v", fa, fb)
	}

	// Block then unblock.
	rec = authedRequest(s, recA, http.MethodPost, "/account/friends/block",
		&url.Values{"account_id": {"acct-b@x.test"}})
	if rec.Code != http.StatusSeeOther || len(fc.blocks) != 1 {
		t.Fatalf("block failed: %d", rec.Code)
	}
	rec = authedRequest(s, recA, http.MethodPost, "/account/friends/unblock",
		&url.Values{"account_id": {"acct-b@x.test"}})
	if rec.Code != http.StatusSeeOther || len(fc.blocks) != 0 {
		t.Fatalf("unblock failed: %d", rec.Code)
	}

	// CSRF is enforced: no token → 403.
	cookie := cookieFrom(t, recA)
	req := post("/account/friends/block", url.Values{"account_id": {"acct-b@x.test"}})
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be rejected, got %d", rec.Code)
	}

	// Remove ends the friendship.
	rec = authedRequest(s, recA, http.MethodPost, "/account/friends/remove",
		&url.Values{"account_id": {"acct-b@x.test"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	fa, _ = s.Social.ListFriends(context.Background(), "acct-a@x.test")
	if len(fa) != 0 {
		t.Fatal("friendship not removed")
	}
}

// TestUnknownFriendCode checks the friendly redirect for a bad code.
func TestUnknownFriendCode(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	recB := login(t, s, "b@x.test", "password-B-1")
	rec := authedRequest(s, recB, http.MethodPost, "/account/friends/request",
		&url.Values{"friend_code": {"999999999999"}})
	if !strings.Contains(rec.Header().Get("Location"), "m=funknown") {
		t.Fatalf("unknown code must redirect with funknown, got %s", rec.Header().Get("Location"))
	}
}

// The invitations view is live now that the core can be read without consuming
// anything; it used to render a notice explaining the missing contract.
func TestInvitationsAreListed(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	fc.invitations = []core.PendingInvitation{{
		ID: "inv-1", FromName: "Host", TitleID: "0100E65002BB8000",
		Namespace: "switch", ExpiresAt: time.Now().Add(2 * time.Minute),
	}}
	recA := login(t, s, "a@x.test", "password-A-1")
	rec := authedRequest(s, recA, http.MethodGet, "/account", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("account page: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "0100E65002BB8000") || !strings.Contains(rec.Body.String(), "Host") {
		t.Error("the pending invitation is not on the page")
	}
}

// TestDeadCoreSessionEndsWebSession: when the core session is revoked
// (ban, logout elsewhere), the website session dies too.
func TestDeadCoreSessionEndsWebSession(t *testing.T) {
	s, fc, db := newTestServer(t)
	seedTwoAccounts(fc)
	recA := login(t, s, "a@x.test", "password-A-1")

	delete(fc.sessions, "sess-a@x.test") // simulate core-side revocation
	cookie := cookieFrom(t, recA)
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("dead core session must redirect to login, got %d", rec.Code)
	}
	if _, err := db.GetWebSession(context.Background(), "sess-a@x.test"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("local session row must be cleaned, got %v", err)
	}
}

func cookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "opak_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func post(target string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// TestAvatarUploadMultipartCSRF: multipart forms must pass the CSRF guard.
func TestAvatarUploadMultipartCSRF(t *testing.T) {
	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	recA := login(t, s, "a@x.test", "password-A-1")
	cookie := cookieFrom(t, recA)

	// Missing token → 403 even on multipart.
	body := &strings.Builder{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("avatar", "x")
	_, _ = mw.CreateFormFile("avatar", "a.png")
	_ = mw.WriteField("csrf", "")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/account/avatar", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("multipart without CSRF must 403, got %d", rec.Code)
	}

	// Valid token → avatar stored.
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	_ = png.Encode(&buf, img)
	body = &strings.Builder{}
	mw = multipart.NewWriter(body)
	_ = mw.WriteField("csrf", s.Sessions.CSRFToken(cookie.Value))
	part, _ := mw.CreateFormFile("avatar", "a.png")
	_, _ = part.Write(buf.Bytes())
	_ = mw.WriteField("done", "1")
	mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/account/avatar", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("multipart upload: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := s.Media.Avatar(context.Background(), "acct-a@x.test", media.SizeSmall); err != nil {
		t.Fatalf("avatar not stored: %v", err)
	}
}

// Changing a password from the account page: a mismatched confirmation must not reach the
// core, and the wrong current password must not rotate anything.
func TestChangePasswordJourney(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["a@x.test"] = "password-A-1"
	fc.verified["a@x.test"] = true // login refuses unverified accounts
	rec := login(t, s, "a@x.test", "password-A-1")

	attempt := func(current, next, confirm string) string {
		body := url.Values{"current_password": {current}, "new_password": {next}, "confirm_password": {confirm}}
		return authedRequest(s, rec, http.MethodPost, "/account/password", &body).Header().Get("Location")
	}

	if got := attempt("password-A-1", "password-B-2", "different"); !strings.Contains(got, "password-mismatch") {
		t.Errorf("mismatched confirmation redirected to %q", got)
	}
	if fc.accounts["a@x.test"] != "password-A-1" {
		t.Error("password rotated despite a mismatched confirmation")
	}
	if got := attempt("wrong-one", "password-B-2", "password-B-2"); !strings.Contains(got, "password-failed") {
		t.Errorf("wrong current password redirected to %q", got)
	}
	if fc.accounts["a@x.test"] != "password-A-1" {
		t.Error("password rotated with the wrong current password")
	}
	if got := attempt("password-A-1", "password-B-2", "password-B-2"); !strings.Contains(got, "password-changed") {
		t.Errorf("valid change redirected to %q", got)
	}
	if fc.accounts["a@x.test"] != "password-B-2" {
		t.Error("password was not rotated")
	}
}

// An account created before registration asked for a country and a date of birth holds
// neither, and the console's user object carries whatever the account holds. The account page
// is the only way to fill them in, so the round trip has to work — and blank has to stay
// allowed, because nothing downstream substitutes a value for what is missing.
func TestProfileCountryAndBirthdayCanBeSaved(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["a@x.test"] = "password-A-1"
	fc.verified["a@x.test"] = true
	rec := login(t, s, "a@x.test", "password-A-1")

	save := func(country, birthday string) string {
		body := url.Values{"country": {country}, "birthday": {birthday}}
		return authedRequest(s, rec, http.MethodPost, "/account/profile", &body).Header().Get("Location")
	}

	if got := save("IE", "1990-01-01"); !strings.Contains(got, "profile-saved") {
		t.Errorf("saving a profile redirected to %q", got)
	}
	if fc.profile != [2]string{"IE", "1990-01-01"} {
		t.Errorf("core received %v", fc.profile)
	}
	if got := save("", ""); !strings.Contains(got, "profile-saved") {
		t.Errorf("clearing the profile redirected to %q", got)
	}
	if fc.profile != [2]string{"", ""} {
		t.Errorf("blank values were not passed through: %v", fc.profile)
	}
	if got := save("IE", "not-a-date"); !strings.Contains(got, "profile-failed") {
		t.Errorf("a bad date redirected to %q", got)
	}
}
