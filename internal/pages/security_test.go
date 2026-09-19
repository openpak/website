package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A 2FA account's password gets a second form, not a session; the code (or a
// recovery code) from that form signs in, and a wrong one keeps the person out.
func TestLoginWithTwoFactor(t *testing.T) {
	s, fc, db := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	fc.twoFactor[emailA] = true
	mux := s.Handler()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {emailA}, "password": {"password-A-1"}}))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `action="/login/2fa"`) || !strings.Contains(body, `value="ch-`+emailA+`"`) {
		t.Fatalf("password step: %d %s", rec.Code, body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a session cookie was set before the second factor")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login/2fa", url.Values{"challenge": {"ch-" + emailA}, "code": {"000000"}}))
	if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), `value="ch-`+emailA+`"`) {
		t.Fatalf("wrong code: %d", rec.Code)
	}

	for _, code := range []string{"123 456", "ABCDE-FGHIJ"} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, post("/login/2fa", url.Values{"challenge": {"ch-" + emailA}, "code": {code}}))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
			t.Fatalf("code %q: %d %s", code, rec.Code, rec.Body.String())
		}
		ws, err := db.GetWebSession(t.Context(), "sess-"+emailA)
		if err != nil || ws.AccountID != "acct-"+emailA || ws.Email != emailA {
			t.Fatalf("web session from whoami: %+v %v", ws, err)
		}
	}
}

// Deleting a 2FA account needs the code as well as the password (core
// docs/two-factor.md: the website sets second_factor_supported and then owes
// VerifySecondFactor).
func TestDeleteAccountStepUpWithTwoFactor(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	fc.twoFactor[emailA] = true

	if body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(); !strings.Contains(body, `id="delete-code"`) {
		t.Fatal("the delete form does not ask a 2FA account for its code")
	}
	for _, code := range []string{"", "999999"} {
		rec := authedRequest(s, a, http.MethodPost, "/account/delete", &url.Values{"password": {"password-A-1"}, "confirm": {"yes"}, "code": {code}})
		if rec.Code != http.StatusForbidden || len(fc.deleted) != 0 {
			t.Fatalf("code %q: %d, deleted %v", code, rec.Code, fc.deleted)
		}
	}
	rec := authedRequest(s, a, http.MethodPost, "/account/delete", &url.Values{"password": {"password-A-1"}, "confirm": {"yes"}, "code": {fakeTOTP}})
	if rec.Code != http.StatusOK || len(fc.deleted) != 1 {
		t.Fatalf("with the code: %d, deleted %v", rec.Code, fc.deleted)
	}
}

// Setup shows the key and the console warning, enabling shows the ten
// recovery codes once, and disabling needs the password and a code.
func TestTwoFactorEnrolment(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")

	body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	if !strings.Contains(body, `action="/account/2fa/setup"`) || !strings.Contains(body, "Switch, Wii U and 3DS") {
		t.Fatal("the security section lacks the setup form or the console warning")
	}
	rec := authedRequest(s, a, http.MethodPost, "/account/2fa/setup", &url.Values{"password": {"wrong"}})
	if !strings.Contains(rec.Header().Get("Location"), "2fa-wrongpassword") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/2fa/setup", &url.Values{"password": {"password-A-1"}})
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "JBSWY3DPEHPK3PXP") || !strings.Contains(body, "otpauth://totp/") ||
		!strings.Contains(body, "Switch, Wii U and 3DS") {
		t.Fatalf("setup page: %d %s", rec.Code, body)
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/2fa/enable", &url.Values{"code": {"000000"}, "secret": {"JBSWY3DPEHPK3PXP"}})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "JBSWY3DPEHPK3PXP") {
		t.Fatalf("wrong first code: %d", rec.Code)
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/2fa/enable", &url.Values{"code": {fakeTOTP}})
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), "rcode-") != 10 || !fc.twoFactor[emailA] {
		t.Fatalf("enable: %d", rec.Code)
	}
	if body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(); !strings.Contains(body, `action="/account/2fa/disable"`) {
		t.Fatal("an enabled account is not offered the disable form")
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/2fa/disable", &url.Values{"password": {"password-A-1"}, "code": {"111111"}})
	if !strings.Contains(rec.Header().Get("Location"), "2fa-wrongcode") || !fc.twoFactor[emailA] {
		t.Fatalf("disable with a wrong code: %s", rec.Header().Get("Location"))
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/2fa/disable", &url.Values{"password": {"password-A-1"}, "code": {fakeRecovery}})
	if !strings.Contains(rec.Header().Get("Location"), "2fa-disabled") || fc.twoFactor[emailA] {
		t.Fatalf("disable: %s", rec.Header().Get("Location"))
	}
	// Every 2FA form is CSRF-checked.
	for _, path := range []string{"/account/2fa/setup", "/account/2fa/enable", "/account/2fa/disable", "/account/email", "/account/verify/resend"} {
		req := post(path, url.Values{"password": {"password-A-1"}})
		req.AddCookie(&http.Cookie{Name: "opak_session", Value: "sess-" + emailA})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s without CSRF: %d", path, rec.Code)
		}
	}
}

