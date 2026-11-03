package logstore

import (
	"strings"
	"testing"
	"time"
)

func TestQuerySQL(t *testing.T) {
	q := Query{Service: "a", Day: time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC), MinLevel: Warn, Text: "50%_x", BeforeID: 9}
	w, args := q.sql()
	if !strings.Contains(w, "msg ILIKE $5") || !strings.Contains(w, "id < $6") || len(args) != 6 {
		t.Fatalf("%s %v", w, args)
	}
	if args[4] != `%50\%\_x%` {
		t.Errorf("like not escaped: %v", args[4])
	}
	if !args[1].(time.Time).Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day not truncated")
	}
}

func TestFormatLine(t *testing.T) {
	l := Line{TS: time.Date(2026, 11, 3, 1, 2, 3, 4e6, time.UTC), Level: Error, Msg: "boom"}
	if got := FormatLine(l); got != "2026-11-03T01:02:03.004Z ERROR boom" {
		t.Errorf("%q", got)
	}
}
