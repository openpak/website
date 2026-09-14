package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// authenticate must exchange the CI identity for a build credential: the
// file the fetch step reads carries the registry's answer, never the
// identity the job started with.
func TestAuthenticateExchangesIdentityForBuildCredential(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/build-sessions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var buf [1024]byte
		n, _ := r.Body.Read(buf[:])
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session_id":"s1","build_token":"opb_issued","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "token")
	t.Setenv("OPENPAK_NETWORK_MODE", "development")
	t.Setenv("OPENPAK_REGISTRY_URL", srv.URL)
	t.Setenv("OPENPAK_CLIENT_HASH", "abc")
	t.Setenv("OPENPAK_PLATFORM", "switch")
	t.Setenv("OPENPAK_CHANNEL", "staging")
	if code := run([]string{"authenticate", "--token", "ci-identity", "--token-output", out}, os.Stdout, os.Stderr); code != 0 {
		t.Fatalf("authenticate exited %d", code)
	}
	if gotAuth != "Bearer ci-identity" {
		t.Fatalf("the exchange presented %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"client_hash":"abc"`) || !strings.Contains(gotBody, `"channel":"staging"`) {
		t.Fatalf("the exchange body was %q", gotBody)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(written)) != "opb_issued" {
		t.Fatalf("the credential file carries %q, want the exchanged credential", written)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode %v, want 0600", info.Mode().Perm())
	}

	// A registry refusal surfaces with its code and stays final.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"forbidden","message":"authorization failed"}`))
	}))
	defer srv2.Close()
	t.Setenv("OPENPAK_REGISTRY_URL", srv2.URL)
	var stderr strings.Builder
	if code := run([]string{"authenticate", "--token", "ci-identity", "--token-output", out}, os.Stdout, &stderr); code != 1 {
		t.Fatalf("refused exchange exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "403") {
		t.Fatalf("stderr missing the refusal code: %s", stderr.String())
	}
}
