package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProfileTextPreservesOmittedFieldsAndSendsClears(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("openpak_session")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/profile" || err != nil || cookie.Value != "session-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProfile(t.Context(), "session-1", "IE", "1990-01-01", "Player"); err != nil {
		t.Fatal(err)
	}
	if _, exists := received["bio"]; exists {
		t.Fatal("older update sent a bio it did not edit")
	}
	bio, motto := "", "Play together"
	if err := c.UpdateProfile(t.Context(), "session-1", "IE", "1990-01-01", "Player", ProfileText{Bio: &bio, Motto: &motto}); err != nil {
		t.Fatal(err)
	}
	if got, exists := received["bio"]; !exists || got != "" || received["motto"] != motto {
		t.Fatalf("text and explicit clear lost: %#v", received)
	}
}
