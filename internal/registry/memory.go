package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// MemStore is the in-memory Store: tests, CI and local development. It obeys
// the same rules as the PG store — transactional sequence allocation,
// immutability, revocation visibility — with data lost on Close.
type MemStore struct {
	mu        sync.Mutex
	clients   map[string]Client       // by hash
	clientIDs map[string]string       // id → hash
	sessions  map[string]BuildSession // by session id
	byDigest  map[string]string       // token digest → session id
	// resolved maps session id → manifest digest the session resolved.
	resolved  map[string]string
	revs      map[string]Revision // platform|channel|seq → revision
	byBundle  map[string]Revision // platform|channel|bundleSha → latest revision
	sequences map[string]int64    // platform|channel → last allocated
	channels  map[string]ChannelState
	now       func() time.Time
}

// NewMemStore builds an empty store.
func NewMemStore(now func() time.Time) *MemStore {
	if now == nil {
		now = time.Now
	}
	return &MemStore{
		clients:   map[string]Client{},
		clientIDs: map[string]string{},
		sessions:  map[string]BuildSession{},
		byDigest:  map[string]string{},
		resolved:  map[string]string{},
		revs:      map[string]Revision{},
		byBundle:  map[string]Revision{},
		sequences: map[string]int64{},
		channels:  map[string]ChannelState{},
		now:       now,
	}
}

func revKey(platform, channel string, sequence int64) string {
	return platform + "|" + channel + "|" + itoa(sequence)
}

func digestKey(platform, channel, sha string) string {
	return platform + "|" + channel + "|" + sha
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (m *MemStore) PutClient(_ context.Context, c Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = m.now()
	}
	c.UpdatedAt = m.now()
	m.clients[c.ClientHash] = c
	m.clientIDs[c.ID] = c.ClientHash
	return nil
}

func (m *MemStore) ClientByHash(_ context.Context, clientHash string) (Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[clientHash]
	if !ok {
		return Client{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) SetClientStatus(_ context.Context, clientID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash, ok := m.clientIDs[clientID]
	if !ok {
		return ErrNotFound
	}
	c := m.clients[hash]
	c.Status = status
	switch status {
	case ClientSuspended, ClientRevoked:
		c.RevocationEpoch++
	}
	c.UpdatedAt = m.now()
	m.clients[hash] = c
	return nil
}

func (m *MemStore) CreateBuildSession(_ context.Context, s BuildSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	m.byDigest[s.Digest] = s.ID
	return nil
}

func (m *MemStore) BuildSessionByDigest(_ context.Context, tokenDigest string) (BuildSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byDigest[tokenDigest]
	if !ok {
		return BuildSession{}, ErrNotFound
	}
	return m.sessions[id], nil
}

func (m *MemStore) RevokeBuildSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	t := m.now()
	s.RevokedAt = &t
	m.sessions[sessionID] = s
	return nil
}

func (m *MemStore) MarkSessionResolved(_ context.Context, sessionID, manifestSHA256 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return ErrNotFound
	}
	m.resolved[sessionID] = manifestSHA256
	return nil
}

func (m *MemStore) SessionResolution(_ context.Context, sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resolved[sessionID]
	if !ok {
		return "", ErrNotFound
	}
	return r, nil
}

func (m *MemStore) NextSequence(_ context.Context, platform, channel string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := platform + "|" + channel
	m.sequences[key]++
	return m.sequences[key], nil
}

func (m *MemStore) InsertRevision(_ context.Context, r Revision) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A bundle once withdrawn never comes back, not even as a "new"
	// revision of the same bytes.
	key := digestKey(r.Platform, r.Channel, r.BundleSHA256)
	if prev, ok := m.byBundle[key]; ok && prev.WithdrawnAt != nil {
		return 0, ErrWithdrawn
	}
	if r.Sequence < 1 {
		return 0, ErrConflict
	}
	k := revKey(r.Platform, r.Channel, r.Sequence)
	if _, exists := m.revs[k]; exists {
		return 0, ErrConflict
	}
	if r.IssuedAt.IsZero() {
		r.IssuedAt = m.now()
	}
	m.revs[k] = r
	m.byBundle[key] = r
	return r.Sequence, nil
}

func (m *MemStore) RevisionByBundleDigest(_ context.Context, platform, channel, bundleSHA256 string) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byBundle[digestKey(platform, channel, bundleSHA256)]
	if !ok {
		return Revision{}, ErrNotFound
	}
	return r, nil
}

func (m *MemStore) RevisionByManifestDigest(_ context.Context, platform, channel, manifestSHA256 string) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.revs {
		rev := m.revs[k]
		if rev.Platform == platform && rev.Channel == channel && rev.ManifestSHA256 == manifestSHA256 {
			return rev, nil
		}
	}
	return Revision{}, ErrNotFound
}

func (m *MemStore) Revision(_ context.Context, platform, channel string, sequence int64) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.revs[revKey(platform, channel, sequence)]
	if !ok {
		return Revision{}, ErrNotFound
	}
	return r, nil
}

func (m *MemStore) Promote(_ context.Context, platform, channel, bundleSHA256 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byBundle[digestKey(platform, channel, bundleSHA256)]
	if !ok {
		return ErrNotFound
	}
	if r.WithdrawnAt != nil {
		return ErrWithdrawn
	}
	m.channels[platform+"|"+channel] = ChannelState{
		Platform: platform, Channel: channel,
		BundleSHA256: bundleSHA256, Sequence: r.Sequence,
		UpdatedAt: m.now(),
	}
	return nil
}

func (m *MemStore) Channel(_ context.Context, platform, channel string) (ChannelState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[platform+"|"+channel]
	if !ok {
		return ChannelState{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) Withdraw(_ context.Context, bundleSHA256 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	t := m.now()
	for k, r := range m.revs {
		if r.BundleSHA256 == bundleSHA256 {
			r.WithdrawnAt = &t
			m.revs[k] = r
			m.byBundle[digestKey(r.Platform, r.Channel, r.BundleSHA256)] = r
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) ListChannels(_ context.Context) ([]ChannelState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChannelState, 0, len(m.channels))
	for _, c := range m.channels {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].Channel < out[j].Channel
	})
	return out, nil
}

func (m *MemStore) ListRevisions(_ context.Context, limit int) ([]Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Revision, 0, len(m.revs))
	for _, r := range m.revs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].IssuedAt.Equal(out[j].IssuedAt) {
			return out[i].IssuedAt.After(out[j].IssuedAt)
		}
		return out[i].Sequence > out[j].Sequence
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListClients(_ context.Context) ([]Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Client, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Close releases the store; memory contents are gone afterwards.
func (m *MemStore) Close() error { return nil }

// TokenDigest is the lookup key for a build session bearer: the store never
// sees the raw token, only its SHA-256 (AUTH-008: no bearer in storage or
// logs).
func TokenDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
