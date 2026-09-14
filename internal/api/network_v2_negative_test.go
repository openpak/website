package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/registry"
)

// openSession runs the happy issuance for the fixture client and returns
// the build token and the session id the registry minted.
func openSession(t *testing.T, f *v2Fixture) (string, string) {
	t.Helper()
	code, _, body := v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1",
		`{"client_hash":"`+f.clientHash+`","platform":"switch","channel":"stable"}`)
	if code != http.StatusCreated {
		t.Fatalf("build-session: %d", code)
	}
	token, _ := body["build_token"].(string)
	sessionID, _ := body["session_id"].(string)
	if token == "" || sessionID == "" {
		t.Fatal("issuance response lacked token or session id")
	}
	return token, sessionID
}

// bareV2Server mounts the routes with no registry configured: the supported
// "REGISTRY_* not set" deployment.
func bareV2Server(t *testing.T) *httptest.Server {
	t.Helper()
	s := &Server{
		Auth: ratelimit.New(1000, time.Minute),
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// POL-M3: unauthorized direct requests return no bytes and no hints. Every
// case asserts the PRD error shape: code, message, request_id.
func TestV2Rejections(t *testing.T) {
	f := newV2ServerFixture(t)
	manifestSHA := f.result.Revision.ManifestSHA256
	bundleSHA := f.result.Revision.BundleSHA256
	shaOf := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}

	t.Run("build-session without a CI assertion", func(t *testing.T) {
		code, _, body := v2POST(t, f.ts, "/api/v2/network/build-sessions", "",
			`{"client_hash":"`+f.clientHash+`","platform":"switch","channel":"stable"}`)
		if code != http.StatusForbidden || body["code"] != "forbidden" {
			t.Fatalf("code %d body %v", code, body)
		}
		assertErrorShape(t, code, body)
	})
	t.Run("build-session with a copied CI token", func(t *testing.T) {
		code, _, _ := v2POST(t, f.ts, "/api/v2/network/build-sessions", "some-other-token",
			`{"client_hash":"`+f.clientHash+`","platform":"switch","channel":"stable"}`)
		if code != http.StatusForbidden {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("unknown client hash is indistinguishable from a denied one", func(t *testing.T) {
		code, _, body := v2POST(t, f.ts, "/api/v2/network/build-sessions", "ci-token-1",
			`{"client_hash":"`+shaOf("nobody")+`","platform":"switch","channel":"stable"}`)
		if code != http.StatusForbidden || body["code"] != "forbidden" {
			t.Fatalf("code %d body %v", code, body)
		}
	})
	t.Run("an approved CI cannot resolve a foreign or unknown digest", func(t *testing.T) {
		token, _ := openSession(t, f)
		code, _, body := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{"bundle_sha256":"`+strings.Repeat("a", 64)+`"}`)
		if code != http.StatusNotFound {
			t.Fatalf("foreign digest: %d", code)
		}
		assertErrorShape(t, code, body)
	})
	t.Run("a digest is never substituted with the channel head", func(t *testing.T) {
		token, _ := openSession(t, f)
		if code, _, _ := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{"bundle_sha256":"`+bundleSHA+`"}`); code != http.StatusOK {
			t.Fatalf("the promoted digest itself must resolve: %d", code)
		}
		if code, _, _ := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{"bundle_sha256":"`+strings.Repeat("b", 64)+`"}`); code != http.StatusNotFound {
			t.Fatalf("unknown digest: %d", code)
		}
	})
	t.Run("artifacts need a resolution first", func(t *testing.T) {
		token, _ := openSession(t, f)
		if code, _, _ := v2GET(t, f.ts, "/api/v2/network/artifacts/"+bundleSHA, token, ""); code != http.StatusForbidden {
			t.Fatalf("artifact before resolve: %d", code)
		}
		if code, _, _ := v2GET(t, f.ts, "/api/v2/network/bundles/"+manifestSHA, token, ""); code != http.StatusForbidden {
			t.Fatalf("bundle before resolve: %d", code)
		}
		if code, _, _ := v2GET(t, f.ts, "/api/v2/network/metadata/targets.json", token, ""); code != http.StatusForbidden {
			t.Fatalf("metadata before resolve: %d", code)
		}
	})
	t.Run("an artifact outside the resolved manifest does not exist", func(t *testing.T) {
		token, _ := openSession(t, f)
		if code, _, _ := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{}`); code != http.StatusOK {
			t.Fatalf("resolve: %d", code)
		}
		code, _, _ := v2GET(t, f.ts, "/api/v2/network/artifacts/"+shaOf("not-in-the-manifest"), token, "")
		if code != http.StatusForbidden {
			t.Fatalf("foreign artifact digest: %d", code)
		}
	})
	t.Run("a revoked session dies within one request", func(t *testing.T) {
		token, sessionID := openSession(t, f)
		if code, _, _ := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{}`); code != http.StatusOK {
			t.Fatalf("resolve: %d", code)
		}
		if err := f.store.RevokeBuildSession(context.Background(), sessionID); err != nil {
			t.Fatal(err)
		}
		code, _, body := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{}`)
		if code != http.StatusUnauthorized || body["code"] != "unauthorized" {
			t.Fatalf("revoked session: %d %v", code, body)
		}
	})
	t.Run("withdrawn bundles answer 410", func(t *testing.T) {
		if err := f.store.Withdraw(context.Background(), bundleSHA); err != nil {
			t.Fatal(err)
		}
		token, _ := openSession(t, f)
		code, _, body := v2POST(t, f.ts, "/api/v2/network/resolve", token, `{"bundle_sha256":"`+bundleSHA+`"}`)
		if code != http.StatusGone || body["code"] != "gone" {
			t.Fatalf("withdrawn digest: %d %v", code, body)
		}
	})
	t.Run("an expired session dies", func(t *testing.T) {
		f2 := newV2ServerFixture(t)
		token, _ := openSession(t, f2)
		f2.svc.Now = func() time.Time { return time.Now().Add(registry.BuildSessionTTL + time.Minute) }
		if code, _, _ := v2POST(t, f2.ts, "/api/v2/network/resolve", token, `{}`); code != http.StatusUnauthorized {
			t.Fatalf("expired session: %d", code)
		}
	})
}

func assertErrorShape(t *testing.T, code int, body map[string]any) {
	t.Helper()
	for _, key := range []string{"code", "message", "request_id"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("status %d error lacks %q: %v", code, key, body)
		}
	}
}

// TestV2Unconfigured pins the 503 behavior when the REGISTRY_* variables are
// absent: the routes exist, nothing else works, the rest of the site does.
func TestV2Unconfigured(t *testing.T) {
	ts := bareV2Server(t)
	code, _, body := v2POST(t, ts, "/api/v2/network/build-sessions", "", `{}`)
	if code != http.StatusServiceUnavailable || body["code"] != "unavailable" {
		t.Fatalf("unconfigured build-sessions: %d %v", code, body)
	}
	code, _, body = v2GET(t, ts, "/api/v2/network/clients/self", "", "")
	if code != http.StatusServiceUnavailable || body["code"] != "unavailable" {
		t.Fatalf("unconfigured clients/self: %d %v", code, body)
	}
}
