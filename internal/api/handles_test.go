package api

import (
	"context"
	"encoding/json"
	"github.com/openpak/website/internal/core"
	"net/http"
	"testing"
)

type handleIdentity struct {
	core.Identity
	called string
}

func (f *handleIdentity) CheckHandle(_ context.Context, name string) (core.HandleCheck, error) {
	f.called = name
	return core.HandleCheck{Valid: true, Available: false, Reason: "handle_taken"}, nil
}
func (f *handleIdentity) ResolveHandle(context.Context, string) (string, error) {
	return "", core.ErrNotFound
}
func TestPublicAvailabilityGETRoute(t *testing.T) {
	s, _, _ := newTestAPI(t)
	identity := &handleIdentity{Identity: s.Identity}
	s.Identity = identity
	rec := do(t, s, http.MethodGet, "/api/v1/handle/check?h=hansolo", "", nil)
	if rec.Code != http.StatusOK || identity.called != "hansolo" {
		t.Fatalf("GET availability: %d %s; queried %q", rec.Code, rec.Body.String(), identity.called)
	}
	var result core.HandleCheck
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Available || result.Reason != "handle_taken" {
		t.Fatalf("wrong core response: %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("availability must not be cached")
	}
}
