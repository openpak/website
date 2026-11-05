// Package profilecache is the website's short read-through cache of the two
// account facts the header shows (display name, email). The account core is
// their only source of truth: nothing here is persisted.
package profilecache

import (
	"context"
	"sync"
	"time"
)

// TTL is how long a read is served without asking the core again.
const TTL = 60 * time.Second

// Profile is what the layout needs about the signed-in account.
type Profile struct {
	DisplayName string
	Email       string
}

// Fetch reads the profile behind a core session from the core.
type Fetch func(ctx context.Context, coreSessionID string) (Profile, error)

type entry struct {
	p  Profile
	at time.Time
}

// Cache keys by core session id. Safe for concurrent use.
type Cache struct {
	fetch Fetch
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	m     map[string]entry
}

// New builds a Cache over fetch with the standard TTL.
func New(fetch Fetch) *Cache {
	return &Cache{fetch: fetch, ttl: TTL, now: time.Now, m: map[string]entry{}}
}

// Get returns the fresh cached profile or reads the core. When the core fails
// it serves the last cached value, and with none degrades to an empty profile.
func (c *Cache) Get(ctx context.Context, coreSessionID string) Profile {
	if c == nil || c.fetch == nil || coreSessionID == "" {
		return Profile{}
	}
	c.mu.Lock()
	e, ok := c.m[coreSessionID]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < c.ttl {
		return e.p
	}
	p, err := c.fetch(ctx, coreSessionID)
	if err != nil {
		return e.p // zero value when nothing was cached
	}
	c.Put(coreSessionID, p)
	return p
}

// Put stores a profile just read from the core (login, profile edit).
func (c *Cache) Put(coreSessionID string, p Profile) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 4096 { // bound memory: drop expired entries
		for k, e := range c.m {
			if c.now().Sub(e.at) >= c.ttl {
				delete(c.m, k)
			}
		}
	}
	c.m[coreSessionID] = entry{p: p, at: c.now()}
}

// Invalidate forgets one session (logout, profile or email change).
func (c *Cache) Invalidate(coreSessionID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.m, coreSessionID)
	c.mu.Unlock()
}
