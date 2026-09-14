package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/ratelimit"
)

// AUTH-007: issuance is quota'd per client hash and requests per session,
// each answering 429 with a bounded hint. Artifact bytes are metered by the
// same shape, tested at the meter because the budget is 2 GiB.
func TestV2Quotas(t *testing.T) {
	t.Run("issuance quota is per hash and never leaks validity", func(t *testing.T) {
		f := newV2ServerFixture(t)
		f.replaceLimits(ratelimit.New(1, time.Hour), nil)

		body := `{"client_hash":"` + f.clientHash + `","platform":"switch","channel":"stable"}`
		code, _, _ := v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1", body)
		if code != http.StatusCreated {
			t.Fatalf("first issuance: %d", code)
		}
		// Second issuance for the same hash: quota, not authorization.
		code, _, body2 := v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1", body)
		if code != http.StatusTooManyRequests || body2["code"] != "rate_limited" {
			t.Fatalf("second issuance: %d %v", code, body2)
		}
		if hint, ok := body2["retry_after_seconds"].(float64); !ok || hint <= 0 {
			t.Fatalf("no bounded retry hint: %v", body2)
		}
		// An unknown hash gets its own bucket and the same generic 403 as
		// always — the quota never turned into an oracle.
		code, _, _ = v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1",
			`{"client_hash":"`+strings.Repeat("a", 64)+`","platform":"switch","channel":"stable"}`)
		if code != http.StatusForbidden {
			t.Fatalf("unknown hash under its own quota: %d", code)
		}
	})

	t.Run("requests per session", func(t *testing.T) {
		f := newV2ServerFixture(t)
		f.replaceLimits(nil, ratelimit.New(2, time.Minute))
		token, _ := openSession(t, f)
		for i := 0; i < 2; i++ {
			if code, _, _ := v2GET(t, f.ts, "/api/v2/network/clients/self", token, ""); code != http.StatusOK {
				t.Fatalf("request %d: %d", i+1, code)
			}
		}
		code, _, body := v2GET(t, f.ts, "/api/v2/network/clients/self", token, "")
		if code != http.StatusTooManyRequests || body["code"] != "rate_limited" {
			t.Fatalf("third request: %d %v", code, body)
		}
		// A second session has its own window.
		token2, _ := openSession(t, f)
		if code, _, _ := v2GET(t, f.ts, "/api/v2/network/clients/self", token2, ""); code != http.StatusOK {
			t.Fatalf("fresh session throttled by its predecessor: %d", code)
		}
	})

	t.Run("artifact byte budget is per session", func(t *testing.T) {
		m := newV2ByteMeter()
		if !m.take("s1", v2SessionByteBudget-1) {
			t.Fatal("budget refused its first byte")
		}
		if m.take("s1", 2) {
			t.Fatal("budget allowed one byte past the limit")
		}
		if !m.take("s2", v2SessionByteBudget) {
			t.Fatal("one session's usage leaked into another's")
		}
	})

	t.Run("a withdrawn bundle answers 410 under active quotas", func(t *testing.T) {
		f := newV2ServerFixture(t)
		f.replaceLimits(ratelimit.New(1000, time.Hour), ratelimit.New(1000, time.Minute))
		token, _ := openSession(t, f)
		if err := f.store.Withdraw(context.Background(), f.result.Revision.BundleSHA256); err != nil {
			t.Fatal(err)
		}
		code, _, _ := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{"bundle_sha256":"`+f.result.Revision.BundleSHA256+`"}`)
		if code != http.StatusGone {
			t.Fatalf("withdrawn pin: %d", code)
		}
	})
}
