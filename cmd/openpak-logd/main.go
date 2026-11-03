// openpak-logd is the host log collector. Every service already writes to
// stdout/stderr, which podman hands to the user journal; this follows that
// journal and keeps, per service, one file per UTC day
// (<LOGD_DIR>/<service>/<YYYY-MM-DD>.log), plus (when LOGD_DATABASE_URL is
// set) ships the same lines to the website, which keeps them in a
// day-partitioned Postgres table the admin site reads. `openpak-logd prune`
// gzips files older than LOGD_GZIP_AFTER_DAYS and deletes files older than
// LOGD_KEEP_DAYS; it is meant for a daily systemd timer (the website drops
// the old partitions itself). See docs/logs.md.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Dir       string
	IngestURL string
	IngestKey string
	DBPrefix  string
	Cursor    string
	KeepDays  int
	GzipAfter int
	Backfill  time.Duration
	Journal   []string
}

func envInt(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return d
}

func loadConfig() config {
	home, _ := os.UserHomeDir()
	c := config{
		Dir:       envOr("LOGD_DIR", filepath.Join(home, "openpak-logs")),
		IngestURL: os.Getenv("LOGD_INGEST_URL"),
		IngestKey: os.Getenv("LOGD_INGEST_KEY"),
		DBPrefix:  os.Getenv("LOGD_DB_PREFIX"),
		KeepDays:  envInt("LOGD_KEEP_DAYS", 90),
		GzipAfter: envInt("LOGD_GZIP_AFTER_DAYS", 2),
		Backfill:  48 * time.Hour,
		Journal:   strings.Fields(envOr("LOGD_JOURNAL_ARGS", "--user")),
	}
	if d, err := time.ParseDuration(os.Getenv("LOGD_BACKFILL")); err == nil {
		c.Backfill = d
	}
	c.Cursor = envOr("LOGD_CURSOR", filepath.Join(c.Dir, ".cursor"))
	return c
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "run":
		err = run(ctx, cfg)
	case "prune":
		err = prune(ctx, cfg, time.Now().UTC())
	default:
		err = fmt.Errorf("usage: openpak-logd [run|prune]")
	}
	if err != nil {
		log.Printf("openpak-logd %s: %v", cmd, err)
		os.Exit(1)
	}
}

// entry is one journal line, already reduced to what is kept.
type entry struct {
	TS      time.Time
	Service string
	Level   int
	Msg     string
}

func run(ctx context.Context, cfg config) error {
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return err
	}
	db := newSink(cfg)
	failures := 0
	for ctx.Err() == nil {
		cursor := readCursor(cfg.Cursor)
		start := time.Now()
		err := follow(ctx, cfg, cursor, db)
		if ctx.Err() != nil {
			break
		}
		// A cursor the journal no longer holds makes journalctl fail at once;
		// start over from the backfill window rather than loop on it.
		if time.Since(start) < 10*time.Second {
			failures++
			if failures >= 3 && cursor != "" {
				log.Printf("cursor rejected (%v); restarting from the backfill window", err)
				_ = os.Remove(cfg.Cursor)
				failures = 0
			}
		} else {
			failures = 0
		}
		log.Printf("journal follower ended: %v; retrying", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func readCursor(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveCursor(path, c string) {
	if c == "" {
		return
	}
	if err := os.WriteFile(path+".new", []byte(c+"\n"), 0o640); err == nil {
		_ = os.Rename(path+".new", path)
	}
}

func follow(ctx context.Context, cfg config, cursor string, db *sink) error {
	args := append([]string{}, cfg.Journal...)
	args = append(args, "-o", "json", "--no-pager", "-f",
		"--output-fields=MESSAGE,_SYSTEMD_USER_UNIT,_SYSTEMD_UNIT,SYSLOG_IDENTIFIER,CONTAINER_NAME")
	if cursor != "" {
		args = append(args, "--after-cursor="+cursor)
	} else {
		args = append(args, "--since", time.Now().Add(-cfg.Backfill).UTC().Format("2006-01-02 15:04:05"))
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	lines := make(chan []byte, 1024)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Done():
				return
			}
		}
	}()

	var batch []entry
	var last string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := appendFiles(cfg.Dir, batch); err != nil {
			log.Printf("write files: %v", err)
			return // keep the batch and the old cursor; try again next tick
		}
		if db != nil {
			db.add(ctx, batch)
		}
		saveCursor(cfg.Cursor, last)
		batch = batch[:0]
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case b, ok := <-lines:
			if !ok {
				flush()
				return cmd.Wait()
			}
			e, cur, ok := parseJournal(b)
			if cur != "" {
				last = cur
			}
			if ok {
				batch = append(batch, e)
			}
			if len(batch) >= 1000 {
				flush()
			}
		case <-tick.C:
			if len(batch) == 0 {
				saveCursor(cfg.Cursor, last)
			}
			flush()
			if db != nil {
				db.retry(ctx)
			}
		case <-ctx.Done():
			flush()
			_ = cmd.Wait()
			return ctx.Err()
		}
	}
}

// parseJournal reduces one `journalctl -o json` object. ok is false for lines
// that are not service output (the manager's own, podman's health probes and
// transient hash-named units); cursor is returned either way so a skipped
// line still advances the position.
func parseJournal(b []byte) (e entry, cursor string, ok bool) {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return e, "", false
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	cursor = str("__CURSOR")
	unit := strings.TrimSuffix(str("_SYSTEMD_USER_UNIT"), ".service")
	if unit == "" {
		unit = strings.TrimSuffix(str("_SYSTEMD_UNIT"), ".service")
	}
	if unit == "" {
		unit = str("SYSLOG_IDENTIFIER")
	}
	unit = cleanService(unit)
	if unit == "" || unit == "init.scope" || isHashUnit(unit) || strings.HasPrefix(unit, "session-") {
		return e, cursor, false
	}
	msg := ""
	switch v := m["MESSAGE"].(type) {
	case string:
		msg = v
	case []any: // binary message: journalctl prints the bytes
		bs := make([]byte, 0, len(v))
		for _, x := range v {
			if f, ok := x.(float64); ok {
				bs = append(bs, byte(f))
			}
		}
		msg = string(bs)
	}
	us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64)
	if err != nil {
		us = time.Now().UnixMicro()
	}
	msg = redact(cleanMessage(msg))
	if msg == "" {
		return e, cursor, false
	}
	return entry{TS: time.UnixMicro(us).UTC(), Service: unit, Level: classify(msg), Msg: msg}, cursor, true
}

// appendFiles writes the batch to <dir>/<service>/<day>.log.
func appendFiles(dir string, batch []entry) error {
	type key struct{ svc, day string }
	groups := map[key]*strings.Builder{}
	for _, e := range batch {
		k := key{e.Service, e.TS.Format("2006-01-02")}
		sb := groups[k]
		if sb == nil {
			sb = &strings.Builder{}
			groups[k] = sb
		}
		sb.WriteString(formatLine(e))
		sb.WriteByte('\n')
	}
	for k, sb := range groups {
		d := filepath.Join(dir, k.svc)
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(d, k.day+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		_, err = f.WriteString(sb.String())
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
