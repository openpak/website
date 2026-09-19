package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/wfc"
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

// Link a Wii by friend code, then unlink it by namespace; the Switch link another
// account holds is out of reach.
func TestLinkAndUnlinkWiiDS(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	nwfc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fc") != "1234-5678-9012" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"user_id":42}`))
	}))
	defer nwfc.Close()
	s.WFC = wfc.New(nwfc.URL, "")
	tok := signIn(t, s)

	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"friend_code": "0000-0000-0000"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/links/wfc", tok, map[string]string{"friend_code": "1234-5678-9012"}); rec.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	if fc.wfcLinks["acct-a@x.test"] != "42" {
		t.Fatalf("core holds %v", fc.wfcLinks)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/links/wfc", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if _, ok := fc.wfcLinks["acct-a@x.test"]; ok {
		t.Fatal("still linked")
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
	st, err := media.NewFSStorage(t.TempDir())
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
