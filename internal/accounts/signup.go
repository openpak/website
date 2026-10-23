package accounts

import (
	"errors"
	"strings"
	"time"

	"github.com/openpak/website/internal/ratelimit"
)

// MailPerHour is how many verification or reset mails one address may cause
// per hour, across the site and the API.
const MailPerHour = 3

// NewMailLimit is the shared per-address mail throttle.
// ponytail: in memory, so the budget is per process; a second website
// instance would double it. Move it to Postgres when the site scales out.
func NewMailLimit() *ratelimit.Limiter { return ratelimit.New(MailPerHour, time.Hour) }

// MailAllowed spends one mail from key's budget (an email address or an
// account id). A nil limiter allows everything.
func MailAllowed(l *ratelimit.Limiter, key string) bool {
	return l == nil || l.Allow("mail:"+strings.ToLower(strings.TrimSpace(key)))
}

// ErrBirthdayInvalid is a missing, malformed or future date of birth.
var ErrBirthdayInvalid = errors.New("accounts: enter a valid date of birth")

// ErrTooYoung is a date of birth under the minimum age.
var ErrTooYoung = errors.New("accounts: below the minimum age")

// CheckAge enforces the signup minimum age (WEBSITE_MIN_AGE) on a YYYY-MM-DD
// birthday. minAge 0 turns the check off. The account core only checks the
// format, so this is where the age rule lives for both the site and the API.
func CheckAge(birthday string, minAge int, now time.Time) error {
	if minAge <= 0 {
		return nil
	}
	b, err := time.Parse("2006-01-02", strings.TrimSpace(birthday))
	if err != nil || b.After(now) {
		return ErrBirthdayInvalid
	}
	// The birthday of someone exactly minAge today; 29 February rolls to 1 March.
	if b.AddDate(minAge, 0, 0).After(now) {
		return ErrTooYoung
	}
	return nil
}
