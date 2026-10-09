package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// TestWritePreview renders every page to HTML files so they can be looked at in
// a browser at a phone's width. It runs only when PREVIEW_DIR is set, because
// its purpose is a human looking at the result, not an assertion.
//
//	PREVIEW_DIR=$PWD/.preview go test ./internal/pages/ ./internal/admin/ -run WritePreview
//	ln -s $PWD/web/static .preview/static
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the page previews")
	}
	t.Chdir("../..") // the repository root, as in production: web/static (cover tiles) resolves there
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	s, fc, db := newTestServer(t)
	seedTwoAccounts(fc)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Outbound", Console: "Switch", Status: "beta", TitleID: "0100ED9024EB8000", TitleIDs: []string{"0100ED9024EB8000"}},
		{Kind: "title", Name: "Stardew Valley", Console: "Switch", Status: "live", TitleID: "0100E65002BB8000", TitleIDs: []string{"0100E65002BB8000"}},
		{Kind: "title", Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "alpha", TitleID: "0100152000022000", TitleIDs: []string{"0100152000022000"}},
		{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "planned", TitleID: "0100C2500FC20000", TitleIDs: []string{"0100C2500FC20000"}},
		{Kind: "title", Name: "Mario Kart 7", Console: "3DS", Status: "beta"},
		{Kind: "title", Name: "Mario Kart Wii", Console: "Wii", Status: "live"},
		{Kind: "title", Name: "Halo 3", Console: "Xbox 360", Status: "beta"},
		{Kind: "title", Name: "Killzone 2", Console: "PS3", Status: "live"},
		{Kind: "title", Name: "Monster Hunter Freedom Unite", Console: "PSP", Status: "beta"},
		{Kind: "title", Name: "Wii U Chat", Console: "Wii U", Status: "beta"}, // no tile: keeps the console mark
		{Kind: "title", Name: "Super Smash Bros. Ultimate", Console: "Switch", Status: "planned", TitleID: "01006A800016E000", TitleIDs: []string{"01006A800016E000"}},
	})
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
		"forgot": "/forgot-password", "titles": "/titles", "help": "/help",
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		write(t, dir, name, w.Body.String())
	}
	// Signed-in pages need the session cookie the login handed back.
	for name, path := range map[string]string{
		"account": "/account", "friends": "/account/friends", "saves": "/account/saves",
		"parental": "/account/parental",
	} {
		w := authedRequest(s, rec, http.MethodGet, path, nil)
		write(t, dir, name, w.Body.String())
	}
	t.Logf("previews written to %s", dir)
}

func write(t *testing.T, dir, name, html string) {
	t.Helper()
	WritePreviewFile(t, dir, name, html)
}

// switchIcon is the icon route, pointed at its upstream so a preview file shows real art.
var switchIcon = regexp.MustCompile(`/titles/icon/([0-9A-Fa-f]{16})`)

// WritePreviewFile makes a rendered page openable as a file: /static/ becomes
// a relative static/ (link web/static there) and title icons load from nlib.
func WritePreviewFile(t testing.TB, dir, name, html string) {
	t.Helper()
	html = strings.ReplaceAll(html, `"/static/`, `"static/`)
	html = strings.ReplaceAll(html, ` /static/`, ` static/`)
	html = switchIcon.ReplaceAllString(html, nlibBase+"$1/icon/256/256")
	if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
