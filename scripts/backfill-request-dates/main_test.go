package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A catalog with a side branch: A never changes; B's changes on main and again on a side
// branch, and its last_updated is the latest of those; C is added in the second commit.
func TestHistoryFixture(t *testing.T) {
	dir := t.TempDir()
	run := func(date string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE=2030-01-01T00:00:00Z",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(date, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		run(date, "add", "catalog.json")
		run(date, "commit", "-qm", date)
	}
	const a = `{"kind":"title","name":"A","console":"Switch","status":"beta"}`
	run("2026-01-01T00:00:00Z", "init", "-q", "-b", "main")
	commit("2026-01-01T00:00:00Z", `[`+a+`,{"kind":"title","name":"B's","console":"Wii U","status":"requestable"}]`)
	commit("2026-02-01T00:00:00Z", `[`+a+`,{"kind":"title","name":"B's","console":"Wii U","status":"beta"},{"kind":"feature","name":"C","console":"","status":"planned"}]`)
	run("2026-03-01T00:00:00Z", "checkout", "-qb", "side")
	commit("2026-03-01T00:00:00Z", `[`+a+`,{"kind":"title","name":"B's","console":"Wii U","status":"live"}]`)
	run("2026-03-01T00:00:00Z", "checkout", "-q", "main")

	got, err := history(dir)
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	want := map[key]dates{
		{"title", "A", "Switch"}:  {day("2026-01-01"), day("2026-01-01")},
		{"title", "B's", "Wii U"}: {day("2026-01-01"), day("2026-03-01")},
		{"feature", "C", ""}:      {day("2026-02-01"), day("2026-02-01")},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %v", len(got), got)
	}
	for k, w := range want {
		if !got[k].Added.Equal(w.Added) || !got[k].Updated.Equal(w.Updated) {
			t.Errorf("%v: got %v, want %v", k, got[k], w)
		}
	}
	var sql strings.Builder
	writeSQL(&sql, got)
	if !strings.Contains(sql.String(), `'B''s'`) || !strings.HasPrefix(sql.String(), "BEGIN;") {
		t.Errorf("sql:\n%s", sql.String())
	}
}
