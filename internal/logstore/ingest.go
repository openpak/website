package logstore

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var serviceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@/-]{0,99}$`)

const (
	maxBody  = 16 << 20
	maxLines = 20000
	maxMsg   = 4096
)

// IngestHandler takes NDJSON {"ts","service","level","msg"} from the host
// collector. key must be non-empty: without one the route refuses everything.
func IngestHandler(s *Store, key string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			http.Error(w, "not configured", http.StatusServiceUnavailable)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sc := bufio.NewScanner(http.MaxBytesReader(w, r.Body, maxBody))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		now := time.Now()
		var lines []Line
		for sc.Scan() && len(lines) < maxLines {
			var l Line
			if json.Unmarshal(sc.Bytes(), &l) != nil || !serviceRe.MatchString(l.Service) ||
				l.Level < Debug || l.Level > Error || l.Msg == "" ||
				l.TS.Before(now.AddDate(0, 0, -Keep)) || l.TS.After(now.Add(time.Hour)) {
				continue
			}
			l.Msg = strings.ToValidUTF8(strings.ReplaceAll(l.Msg, "\x00", ""), "")
			if len(l.Msg) > maxMsg {
				l.Msg = strings.ToValidUTF8(l.Msg[:maxMsg], "")
			}
			lines = append(lines, l)
		}
		if err := sc.Err(); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if len(lines) > 0 {
			if err := s.Ingest(ctx, lines); err != nil {
				log.Error("log ingest", "err", err)
				http.Error(w, "store failed", http.StatusBadGateway)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Run keeps the partitions: at start (Open already did) and every 6 hours.
func (s *Store) Run(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Maintain(ctx, time.Now()); err != nil {
				log.Error("log partitions", "err", err)
			}
		}
	}
}
