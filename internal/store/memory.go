package store

import (
	"context"
	"slices"
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
	changes     []RequestChange
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

// SeedRequests is a test fixture: it adds rows as given, without the change log.
func (m *Memory) SeedRequests(_ context.Context, reqs []Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range reqs {
		m.nextReqID++
		r.ID, r.DateAdded, r.LastUpdated = m.nextReqID, time.Now(), time.Now()
		m.requests = append(m.requests, r)
	}
	return nil
}

func (m *Memory) indexOf(id int64) int {
	return slices.IndexFunc(m.requests, func(r Request) bool { return r.ID == id })
}

func (m *Memory) taken(r Request, except int64) bool {
	return slices.ContainsFunc(m.requests, func(o Request) bool {
		return o.ID != except && (o.Kind == r.Kind && o.Name == r.Name && o.Console == r.Console ||
			r.TitleID != "" && o.TitleID == r.TitleID)
	})
}

func (m *Memory) log(id int64, actor, action string, ch map[string]FieldChange) {
	m.changes = append(m.changes, RequestChange{RequestID: id, Actor: actor, Action: action, At: time.Now(), Changes: ch})
}

// RequestChanges is the change log of one request, oldest first (tests read it).
func (m *Memory) RequestChanges(id int64) []RequestChange {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RequestChange
	for _, c := range m.changes {
		if c.RequestID == id {
			out = append(out, c)
		}
	}
	return out
}

func (m *Memory) CreateRequest(_ context.Context, r Request, actor string) (Request, error) {
	r, err := normalize(r)
	if err != nil {
		return r, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.taken(r, 0) {
		return r, ErrDuplicate
	}
	if r.Sort < 0 {
		r.Sort = 0
		for _, o := range m.requests {
			r.Sort = max(r.Sort, o.Sort+1)
		}
	}
	m.nextReqID++
	r.ID, r.DateAdded, r.LastUpdated, r.Votes, r.Voted = m.nextReqID, time.Now(), time.Now(), 0, false
	m.requests = append(m.requests, r)
	m.log(r.ID, actor, "create", diff(nil, fields(r)))
	return r, nil
}

func (m *Memory) UpdateRequest(_ context.Context, id int64, p RequestPatch, actor string) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.indexOf(id)
	if i < 0 {
		return Request{}, ErrNotFound
	}
	old := m.requests[i]
	r, err := normalize(p.apply(old))
	if err != nil {
		return old, err
	}
	ch := diff(fields(old), fields(r))
	if len(ch) == 0 {
		return old, nil
	}
	if m.taken(r, id) {
		return old, ErrDuplicate
	}
	r.LastUpdated = time.Now()
	m.requests[i] = r
	m.log(id, actor, "update", ch)
	if d := statusAudit(id, old, r); d != "" {
		m.auditSeq++
		m.audit = append(m.audit, AuditEntry{ID: m.auditSeq, ActorID: actor, Action: "title.status", Detail: d, CreatedAt: time.Now()})
	}
	return r, nil
}

func (m *Memory) DeleteRequest(_ context.Context, id int64, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	if len(m.votes[id]) > 0 {
		return ErrHasVotes
	}
	m.log(id, actor, "delete", diff(fields(m.requests[i]), nil))
	m.requests = slices.Delete(m.requests, i, i+1)
	return nil
}

func (m *Memory) ToggleVote(_ context.Context, requestID int64, accountID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *Request
	for i := range m.requests {
		if m.requests[i].ID == requestID {
			found = &m.requests[i]
			break
		}
	}
	if found == nil {
		return false, ErrNotFound
	}
	if !found.Votable() {
		return false, ErrNotVotable
	}
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

func (m *Memory) RequestVotes(_ context.Context, requestID int64, accountID string) (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.votes[requestID]), m.votes[requestID][accountID], nil
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
