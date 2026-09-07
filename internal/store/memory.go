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
	titles      []Title
	audit       []AuditEntry
	auditSeq    int64
	media       map[[2]string]MediaRecord
	webSessions map[string]WebSession
	nextToken   int
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
	rec.UpdatedAt = time.Now()
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

func (m *Memory) ListTitles(ctx context.Context) ([]Title, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Title(nil), m.titles...), nil
}

func (m *Memory) SeedTitles(ctx context.Context, titles []Title) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.titles) > 0 {
		return nil
	}
	m.titles = append(m.titles, titles...)
	return nil
}
