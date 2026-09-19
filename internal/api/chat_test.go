package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/chat"
)

// A block stops a message before it reaches the chat service, which only checks friendship
// and would otherwise deliver it: blocking does not end a friendship.
func TestChatSendRefusedWhenBlocked(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	sent := 0
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"message":{"id":1,"body":"hi"}}`))
	}))
	defer svc.Close()
	s.Chat = chat.New(svc.URL, "k")
	tok := signIn(t, s)
	fc.friends["acct-a@x.test"] = []string{"acct-b@x.test"}
	fc.blocks = [][2]string{{"acct-b@x.test", "acct-a@x.test"}}

	rec := do(t, s, http.MethodPost, "/api/v1/me/chat/acct-b@x.test", tok, map[string]string{"body": "hi"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error"`) || sent != 0 {
		t.Fatalf("blocked: %d %s, %d sent", rec.Code, rec.Body.String(), sent)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/chat/acct-c@x.test", tok, map[string]string{"body": "hi"}); rec.Code != http.StatusForbidden || sent != 0 {
		t.Fatalf("not a friend: %d, %d sent", rec.Code, sent)
	}
	fc.blocks = nil
	if rec := do(t, s, http.MethodPost, "/api/v1/me/chat/acct-b@x.test", tok, map[string]string{"body": "hi"}); rec.Code != http.StatusCreated || sent != 1 {
		t.Fatalf("unblocked: %d %s, %d sent", rec.Code, rec.Body.String(), sent)
	}
}
