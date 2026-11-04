package pakkachat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type noTools struct{}

func (noTools) Eligible(context.Context, Principal) bool         { return true }
func (noTools) Consoles(context.Context, Principal) (any, error) { return nil, nil }
func (noTools) Friends(context.Context, Principal) (any, error)  { return nil, nil }
func (noTools) Rewards(context.Context, Principal) (any, error)  { return nil, nil }
func (noTools) Status(context.Context) (any, error)              { return map[string]string{"ok": "1"}, nil }

func TestGrantLifecycle(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := r.Header.Get("X-Pakka-Grant")
		io.WriteString(w, g)
	}))
	defer up.Close()
	b := New(up.URL, "k", noTools{})
	req := httptest.NewRequest("GET", "/x", nil)
	w := httptest.NewRecorder()
	b.Forward(w, req, Principal{AccountID: "a"}, Route{Method: "GET", Upstream: "/v1/chat/state"})
	g := w.Body.String()
	if g == "" {
		t.Fatal("no grant forwarded")
	}
	// The grant dies with the request that carried it.
	if _, ok := b.resolve(g); ok {
		t.Error("grant outlived its request")
	}
	// And dies on its own after the TTL.
	g2 := b.mint(Principal{AccountID: "a"})
	b.mu.Lock()
	v := b.grants[g2]
	v.expires = time.Now().Add(-time.Second)
	b.grants[g2] = v
	b.mu.Unlock()
	if _, ok := b.resolve(g2); ok {
		t.Error("expired grant resolved")
	}
}

func TestNewNeedsURLAndKey(t *testing.T) {
	if New("", "k", nil) != nil || New("http://x", "", nil) != nil {
		t.Error("bridge built without config")
	}
}

func TestEraseAccountCallsPakkaWithKeyAndAccount(t *testing.T) {
	var got struct{ method, path, auth, account string }
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.auth, got.account = r.Header.Get("Authorization"), r.Header.Get("X-Account-Id")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	b := New(srv.URL, "chat-key", nil)
	if err := b.EraseAccount(context.Background(), "acct-1"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodDelete || got.path != "/v1/chat/account" || got.auth != "Bearer chat-key" || got.account != "acct-1" {
		t.Errorf("%+v", got)
	}
	status = http.StatusBadGateway
	if err := b.EraseAccount(context.Background(), "acct-1"); err == nil {
		t.Error("a refused erase must fail the step so the account waits")
	}
	if err := (*Bridge)(nil).EraseAccount(context.Background(), "x"); err == nil {
		t.Error("an unconfigured bridge must not report success")
	}
}
