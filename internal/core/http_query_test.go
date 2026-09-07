package core

import (
	"net/url"
	"strings"
	"testing"
)

// A query string must survive the base-URL join: url.URL.JoinPath escapes "?" into the
// path, which made every verification link report "invalid or expired" (the core 404s on
// /v1/verify%3Ftoken=...).
func TestQuerySurvivesJoinPath(t *testing.T) {
	base, _ := url.Parse("http://openpak-account:20000")
	pathOnly, rawQuery, _ := strings.Cut(verifyPath("abc 123"), "?")
	u := base.JoinPath(pathOnly)
	u.RawQuery = rawQuery

	if got, want := u.String(), "http://openpak-account:20000/v1/verify?token=abc+123"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
	if got := u.Query().Get("token"); got != "abc 123" {
		t.Errorf("token = %q, want %q", got, "abc 123")
	}
	if strings.Contains(u.String(), "%3F") {
		t.Errorf("query got escaped into the path: %s", u)
	}
}
