package accounts

import "time"

// AdultAge is the age Pakka (and the model provider behind it) requires.
const AdultAge = 18

// IsAdult reports whether the YYYY-MM-DD birthday is a real past date at
// least AdultAge years ago. A missing or malformed birthday is not adult:
// callers that gate children out fail closed on unknown age.
func IsAdult(birthday string, now time.Time) bool {
	return CheckAge(birthday, AdultAge, now) == nil
}
