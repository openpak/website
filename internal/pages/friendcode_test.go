package pages

import (
	"regexp"
	"strings"
	"testing"
)

// TestFriendCodeAcceptsWhatItAdvertises pins the three things that must
// agree: the format the account page displays, the pattern the form
// enforces in the browser, and what normalizeFriendCode sends to the
// contract. They drifted once (the pattern demanded four groups of four
// while a code is three), which rejected the code the site itself shows.
func TestFriendCodeAcceptsWhatItAdvertises(t *testing.T) {
	// Both lengths occur in the wild: a live link subject observed against
	// the running adapter is 16 hex digits, the fixtures use 12.
	for _, subject := range []string{"0123456789ab", "c4dd2c5a3734c55c"} {
		t.Run(subject, func(t *testing.T) { checkFriendCode(t, subject) })
	}
}

func checkFriendCode(t *testing.T, subject string) {
	t.Helper()
	displayed := fmtFriendCode(subject) // SW-xxxx-xxxx-xxxx[-xxxx]
	if !strings.HasPrefix(displayed, "SW-") {
		t.Fatalf("fmtFriendCode(%q) = %q, want an SW- code", subject, displayed)
	}

	src, err := templateFS.ReadFile("templates/account.html")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`id="add-code"[^>]*?pattern="([^"]+)"`).FindStringSubmatch(string(src))
	if pattern == nil {
		t.Fatal("no pattern attribute on the friend-code field")
	}
	// HTML5 pattern is implicitly anchored to the whole value.
	form := regexp.MustCompile("^(?:" + pattern[1] + ")$")

	for _, in := range []string{
		displayed,                  // copied straight off the account page
		strings.ToUpper(displayed), // shouting it
		displayed[3:],              // without the SW- prefix
		subject,                    // bare subject
	} {
		if !form.MatchString(in) {
			t.Errorf("form pattern rejects %q", in)
		}
		if got := normalizeFriendCode(in); got != subject {
			t.Errorf("normalizeFriendCode(%q) = %q, want %q", in, got, subject)
		}
	}

	// A code of the wrong length must not slip past the browser.
	for _, in := range []string{"SW-0123-4567", "0123456789abcde"} {
		if form.MatchString(in) {
			t.Errorf("form pattern accepts malformed %q", in)
		}
	}
}
