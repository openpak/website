package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/consolesync"
)

func TestAppSignInAndConsolePassword(t *testing.T) {
	s, _, _ := newTestAPI(t)
	var mu sync.Mutex
	var calls []map[string]string
	nn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"nnid": "playera", "pid": 1, "mode": "", "min": 6, "max": 16})
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		calls = append(calls, in)
		mu.Unlock()
		if in["password"] == "tooLongToTypeOnAWiiU" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"error":"password_length"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": in["mode"], "nnid": "playera"})
	}))
	defer nn.Close()
	s.NNAdapter = adapter.New(nn.URL, "k")
	w := consolesync.New(s.Core, s.NNAdapter, s.log).(*consolesync.HTTP)
	s.Core = w

	tok := signIn(t, s) // the app's token login carries the plain password
	w.Wait.Wait()
	mu.Lock()
	if len(calls) != 1 || calls[0]["mode"] != "sync" || calls[0]["password"] != "password-A-1" {
		t.Fatalf("app sign-in did not sync: %v", calls)
	}
	mu.Unlock()

	if rec := do(t, s, http.MethodGet, "/api/v1/me/wiiu/console-password", tok, nil); rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	body := func(cur, console string) map[string]string {
		return map[string]string{"current_password": cur, "console_password": console}
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/wiiu/console-password", tok, body("wrong-wrong-1", "consolePass9")); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong current password: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/wiiu/console-password", tok, body("password-A-1", "tooLongToTypeOnAWiiU")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rules: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/wiiu/console-password", tok, body("password-A-1", "consolePass9")); rec.Code != 200 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/wiiu/console-password", "", body("password-A-1", "consolePass9")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", rec.Code)
	}
}
