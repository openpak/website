// Package pakkachat connects the website and the app to Pakka's chat service.
//
// The website authenticates a person first (cookie session or app bearer),
// then forwards the request to Pakka with the shared service key, the account
// id it resolved, and a short-lived grant. Pakka uses the grant to ask this
// package's tool endpoint for facts about that same person; the endpoint
// resolves the grant to an account itself, so a tool can never be pointed at
// anyone else. Pakka's address is private and it never sees a session id.
package pakkachat

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Principal is an authenticated person.
type Principal struct {
	AccountID     string
	CoreSessionID string
}

// Tools answers the account-scoped reads Pakka may make. Each takes the
// principal the grant resolved to; none takes an account argument.
type Tools interface {
	// Eligible reports whether the account may use Pakka at all. It must fail
	// closed: any doubt is false.
	Eligible(ctx context.Context, p Principal) bool
	Consoles(ctx context.Context, p Principal) (any, error)
	Friends(ctx context.Context, p Principal) (any, error)
	Rewards(ctx context.Context, p Principal) (any, error)
	Status(ctx context.Context) (any, error)
}

// Bridge proxies chat calls to Pakka and serves Pakka's tool reads.
type Bridge struct {
	URL   string // http://openpak-pakka:24800
	Key   string
	Tools Tools
	HTTP  *http.Client

	mu     sync.Mutex
	grants map[string]grant
}

type grant struct {
	p       Principal
	expires time.Time
}

// grantTTL covers one reply (Pakka gives up after 90 seconds).
const grantTTL = 3 * time.Minute

// New returns nil when Pakka is not configured, so every route is absent.
func New(url, key string, tools Tools) *Bridge {
	if url == "" || key == "" {
		return nil
	}
	return &Bridge{URL: strings.TrimRight(url, "/"), Key: key, Tools: tools,
		HTTP:   &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 100 * time.Second}},
		grants: map[string]grant{}}
}

func (b *Bridge) mint(p Principal) string {
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	g := "pg_" + base64.RawURLEncoding.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for k, v := range b.grants {
		if now.After(v.expires) {
			delete(b.grants, k)
		}
	}
	b.grants[g] = grant{p: p, expires: now.Add(grantTTL)}
	return g
}

// MintGrant mints a grant for p. Forward does this itself for every request;
// it is exported so tests of the tool endpoint can act as Pakka would.
func (b *Bridge) MintGrant(p Principal) string { return b.mint(p) }

func (b *Bridge) resolve(g string) (Principal, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.grants[g]
	if !ok || time.Now().After(v.expires) {
		delete(b.grants, g)
		return Principal{}, false
	}
	return v.p, true
}

func (b *Bridge) revoke(g string) {
	b.mu.Lock()
	delete(b.grants, g)
	b.mu.Unlock()
}

// Route is one chat call a client may make; the path is fixed here, never
// taken from the client.
type Route struct {
	Method, Pattern, Upstream string
	Mutates                   bool
}

// Routes lists the client-facing calls, relative to a prefix. {id} is the
// conversation id, validated before it is forwarded.
var Routes = []Route{
	{"GET", "/state", "/v1/chat/state", false},
	{"POST", "/consent", "/v1/chat/consent", true},
	{"DELETE", "/consent", "/v1/chat/consent", true},
	{"GET", "/conversations", "/v1/chat/conversations", false},
	{"POST", "/conversations", "/v1/chat/conversations", true},
	{"GET", "/conversations/{id}", "/v1/chat/conversations/{id}", false},
	{"DELETE", "/conversations/{id}", "/v1/chat/conversations/{id}", true},
	{"POST", "/conversations/{id}/messages", "/v1/chat/conversations/{id}/messages", true},
}

func validConvID(id string) bool {
	if len(id) != 24 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Forward sends one request to Pakka for p and streams the answer back.
func (b *Bridge) Forward(w http.ResponseWriter, r *http.Request, p Principal, rt Route) {
	up := rt.Upstream
	if strings.Contains(up, "{id}") {
		id := r.PathValue("id")
		if !validConvID(id) {
			http.Error(w, `{"error":"conversation not found","code":"not_found"}`, http.StatusNotFound)
			return
		}
		up = strings.ReplaceAll(up, "{id}", id)
	}
	var body io.Reader
	if r.Method == http.MethodPost {
		body = http.MaxBytesReader(w, r.Body, 64<<10)
	}
	g := b.mint(p)
	defer b.revoke(g)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, b.URL+up, body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.Key)
	req.Header.Set("X-Account-Id", p.AccountID)
	req.Header.Set("X-Pakka-Grant", g)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"Pakka is not answering","code":"unavailable"}`)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Cache-Control", "Retry-After"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	buf := make([]byte, 2048)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// ToolHandler serves GET /internal/pakka/tool/{name} to Pakka only: the
// service key must match, and the grant decides whose data is read.
func (b *Bridge) ToolHandler(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(b.Key)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	if name == "status" {
		b.answer(w, func() (any, error) { return b.Tools.Status(r.Context()) })
		return
	}
	p, ok := b.resolve(r.Header.Get("X-Pakka-Grant"))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	switch name {
	case "eligibility":
		b.answer(w, func() (any, error) { return map[string]bool{"eligible": b.Tools.Eligible(ctx, p)}, nil })
	case "consoles", "friends", "rewards":
		// Defense in depth: every account read re-checks eligibility.
		if !b.Tools.Eligible(ctx, p) {
			http.Error(w, "not available", http.StatusForbidden)
			return
		}
		b.answer(w, func() (any, error) {
			switch name {
			case "consoles":
				return b.Tools.Consoles(ctx, p)
			case "friends":
				return b.Tools.Friends(ctx, p)
			}
			return b.Tools.Rewards(ctx, p)
		})
	default:
		http.NotFound(w, r)
	}
}

func (b *Bridge) answer(w http.ResponseWriter, f func() (any, error)) {
	v, err := f()
	if err != nil {
		http.Error(w, "unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
