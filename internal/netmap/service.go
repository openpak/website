package netmap

import (
	"context"
	"sync"
	"time"
)

// Service gathers the inputs and keeps the last graph for a short while, so a
// page of visitors costs one round of questions to the hosts, not one each.
type Service struct {
	// Topology asks every host agent (admin.OpsAPI.Topology).
	Topology func(ctx context.Context) []HostTopo
	// Status reads the availability page; nil result means unreachable.
	Status func(ctx context.Context) *StatusPage
	// Stages is the per-console stage from the features rows.
	Stages func(ctx context.Context) map[string]string
	LogsOn bool
	TTL    time.Duration

	mu   sync.Mutex
	at   time.Time
	last Graph
}

// Graph is the current map, at most TTL old. A failed refresh keeps serving the
// previous graph rather than an empty one.
func (s *Service) Graph(ctx context.Context) Graph {
	ttl := s.TTL
	if ttl == 0 {
		ttl = 20 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < ttl {
		return s.last
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var in Input
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); in.Hosts = s.Topology(ctx) }()
	go func() {
		defer wg.Done()
		if s.Status != nil {
			in.Status = s.Status(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		if s.Stages != nil {
			in.Stages = s.Stages(ctx)
		}
	}()
	wg.Wait()
	in.LogsOn, in.Now = s.LogsOn, time.Now().UTC()
	g := Build(in)
	// Every host silent: keep the last good picture instead of an empty one.
	ok := false
	for _, h := range g.Hosts {
		ok = ok || h.Up
	}
	if ok || s.last.Nodes == nil {
		s.last, s.at = g, time.Now()
	}
	return s.last
}
