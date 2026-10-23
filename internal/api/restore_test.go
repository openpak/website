package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A closed account's right password answers 409 account_deletion_pending with
// the erasure date and no bearer; /token/restore reopens it and mints one.
func TestTokenOffersRestore(t *testing.T) {
	s, fc, _ := newTestAPI(t)
	fc.closed = map[string]bool{"a@x.test": true}

	rec := do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "wrong-password"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/api/v1/token", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusConflict || out["code"] != "account_deletion_pending" || out["erase_after"] != "2026-11-22T12:00:00Z" || out["token"] != nil {
		t.Fatalf("closed account: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodPost, "/api/v1/token/restore", "", tokenRequest{Email: "a@x.test", Password: "password-A-1"})
	out = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated || out["token"] == nil || fc.closed["a@x.test"] {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
}
