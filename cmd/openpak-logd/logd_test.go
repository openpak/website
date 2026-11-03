package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedact(t *testing.T) {
	for _, c := range []struct{ in, notWant string }{
		{"login ok password=hunter2 user=a", "hunter2"},
		{`body {"token":"abc.def"}`, "abc.def"},
		{"Authorization: Bearer abcdefghijkl1234", "abcdefghijkl1234"},
		{"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig_nature-1 end", "eyJhbGci"},
		{"cookie=session=abcd1234 x", "abcd1234"},
		{"-----BEGIN PRIVATE KEY----- MIIE", "MIIE"},
		{"internal_key=zzz999", "zzz999"},
	} {
		if got := redact(c.in); strings.Contains(got, c.notWant) {
			t.Errorf("redact(%q) = %q still holds %q", c.in, got, c.notWant)
		}
	}
	if got := redact("GET /v1/x -> 200 12ms"); got != "GET /v1/x -> 200 12ms" {
		t.Errorf("plain line changed: %q", got)
	}
}

func TestClassify(t *testing.T) {
	for in, want := range map[string]int{
		"2026/11/03 listening on :20000": levelInfo,
		"level=ERROR msg=boom":           levelError,
		"panic: nil pointer":             levelError,
		"level=warn msg=slow":            levelWarn,
		"dial tcp: connection refused":   levelWarn,
		"[nx-baas] TLS hello sni=x":      levelInfo,
		`{"level":"error","msg":"x"}`:    levelError,
		"GET /a -> 500 3ms":              levelInfo,
	} {
		if got := classify(in); got != want {
			t.Errorf("classify(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseJournal(t *testing.T) {
	line := `{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1793739280900755","_SYSTEMD_USER_UNIT":"openpak-account.service","MESSAGE":"hello\tworld token=abc123"}`
	e, cur, ok := parseJournal([]byte(line))
	if !ok || cur != "c1" || e.Service != "openpak-account" || strings.Contains(e.Msg, "abc123") {
		t.Fatalf("got %+v %q %v", e, cur, ok)
	}
	if e.TS.Format("2006-01-02") != "2026-11-03" {
		t.Errorf("day %v", e.TS)
	}
	hash := `{"__CURSOR":"c2","_SYSTEMD_USER_UNIT":"7fe5165c4a791a305ad9e999077d81d2b7f5bb01c998c3112e7a1b2c3d4e5f60.service","MESSAGE":"x"}`
	if _, cur, ok := parseJournal([]byte(hash)); ok || cur != "c2" {
		t.Errorf("hash unit kept")
	}
	evil := `{"__CURSOR":"c3","_SYSTEMD_USER_UNIT":"../../etc.service","MESSAGE":"x"}`
	if e, _, _ := parseJournal([]byte(evil)); strings.Contains(e.Service, "/") {
		t.Errorf("path in service %q", e.Service)
	}
}

func TestAppendAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	mk := func(day time.Time) entry { return entry{TS: day, Service: "svc", Level: levelInfo, Msg: "m"} }
	days := []int{0, 1, 3, 100}
	var es []entry
	for _, d := range days {
		es = append(es, mk(now.AddDate(0, 0, -d)))
	}
	if err := appendFiles(dir, es); err != nil {
		t.Fatal(err)
	}
	if err := prune(t.Context(), config{Dir: dir, KeepDays: 90, GzipAfter: 2}, now); err != nil {
		t.Fatal(err)
	}
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, "svc", n)); return err == nil }
	if !has("2026-11-03.log") || !has("2026-11-02.log") || !has("2026-10-31.log.gz") || has("2026-10-31.log") {
		t.Errorf("unexpected layout")
	}
	if has("2025-07-25.log") || has("2025-07-25.log.gz") {
		t.Errorf("old file kept")
	}
}