// The email change mails a link; the link lands on a page with a button (a
// scanner fetching it changes nothing), and the button applies the change.
func TestEmailChangeAndConfirm(t *testing.T) {
	s, fc, db := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")

	rec := authedRequest(s, a, http.MethodPost, "/account/email", &url.Values{"new_email": {"new@x.test"}, "password": {"nope"}})
	if !strings.Contains(rec.Header().Get("Location"), "email-wrongpassword") {
		t.Fatalf("wrong password: %s", rec.Header().Get("Location"))
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/email", &url.Values{"new_email": {"New@X.test"}, "password": {"password-A-1"}})
	if !strings.Contains(rec.Header().Get("Location"), "email-sent") || fc.emailChange[emailA] != "new@x.test" {
		t.Fatalf("change: %s %v", rec.Header().Get("Location"), fc.emailChange)
	}

	rec = authedRequest(s, a, http.MethodGet, "/email/confirm?token="+fakeEmailToken, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `value="`+fakeEmailToken+`"`) || fc.emailChange[emailA] == "" {
		t.Fatalf("confirm page: %d; a GET must change nothing", rec.Code)
	}
	rec = authedRequest(s, a, http.MethodPost, "/email/confirm", &url.Values{"token": {"forged"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad token: %d", rec.Code)
	}
	rec = authedRequest(s, a, http.MethodPost, "/email/confirm", &url.Values{"token": {fakeEmailToken}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "has been changed") {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	if ws, _ := db.GetWebSession(t.Context(), "sess-"+emailA); ws.Email != "new@x.test" {
		t.Fatalf("the cached address was not refreshed: %q", ws.Email)
	}
}

// An unconfirmed address gets a resend button; a confirmed one does not.
func TestVerificationResend(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	if strings.Contains(authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(), `action="/account/verify/resend"`) {
		t.Fatal("a confirmed address is offered a resend")
	}
	fc.verified[emailA] = false // e.g. mid-way through an address change
	if !strings.Contains(authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(), `action="/account/verify/resend"`) {
		t.Fatal("an unconfirmed address is not offered a resend")
	}
	rec := authedRequest(s, a, http.MethodPost, "/account/verify/resend", &url.Values{})
	if !strings.Contains(rec.Header().Get("Location"), "verify-sent") || fc.resent != 1 {
		t.Fatalf("resend: %s, %d", rec.Header().Get("Location"), fc.resent)
	}
}

// The account page lists who the viewer blocked, each with an unblock button.
func TestBlockedListOnAccountPage(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, emailB := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	if !strings.Contains(authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(), "not blocked anyone") {
		t.Fatal("empty block list not said")
	}
	authedRequest(s, a, http.MethodPost, "/account/friends/block", &url.Values{"account_id": {"acct-" + emailB}})
	body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	if !strings.Contains(body, "name-acct-"+emailB) || !strings.Contains(body, `action="/account/friends/unblock"`) {
		t.Fatal("the blocked account is not listed with an unblock button")
	}
	authedRequest(s, a, http.MethodPost, "/account/friends/unblock", &url.Values{"account_id": {"acct-" + emailB}})
	if strings.Contains(authedRequest(s, a, http.MethodGet, "/account", nil).Body.String(), "name-acct-"+emailB) {
		t.Fatal("still listed after unblocking")
	}
}

// A friend code the adapter published on the link resolves through the core.
func TestFriendRequestByPublishedCode(t *testing.T) {
	s, fc, _ := newTestServer(t)
	emailA, emailB := seedTwoAccounts(fc)
	fc.publicCodes["SW-1111-2222-3333"] = "acct-" + emailB
	a := login(t, s, emailA, "password-A-1")
	rec := authedRequest(s, a, http.MethodPost, "/account/friends/request", &url.Values{"friend_code": {"SW-1111-2222-3333"}})
	if !strings.Contains(rec.Header().Get("Location"), "frequest") {
		t.Fatalf("request: %s", rec.Header().Get("Location"))
	}
}
