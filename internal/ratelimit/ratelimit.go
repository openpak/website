// Package ratelimit provides a dependency-free fixed-window limiter used on
// authentication endpoints (per client IP) and on the mobile API (per token).
package ratelimit

import (
	"sync"
	"time"
)

type window struct {
	start time.Time
	count int
}

// Limiter allows max events per key per window.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*window
	max     int
	per     time.Duration
}

// New builds a limiter allowing max events per key in every per-long window.
func New(max int, per time.Duration) *Limiter {
	l := &Limiter{windows: map[string]*window{}, max: max, per: per}
	go l.gc()
	return l
}

// Allow reports whether one event for key is permitted.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= l.per {
		l.windows[key] = &window{start: now, count: 1}
		return true
	}
	if w.count >= l.max {
		return false
	}
	w.count++
	return true
}

// gc drops stale windows so the map does not grow without bound.
func (l *Limiter) gc() {
	for range time.Tick(10 * time.Minute) {
		cut := time.Now().Add(-l.per)
		l.mu.Lock()
		for k, w := range l.windows {
			if w.start.Before(cut) {
				delete(l.windows, k)
			}
		}
		l.mu.Unlock()
	}
}
