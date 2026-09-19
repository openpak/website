// Command backfill-request-dates is the one-off that dates the requests rows migration 0005
// stamped with the migration time. It reads the openpak/catalog repository's history and
// prints SQL for psql:
//
//	go run ./scripts/backfill-request-dates -repo ../docs/catalog | psql "$WEBSITE_DATABASE_URL"
//
// date_added is the first commit an entry (kind, name, console) appears in, last_updated the
// last commit that changed it; author dates, since the 2026-09-19 history rewrite reset the
// committer dates. Every ref is read (--all), so unpushed commits count. A row with no
// history keeps the migration time, and psql prints it. A row already changed through the
// store (it has request_changes) keeps its last_updated.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"
)

type key struct{ Kind, Name, Console string }

type dates struct{ Added, Updated time.Time }

func main() {
	repo := flag.String("repo", ".", "a clone of openpak/catalog")
	flag.Parse()
	d, err := history(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writeSQL(os.Stdout, d)
}

func git(repo string, args ...string) ([]byte, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// entriesAt is catalog.json at a commit, by key; empty when the commit has no such file.
func entriesAt(repo, rev string) (map[key]map[string]any, error) {
	b, err := git(repo, "show", rev+":catalog.json")
	if err != nil {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", rev, err)
	}
	out := map[key]map[string]any{}
	for _, e := range raw {
		s := func(k string) string { v, _ := e[k].(string); return v }
		out[key{s("kind"), s("name"), s("console")}] = e
	}
	return out, nil
}

// history dates every entry from each commit against its first parent.
func history(repo string) (map[key]dates, error) {
	log, err := git(repo, "log", "--all", "--format=%H %P|%aI", "--", "catalog.json")
	if err != nil {
		return nil, err
	}
	out := map[key]dates{}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		shas, when, _ := strings.Cut(line, "|")
		at, err := time.Parse(time.RFC3339, when)
		if err != nil {
			return nil, err
		}
		f := strings.Fields(shas)
		cur, err := entriesAt(repo, f[0])
		if err != nil {
			return nil, err
		}
		var prev map[key]map[string]any
		if len(f) > 1 {
			if prev, err = entriesAt(repo, f[1]); err != nil {
				return nil, err
			}
		}
		for k, e := range cur {
			d, seen := out[k]
			if !seen || at.Before(d.Added) {
				d.Added = at
			}
			if !reflect.DeepEqual(e, prev[k]) && at.After(d.Updated) {
				d.Updated = at
			}
			out[k] = d
		}
	}
	// Across branches the first appearance can be dated after a change on another branch.
	for k, d := range out {
		if d.Updated.Before(d.Added) {
			d.Updated = d.Added
			out[k] = d
		}
	}
	return out, nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func writeSQL(w io.Writer, d map[key]dates) {
	keys := make([]key, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	var b bytes.Buffer
	b.WriteString("BEGIN;\nCREATE TEMP TABLE catalog_dates(kind text, name text, console text, date_added timestamptz, last_updated timestamptz) ON COMMIT DROP;\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "INSERT INTO catalog_dates VALUES(%s, %s, %s, %s, %s);\n", quote(k.Kind), quote(k.Name), quote(k.Console),
			quote(d[k].Added.Format(time.RFC3339)), quote(d[k].Updated.Format(time.RFC3339)))
	}
	b.WriteString(`UPDATE requests r SET date_added = d.date_added,
	last_updated = CASE WHEN EXISTS (SELECT 1 FROM request_changes c WHERE c.request_id = r.id) THEN r.last_updated ELSE d.last_updated END
	FROM catalog_dates d WHERE (r.kind, r.name, r.console) = (d.kind, d.name, d.console);
SELECT 'no catalog history, kept the migration time: ' || kind || ' / ' || name || ' / ' || console AS backfill
	FROM requests r WHERE NOT EXISTS (SELECT 1 FROM catalog_dates d WHERE (r.kind, r.name, r.console) = (d.kind, d.name, d.console));
SELECT count(*) AS dated FROM requests r JOIN catalog_dates d ON (r.kind, r.name, r.console) = (d.kind, d.name, d.console);
COMMIT;
`)
	_, _ = w.Write(b.Bytes())
}
