package store

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Memory is an in-process Store for tests and (optionally) ephemeral
// development runs. It implements the same contract as PG.
type Memory struct {
	mu          sync.Mutex
	cfg         SiteConfig
	tokens      map[string]APIToken // by hash
	requests    []Request
	votes       map[int64]map[string]bool // request id -> account id -> voted
	nextReqID   int64
	audit       []AuditEntry
	auditSeq    int64
	media       map[[2]string]MediaRecord
	webSessions map[string]WebSession
	nextToken   int
	pushTokens  map[string]PushToken // by token
	pushOwner   map[string]string    // token -> account
	pushCursor  uint64
}

// NewMemory returns an empty in-memory store with registrations open.
func NewMemory() *Memory {
	return &Memory{
		cfg:         SiteConfig{RegistrationsOpen: true},
		tokens:      map[string]APIToken{},
		media:       map[[2]string]MediaRecord{},
		webSessions: map[string]WebSession{},
	}
}

func (m *Memory) GetSiteConfig(ctx context.Context) (SiteConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, nil
}

func (m *Memory) SetSiteConfig(ctx context.Context, cfg SiteConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	return nil
}

func (m *Memory) CreateAPIToken(ctx context.Context, id, tokenHash, accountID, coreSessionID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[tokenHash] = APIToken{ID: id, AccountID: accountID, CoreSessionID: coreSessionID, CreatedAt: time.Now(), ExpiresAt: expiresAt}
	return nil
}

func (m *Memory) APITokenByHash(ctx context.Context, hash string) (APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok || t.RevokedAt != nil || time.Now().After(t.ExpiresAt) {
		return APIToken{}, ErrNotFound
	}
	return t, nil
}

func (m *Memory) RevokeAPIToken(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, t := range m.tokens {
		if t.ID == id {
			now := time.Now()
			t.RevokedAt = &now
			m.tokens[h] = t
		}
	}
	return nil
}

func (m *Memory) TouchAPIToken(ctx context.Context, id string) error { return nil }

func (m *Memory) PutWebSession(ctx context.Context, ws WebSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws.CreatedAt = time.Now()
	m.webSessions[ws.CoreSessionID] = ws
	return nil
}

func (m *Memory) GetWebSession(ctx context.Context, coreSessionID string) (WebSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws, ok := m.webSessions[coreSessionID]
	if !ok {
		return WebSession{}, ErrNotFound
	}
	return ws, nil
}

func (m *Memory) DeleteWebSession(ctx context.Context, coreSessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.webSessions, coreSessionID)
	return nil
}

func (m *Memory) DeleteWebSessionsForAccount(ctx context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, ws := range m.webSessions {
		if ws.AccountID == accountID {
			delete(m.webSessions, k)
		}
	}
	return nil
}

func (m *Memory) Ping(ctx context.Context) error { return nil }

func (m *Memory) RevokeAPITokensForAccount(ctx context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for h, t := range m.tokens {
		if t.AccountID == accountID {
			t.RevokedAt = &now
			m.tokens[h] = t
		}
	}
	return nil
}

func (m *Memory) AddAudit(ctx context.Context, actorID, action, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditSeq++
	m.audit = append(m.audit, AuditEntry{ID: m.auditSeq, ActorID: actorID, Action: action, Detail: detail, CreatedAt: time.Now()})
	return nil
}

func (m *Memory) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]AuditEntry(nil), m.audit...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) UpsertMedia(ctx context.Context, rec MediaRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Honour the caller's timestamp, as the SQL store does. Overwriting it
	// with now() made this double incapable of showing a stale row, which is
	// the only interesting thing that can go wrong here.
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now()
	}
	m.media[[2]string{rec.AccountID, rec.Kind}] = rec
	return nil
}

func (m *Memory) GetMedia(ctx context.Context, accountID, kind string) (MediaRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.media[[2]string{accountID, kind}]
	if !ok {
		return MediaRecord{}, ErrNotFound
	}
	return rec, nil
}

func (m *Memory) ListRequests(_ context.Context, kind, viewerAccountID string) ([]Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Request
	for _, r := range m.requests {
		if kind != "" && r.Kind != kind {
			continue
		}
		r.Votes = len(m.votes[r.ID])
		r.Voted = viewerAccountID != "" && m.votes[r.ID][viewerAccountID]
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// SeedRequests upserts by (kind, name), preserving ids and votes.
func (m *Memory) SeedRequests(_ context.Context, reqs []Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.votes == nil {
		m.votes = map[int64]map[string]bool{}
	}
	byKey := map[[2]string]int{}
	for i, r := range m.requests {
		byKey[[2]string{r.Kind, r.Name}] = i
	}
	for _, r := range reqs {
		key := [2]string{r.Kind, r.Name}
		if i, ok := byKey[key]; ok {
			id := m.requests[i].ID
			r.ID = id
			m.requests[i] = r
			continue
		}
		m.nextReqID++
		r.ID = m.nextReqID
		m.requests = append(m.requests, r)
		byKey[key] = len(m.requests) - 1
	}
	return nil
}

func (m *Memory) ToggleVote(_ context.Context, requestID int64, accountID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.votes == nil {
		m.votes = map[int64]map[string]bool{}
	}
	if m.votes[requestID] == nil {
		m.votes[requestID] = map[string]bool{}
	}
	if m.votes[requestID][accountID] {
		delete(m.votes[requestID], accountID)
		return false, nil
	}
	m.votes[requestID][accountID] = true
	return true, nil
}

func (m *Memory) SavePushToken(_ context.Context, accountID, token, platform string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pushTokens == nil {
		m.pushTokens, m.pushOwner = map[string]PushToken{}, map[string]string{}
	}
	m.pushTokens[token] = PushToken{Token: token, Platform: platform}
	m.pushOwner[token] = accountID
	return nil
}

func (m *Memory) DeletePushToken(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pushTokens, token)
	delete(m.pushOwner, token)
	return nil
}

func (m *Memory) PushTokensForAccount(_ context.Context, accountID string) ([]PushToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PushToken
	for tok, owner := range m.pushOwner {
		if owner == accountID {
			out = append(out, m.pushTokens[tok])
		}
	}
	return out, nil
}

func (m *Memory) PushCursor(_ context.Context) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pushCursor, nil
}

func (m *Memory) SetPushCursor(_ context.Context, v uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushCursor = v
	return nil
}
