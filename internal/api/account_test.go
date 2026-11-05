package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/wfc/wfctest"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/store"
)

func signIn(t *testing.T, s *Server) string {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var m struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m.Token
}

func TestRegisterAndForgotArePublic(t *testing.T) {
	s, _, db := newTestAPI(t)
	if rec := do(t, s, http.MethodPost, "/api/v1/register", "", map[string]string{"email": "n@x.test", "password": "password-N-1"}); rec.Code != http.StatusAccepted {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/password/forgot", "", map[string]string{"email": "n@x.test"}); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d", rec.Code)
	}
	_ = db.SetSiteConfig(context.Background(), store.SiteConfig{})
	if rec := do(t, s, http.MethodPost, "/api/v1/register", "", map[string]string{"email": "m@x.test"}); rec.Code != http.StatusForbidden {
		t.Fatalf("closed registrations: %d", rec.Code)
	}
}

func TestChangePassword(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := signIn(t, s)
	if rec := do(t, s, http.MethodPost, "/api/v1/me/password", tok, map[string]string{"new_password": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing current: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/password", tok, map[string]string{"current_password": "password-A-1", "new_password": "password-A-2"}); rec.Code != http.StatusOK {
		t.Fatalf("change: %d %s", rec.Code, rec.Body.String())
	}
}

// Link a Wii by the code it showed, then unlink it by namespace; the Switch link another
// account holds is out of reach.
func TestLinkAndUnlinkWiiDS(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	f, cli := wfctest.New(t)
	f.Codes["QXZP7K2M"] = wfctest.Code{Number: 24829, UserID: 42, Unit: "ds", Game: "AMCE",
		Proof: func(ip, name string) bool { return name == "TOBAGIN" }}
	s.WFC = cli
	tok := signIn(t, s)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"code": "24829"}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "wfc_code_unknown") {
		t.Fatalf("number without proof: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"code": "24829", "name": "TOBAGIN"})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"unit":"ds"`) {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	if f.Linked[42].Account != "acct-a@x.test" {
		t.Fatalf("approve went out as %+v", f.Linked)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/links/wfc", tok, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"unit":"ds"`) || strings.Contains(rec.Body.String(), "42") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/wfc", tok, nil); rec.Code != http.StatusOK || len(f.Unlinked) != 1 {
		t.Fatalf("unlink: %d %v", rec.Code, f.Unlinked)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/wfc", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unlink twice: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/switch", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unlinking a console A never had: %d", rec.Code)
	}
	if _, ok := fc.links["acct-b@x.test"]; !ok {
		t.Fatal("B's switch link was touched")
	}
}

func TestAvatarUpload(t *testing.T) {
	s, _, db := newTestAPI(t)
	st, err := media.NewMemStorage(), error(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.New(st)
	tok := signIn(t, s)

	send := func(body []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("avatar", "me.png")
		_, _ = fw.Write(body)
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+tok)
		mux := http.NewServeMux()
		s.RegisterRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := send([]byte("not an image")); rec.Code != http.StatusBadRequest {
		t.Fatalf("junk: %d", rec.Code)
	}
	var img bytes.Buffer
	if err := pngEncode(&img); err != nil {
		t.Fatal(err)
	}
	if rec := send(img.Bytes()); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := db.GetMedia(context.Background(), "acct-a@x.test", "avatar"); err != nil {
		t.Fatalf("media row not stamped: %v", err)
	}
}

// Deleting the account needs the password again, and afterwards the bearer that asked is
// dead along with every other one.
func TestDeleteAccount(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	tok := signIn(t, s)
	other := signIn(t, s)
	rec := do(t, s, http.MethodDelete, "/api/v1/me", tok, map[string]string{"password": "wrong"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body.String())
	}
	if len(fc.deleted) != 0 {
		t.Fatal("a wrong password started a deletion")
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me", tok, map[string]string{"password": "password-A-1"}); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(fc.deleted) != 1 || fc.deleted[0] != "acct-a@x.test" {
		t.Fatalf("deletion requested for %v", fc.deleted)
	}
	for _, b := range []string{tok, other} {
		if rec := do(t, s, http.MethodGet, "/api/v1/me", b, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("a bearer outlived the account: %d", rec.Code)
		}
	}
}

// The app's old field name still carries the code; several consoles need a link_id to unlink
// one; attempts are rate limited per account.
func TestLinkWiiDSByPairingCode(t *testing.T) {
	s, _, _ := newTestAPI(t)
	f, cli := wfctest.New(t)
	f.Codes["QXZP7K2M"] = wfctest.Code{Number: 24829, UserID: 77, Unit: "wii"}
	f.Codes["ABCD2345"] = wfctest.Code{Number: 25222, UserID: 78, Unit: "ds"}
	s.WFC = cli
	s.PairLimit = ratelimit.New(3, time.Minute)
	tok := signIn(t, s)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"friend_code": "qxzp-7k2m"}); rec.Code != http.StatusCreated {
		t.Fatalf("code in the friend_code field: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"code": "ABCD-2345"}); rec.Code != http.StatusCreated {
		t.Fatalf("second console: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/wfc", tok, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "wfc_choose_console") {
		t.Fatalf("ambiguous unlink: %d %s", rec.Code, rec.Body.String())
	}
	link := f.Linked[78].LinkID
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/wfc?erase=1&link_id="+link, tok, nil); rec.Code != http.StatusOK || len(f.Erased) != 1 || f.Erased[0] != 78 {
		t.Fatalf("unlink one: %d erased %v", rec.Code, f.Erased)
	}
	do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"code": "ZZZZ-ZZZZ"})
	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"code": "ZZZZ-ZZZZ"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", rec.Code)
	}
}
