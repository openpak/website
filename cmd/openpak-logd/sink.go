package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const maxPending = 50000

// sink ships batches to the website's /internal/logs/ingest, which holds the
// Postgres side (day partitions, 90-day drop, the admin viewer). The host
// cannot reach Postgres, which has no published port, and the website
// already holds the database credentials. Lines the website cannot take are
// kept in memory and retried; the day files are written either way.
type sink struct {
	url, key, prefix string
	pending          []entry
	client           *http.Client
}

func newSink(cfg config) *sink {
	if cfg.IngestURL == "" {
		return nil
	}
	return &sink{url: cfg.IngestURL, key: cfg.IngestKey, prefix: cfg.DBPrefix, client: &http.Client{Timeout: 20 * time.Second}}
}

type wireLine struct {
	TS      time.Time `json:"ts"`
	Service string    `json:"service"`
	Level   int       `json:"level"`
	Msg     string    `json:"msg"`
}

func (s *sink) add(ctx context.Context, batch []entry) {
	s.pending = append(s.pending, batch...)
	if len(s.pending) > maxPending {
		drop := len(s.pending) - maxPending
		log.Printf("ingest unreachable: dropping %d oldest lines from the database (the day files keep them)", drop)
		s.pending = s.pending[drop:]
	}
	s.retry(ctx)
}

func (s *sink) retry(ctx context.Context) {
	for len(s.pending) > 0 {
		n := min(len(s.pending), 5000)
		if err := s.post(ctx, s.pending[:n]); err != nil {
			log.Printf("ingest: %v", err)
			return
		}
		s.pending = s.pending[n:]
	}
	s.pending = nil
}

func (s *sink) post(ctx context.Context, es []entry) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range es {
		_ = enc.Encode(wireLine{e.TS, s.prefix + e.Service, e.Level, e.Msg})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-Internal-Key", s.key)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
