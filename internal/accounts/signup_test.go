package accounts

import (
	"errors"
	"testing"
	"time"
)

func TestCheckAge(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		birthday string
		min      int
		want     error
	}{
		{"2010-10-23", 16, nil},         // sixteen today
		{"2010-10-24", 16, ErrTooYoung}, // sixteen tomorrow
		{"1990-01-01", 16, nil},
		{"2020-01-01", 0, nil}, // check off
		{"", 0, nil},
		{"", 16, ErrBirthdayInvalid},
		{"23/10/2010", 16, ErrBirthdayInvalid},
		{"2027-01-01", 16, ErrBirthdayInvalid}, // the future
		{"2013-10-23", 13, nil},
	} {
		if err := CheckAge(c.birthday, c.min, now); !errors.Is(err, c.want) {
			t.Errorf("CheckAge(%q, %d) = %v, want %v", c.birthday, c.min, err, c.want)
		}
	}
}
