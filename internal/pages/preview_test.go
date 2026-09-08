package pages

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
)

// TestWritePreview renders every page to HTML files so they can be looked at in
// a browser at a phone's width. It runs only when PREVIEW_DIR is set, because
// its purpose is a human looking at the result, not an assertion.
//
//	PREVIEW_DIR=/tmp/preview go test ./internal/pages/ -run WritePreview
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the page previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	s, fc, _ := newTestServer(t)
	seedTwoAccounts(fc)
	fc.friends = append(fc.friends, friendRow{
		requester: "acct-a@x.test", addressee: "acct-b@x.test", accepted: true})
	fc.presence = map[string]core.Presence{
		"acct-b@x.test": {Namespace: "switch", TitleID: "0100E65002BB8000", Since: time.Now()},
	}
	fc.players = []core.TitlePlayers{
		{TitleID: "0100ED9024EB8000", Namespace: "switch", Players: 2},
		{TitleID: "0100E65002BB8000", Namespace: "switch", Players: 1},
	}
	fc.networkPlayers = map[string]int{"switch": 3}
	fc.totalPlayers = 3
	fc.invitations = []core.PendingInvitation{{
		ID: "inv-1", FromName: "Leia", TitleID: "0100ED9024EB8000",
		Namespace: "switch", ExpiresAt: time.Now().Add(4 * time.Minute),
	}}

	rec := login(t, s, "a@x.test", "password-A-1")

	for name, path := range map[string]string{
		"home": "/", "features": "/features", "downloads": "/downloads",
		"status": "/status", "login": "/login", "register": "/register",
		"forgot": "/forgot-password",
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		write(t, dir, name, w.Body.String())
	}
	// Signed-in pages need the session cookie the login handed back.
	for name, path := range map[string]string{
		"account": "/account", "friends": "/account/friends",
	} {
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		write(t, dir, name, w.Body.String())
	}
	t.Logf("previews written to %s", dir)
}

func write(t *testing.T, dir, name, html string) {
	t.Helper()
	// Point at a local copy of the stylesheet so the file renders standalone.
	html = strings.ReplaceAll(html, "/static/site.css?v=", "site.css?v=")
	html = strings.ReplaceAll(html, "/static/icon.svg?v=", "icon.svg?v=")
	if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
