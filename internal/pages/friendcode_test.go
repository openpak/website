package pages

import (
	"os"
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

	// The add-friend dialog's pattern for a Switch code lives in site.js,
	// which sets it when the kind is chosen and upper-cases what is typed.
	js, err := os.ReadFile("../../web/static/site.js")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`switch: \{[^}]*pattern: "([^"]+)"`).FindSubmatch(js)
	if pattern == nil {
		t.Fatal("no Switch pattern in site.js")
	}
	// HTML5 pattern is implicitly anchored to the whole value.
	form := regexp.MustCompile("^(?:" + string(pattern[1]) + ")$")
	if !form.MatchString(strings.ToUpper(displayed)) {
		t.Errorf("form pattern rejects %q, the code the site shows", strings.ToUpper(displayed))
	}

	for _, in := range []string{
		displayed,                  // copied straight off the account page
		strings.ToUpper(displayed), // shouting it
		displayed[3:],              // without the SW- prefix
		subject,                    // bare subject
	} {
		if got := normalizeFriendCode(in); got != subject {
			t.Errorf("normalizeFriendCode(%q) = %q, want %q", in, got, subject)
		}
	}

	// A code of the wrong length must not slip past the browser.
	for _, in := range []string{"SW-0123-4567", "SW-0123-4567-89AB-C"} {
		if form.MatchString(in) {
			t.Errorf("form pattern accepts malformed %q", in)
		}
	}
}
