package achievements

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			w.WriteHeader(200)
		case r.Method == "DELETE" && r.Header.Get("X-Internal-Key") == "k":
			deleted = r.URL.Path
			w.WriteHeader(200)
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	if err := c.Healthy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.EraseAccount(context.Background(), "abc"); err != nil || deleted != "/internal/accounts/abc/achievements" {
		t.Fatalf("erase: %v %q", err, deleted)
	}
	if err := New(srv.URL, "wrong").EraseAccount(context.Background(), "abc"); err == nil {
		t.Fatal("a rejected key must fail the erasure step")
	}
	var none *Client
	if none.Healthy(context.Background()) == nil || none.EraseAccount(context.Background(), "a") == nil {
		t.Fatal("a nil client must report unavailable")
	}
}
