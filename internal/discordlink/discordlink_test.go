package discordlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func fake(t *testing.T, id string) (*Client, *int32) {
	var revoked int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostFormValue("code") != "good" || r.PostFormValue("client_secret") != "sec" || r.PostFormValue("redirect_uri") != "https://x/cb" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		w.Write([]byte(`{"access_token":"tok","scope":"identify"}`))
	})
	mux.HandleFunc("GET /users/@me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		w.Write([]byte(`{"id":"` + id + `","username":"someone","global_name":"Some One"}`))
	})
	mux.HandleFunc("POST /oauth2/token/revoke", func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&revoked, 1) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New("cid", "sec")
	c.APIBase = srv.URL
	return c, &revoked
}

func TestAuthURLIsIdentifyOnly(t *testing.T) {
	u, _ := url.Parse(New("cid", "sec").AuthURL("https://x/cb", "st"))
	q := u.Query()
	if q.Get("scope") != "identify" || q.Get("client_id") != "cid" || q.Get("state") != "st" || q.Get("redirect_uri") != "https://x/cb" || q.Get("response_type") != "code" {
		t.Fatalf("bad url %s", u)
	}
	if strings.Contains(u.String(), "secret") {
		t.Fatal("secret in URL")
	}
}

func TestIdentify(t *testing.T) {
	c, revoked := fake(t, "123456789012345678")
	u, err := c.Identify(context.Background(), "good", "https://x/cb")
	if err != nil || u.ID != "123456789012345678" || u.Username != "someone" {
		t.Fatalf("%v %+v", err, u)
	}
	if atomic.LoadInt32(revoked) != 1 {
		t.Fatal("token not revoked")
	}
	if _, err := c.Identify(context.Background(), "bad", "https://x/cb"); err == nil {
		t.Fatal("bad code accepted")
	}
}

func TestIdentifyRejectsOddID(t *testing.T) {
	c, _ := fake(t, "not-a-number")
	if _, err := c.Identify(context.Background(), "good", "https://x/cb"); err == nil {
		t.Fatal("non-snowflake accepted")
	}
}

func TestFromEnvUnset(t *testing.T) {
	t.Setenv("DISCORD_OAUTH_CLIENT_ID", "")
	if FromEnv() != nil {
		t.Fatal("want nil")
	}
}
