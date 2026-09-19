package registryhttp

import (
	"context"
	"net/http"
	"testing"
)

// The admin status route fails closed: no key configured means no route,
// a wrong key is refused, and the right key reads the registry in the shape
// the website's admin page is built against.
func TestInternalStatus(t *testing.T) {
	f := newV2ServerFixture(t)

	if code, _, _ := v2GET(t, f.ts, "/internal/status", "", ""); code != http.StatusNotFound {
		t.Fatalf("unset admin key: %d", code)
	}

	f.srv.AdminKey = "admin-key-1"
	if code, _, _ := v2GET(t, f.ts, "/internal/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", code)
	}
	if code, _, _ := v2GET(t, f.ts, "/internal/status", "admin-key-2", ""); code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: %d", code)
	}

	if err := f.store.Withdraw(context.Background(), f.result.Revision.BundleSHA256); err != nil {
		t.Fatal(err)
	}
	code, _, body := v2GET(t, f.ts, "/internal/status", "admin-key-1", "")
	if code != http.StatusOK {
		t.Fatalf("status: %d %v", code, body)
	}

	channels, _ := body["channels"].([]any)
	if len(channels) != 1 {
		t.Fatalf("channels: %v", body["channels"])
	}
	ch := channels[0].(map[string]any)
	if ch["name"] != "switch/stable" || ch["revision"] != "switch/stable/1" || ch["sequence"] != float64(1) {
		t.Fatalf("channel: %v", ch)
	}
	if _, ok := ch["updated_at"].(string); !ok {
		t.Fatalf("channel updated_at: %v", ch)
	}

	revisions, _ := body["revisions"].([]any)
	if len(revisions) != 1 {
		t.Fatalf("revisions: %v", body["revisions"])
	}
	rev := revisions[0].(map[string]any)
	if rev["id"] != "switch/stable/1" || rev["policy_id"] != "openpak-production" {
		t.Fatalf("revision: %v", rev)
	}
	if _, ok := rev["created_at"].(string); !ok {
		t.Fatalf("revision created_at: %v", rev)
	}
	if _, ok := rev["withdrawn_at"].(string); !ok {
		t.Fatalf("withdrawn revision lacks withdrawn_at: %v", rev)
	}

	clients, _ := body["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients: %v", body["clients"])
	}
	cl := clients[0].(map[string]any)
	if cl["id"] != "client-1" || cl["name"] != "openpak/citron" || cl["status"] != "production-approved" {
		t.Fatalf("client: %v", cl)
	}
}
