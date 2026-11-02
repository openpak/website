package supervision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNilClientIsNotConfigured(t *testing.T) {
	var c *Client
	if c.SupervisedOrUnknown(context.Background(), "x") {
		t.Fatal("an unconfigured client must not block")
	}
}

func TestUnreachableCoreCountsAsSupervised(t *testing.T) {
	c, err := New("127.0.0.1:1", "", "", "k")
	if err != nil {
		t.Fatal(err)
	}
	if !c.SupervisedOrUnknown(context.Background(), "00000000-0000-4000-8000-000000000001") {
		t.Fatal("an unreachable core must count as supervised")
	}
}

func TestSetLevelKeepsOtherKeysAndVersion(t *testing.T) {
	var put map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "web" || r.Header.Get("Authorization") != "Bearer sess" {
			http.Error(w, "no", 401)
			return
		}
		if r.Method == "GET" {
			io.WriteString(w, `{"profile":{"rating_age":12},"version":4}`)
			return
		}
		json.NewDecoder(r.Body).Decode(&put)
		io.WriteString(w, `{"version":5}`)
	}))
	defer srv.Close()
	c, _ := New("127.0.0.1:1", srv.URL, "web", "k")
	if err := c.SetOpenVerseLevel(context.Background(), "sess", "m", LevelRead); err != nil {
		t.Fatal(err)
	}
	p := put["profile"].(map[string]any)
	if p["openverse"] != "read" || p["rating_age"] != float64(12) || put["version"] != float64(4) {
		t.Fatalf("sent %v", put)
	}
	if err := c.SetOpenVerseLevel(context.Background(), "sess", "m", "yes"); err == nil {
		t.Fatal("bad level accepted")
	}
}
