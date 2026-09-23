package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

// The internal ban lookup (docs/ban-lookup.md): status from the core, reason
// and actor from the website's record, by id or by an adapter link.
func TestInternalBanLookup(t *testing.T) {
	s, fc, db := newTestAPI(t)
	fc.status = map[string]string{"acct-bad": "banned", "acct-gone": "missing", "acct-b@x.test": "deletion-pending"}
	_ = db.RecordBan(context.Background(), store.Ban{AccountID: "acct-bad", Banned: true, Reason: "cheating", Actor: "acct-admin"})

	if rec := doRaw(t, s, http.MethodGet, "/internal/bans/acct-bad", "Bearer x", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key configured: %d", rec.Code)
	}
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	if rec := doRaw(t, s, http.MethodGet, "/internal/bans/acct-bad", "Bearer wrong", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}

	var got struct {
		AccountID, Status, Reason, Actor, Since string
		Banned                                  bool
	}
	rec := doRaw(t, s, http.MethodGet, "/internal/bans/acct-bad", "Bearer k", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || !got.Banned || got.Status != "banned" || got.Reason != "cheating" || got.Actor != "acct-admin" || got.Since == "" {
		t.Fatalf("banned: %d %s", rec.Code, rec.Body.String())
	}
	got = struct {
		AccountID, Status, Reason, Actor, Since string
		Banned                                  bool
	}{}
	rec = doRaw(t, s, http.MethodGet, "/internal/bans/acct-ok", "Bearer k", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.Banned || got.Status != "active" || got.Reason != "" {
		t.Fatalf("active: %s", rec.Body.String())
	}
	// By link, with the header the core's callers use; a deleting account is locked out too.
	req := doHeader(t, s, "/internal/bans?namespace=switch&subject=0123456789ab", "X-Internal-Key", "k")
	if req.Code != http.StatusOK || !json.Valid(req.Body.Bytes()) || !contains(req.Body.String(), `"banned":true`, `"status":"deletion-pending"`) {
		t.Fatalf("by subject: %d %s", req.Code, req.Body.String())
	}
	if rec := doRaw(t, s, http.MethodGet, "/internal/bans?namespace=switch&subject=nope", "Bearer k", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown subject: %d", rec.Code)
	}
	if rec := doRaw(t, s, http.MethodGet, "/internal/bans/acct-gone", "Bearer k", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown account: %d", rec.Code)
	}
	if rec := doRaw(t, s, http.MethodGet, "/internal/bans", "Bearer k", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("nothing asked: %d", rec.Code)
	}
}

// doHeader sends a GET with one arbitrary header.
func doHeader(t *testing.T, s *Server, target, name, value string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(name, value)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
