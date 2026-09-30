// Command seed-playability fills the Ryujinx column of title_playability from Ryujinx's own
// compatibility list, once:
//
//	go run ./scripts/seed-playability -base https://openpak.org -key "$WEBSITE_CATALOG_KEY" \
//		-csv ../emulators/ryujinx/docs/compatibility.csv
//
// The CSV's ~3500 rows are keyed by title id; the catalogue's are keyed by the site's request
// id, so the join is over title_id and every entry of title_ids. Only titles the catalogue
// already carries get a row — the CSV is Ryujinx's whole library, not OpenPak's. A game whose
// several ids disagree takes the best of them, which is how the emulator would read it.
//
// It writes through PATCH /api/v1/admin/titles/{id}, so every row goes down the same store
// path as an admin edit: one request_changes line and one last_updated bump each. -n prints
// what it would send and writes nothing.
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// states, best first: the order the CSV's five words rank in.
var states = []string{"playable", "ingame", "menus", "boots", "nothing"}

func main() {
	base := flag.String("base", "https://openpak.org", "site to write to")
	key := flag.String("key", os.Getenv("WEBSITE_CATALOG_KEY"), "catalog write key")
	path := flag.String("csv", "", "Ryujinx's docs/compatibility.csv")
	client := flag.String("client", "Ryujinx", "the client column to fill")
	dry := flag.Bool("n", false, "print, do not write")
	flag.Parse()
	if *path == "" || (*key == "" && !*dry) {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*base, *key, *path, *client, *dry); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(base, key, path, client string, dry bool) error {
	byID, err := readCSV(path)
	if err != nil {
		return err
	}
	titles, err := catalogue(base)
	if err != nil {
		return err
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	var wrote, skipped int
	for _, t := range titles {
		best := ""
		for _, id := range append([]string{t.TitleID}, t.TitleIDs...) {
			if s := byID[strings.ToUpper(id)]; id != "" && s != "" && (best == "" || slices.Index(states, s) < slices.Index(states, best)) {
				best = s
			}
		}
		if best == "" {
			skipped++
			continue
		}
		if dry {
			fmt.Printf("%d\t%s\t%s\n", t.ID, t.Name, best)
			wrote++
			continue
		}
		if err := patch(hc, base, key, t.ID, client, best); err != nil {
			return fmt.Errorf("%s (%d): %w", t.Name, t.ID, err)
		}
		wrote++
	}
	fmt.Fprintf(os.Stderr, "%d titles written, %d not in the list\n", wrote, skipped)
	return nil
}

// readCSV is title id (upper case) -> status. A status the five words do not cover is a
// mistake in the list, not something to guess at.
func readCSV(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, r := range rows {
		if i == 0 || len(r) < 4 { // the header names the columns
			continue
		}
		id, status := strings.ToUpper(strings.TrimSpace(r[0])), strings.TrimSpace(r[3])
		if id == "" { // the list carries homebrew with no title id; nothing to join on
			continue
		}
		if !slices.Contains(states, status) {
			return nil, fmt.Errorf("%s line %d: status %q", path, i+1, status)
		}
		out[id] = status
	}
	return out, nil
}

type title struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	TitleID  string   `json:"title_id"`
	TitleIDs []string `json:"title_ids"`
}

func catalogue(base string) ([]title, error) {
	resp, err := http.Get(base + "/api/v1/titles")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET /api/v1/titles: %s", resp.Status)
	}
	var body struct {
		Titles []title `json:"titles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Titles, nil
}

func patch(c *http.Client, base, key string, id int64, client, status string) error {
	b, _ := json.Marshal(map[string]any{"playability": map[string]string{client: status}})
	req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/v1/admin/titles/%d", base, id), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	return nil
}
