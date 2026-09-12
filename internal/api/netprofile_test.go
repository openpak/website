package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

// The network profile is public, platform-scoped and ETag-cached (PRD
// network-profile-service-prd NP-0/NP-1). The CA digest it publishes must be
// the digest of the CA it serves — that is the whole trust bootstrap (NP-2).
func TestNetworkProfile(t *testing.T) {
	s, _, _ := newTestAPI(t)
	dir := t.TempDir()
	traefik := filepath.Join(dir, "dynamic.yml")
	if err := os.WriteFile(traefik, []byte(testRouting), 0o644); err != nil {
		t.Fatal(err)
	}
	pemBytes := testPEM(t)
	np, err := netprofile.New(netprofile.Options{
		TraefikFile: traefik,
		CA:          func(context.Context) ([]byte, error) { return pemBytes, nil },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Profiles = np

	// A known platform answers with that platform's families, narrowed to
	// what a Wii speaks: gamespy yes, Switch-era nintendo.net no.
	rec := do(t, s, http.MethodGet, "/api/v1/network/profile?platform=wii", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body.String())
	}
	var prof netprofile.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &prof); err != nil {
		t.Fatal(err)
	}
	if prof.Platform != "wii" || prof.Version == 0 || prof.GeneratedAt == 0 {
		t.Fatalf("identity fields: %+v", prof)
	}
	has := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}
	if !has(prof.Redirect.Suffixes, ".gamespy.com") {
		t.Fatalf("a Wii profile without .gamespy.com: %+v", prof.Redirect)
	}
	if has(prof.Redirect.Suffixes, ".nintendo.net") {
		t.Fatalf("a Wii profile must not carry Switch families: %+v", prof.Redirect)
	}
	if len(prof.Redirect.Never) == 0 {
		t.Fatal("the profile carries no never list")
	}
	if prof.CA == nil || prof.CA.URL == "" || prof.CA.SHA256 == "" || prof.CA.NotAfter == 0 {
		t.Fatalf("ca block incomplete: %+v", prof.CA)
	}

	// Omitted platform: the union, for diagnostics — every family, and the
	// routed name no family covers named exactly once.
	rec = do(t, s, http.MethodGet, "/api/v1/network/profile", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("union: %d %s", rec.Code, rec.Body.String())
	}
	var all netprofile.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if all.Platform != "all" || !has(all.Redirect.Suffixes, ".nintendo.net") {
		t.Fatalf("union: %+v", all)
	}

	// An unknown platform is a 400, never an empty profile.
	if rec := do(t, s, http.MethodGet, "/api/v1/network/profile?platform=gamecube", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown platform: %d", rec.Code)
	}

	// ETag: the same profile again is a 304 on If-None-Match (NP-1). The
	// second launch costs one conditional request and no body.
	tag := rec.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on the profile")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/profile", nil)
	req.Header.Set("If-None-Match", tag)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	rec304 := httptest.NewRecorder()
	mux.ServeHTTP(rec304, req)
	if rec304.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d, want 304", rec304.Code)
	}

	// A weak or listed validator is honoured too; a different one is not.
	for _, im := range []string{"W/" + tag, `"nope", ` + tag} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/network/profile", nil)
		req.Header.Set("If-None-Match", im)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Fatalf("If-None-Match %q: %d, want 304", im, rec.Code)
		}
	}

	// The served CA matches the digest the profile published: the client can
	// fetch the CA and check it against the profile before trusting it.
	recCA := do(t, s, http.MethodGet, "/api/v1/network/ca.pem", "", nil)
	if recCA.Code != http.StatusOK {
		t.Fatalf("ca: %d", recCA.Code)
	}
	if got, want := recCA.Header().Get("X-CA-Sha256"), prof.CA.SHA256; got != want {
		t.Fatalf("served CA digest %q != published %q", got, want)
	}
	if recCA.Body.String() != string(pemBytes) {
		t.Fatal("served CA is not the configured PEM")
	}

	// And the CA endpoint honours its own ETag.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/network/ca.pem", nil)
	req.Header.Set("If-None-Match", recCA.Header().Get("ETag"))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("ca If-None-Match: %d, want 304", rec.Code)
	}
}

// Without the profile service (no NETPROFILE_TRAEFIK_FILE) the endpoints say
// so plainly and nothing else on the site is affected.
func TestNetworkProfileDisabled(t *testing.T) {
	s, _, _ := newTestAPI(t)
	if rec := do(t, s, http.MethodGet, "/api/v1/network/profile?platform=switch", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled profile: %d", rec.Code)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/network/ca.pem", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled ca: %d", rec.Code)
	}
}

// Without a CA source the profile serves without a ca block and the CA
// endpoint is a 404 — a state question, not a server failure.
func TestNetworkProfileWithoutCA(t *testing.T) {
	s, _, _ := newTestAPI(t)
	traefik := filepath.Join(t.TempDir(), "dynamic.yml")
	if err := os.WriteFile(traefik, []byte(testRouting), 0o644); err != nil {
		t.Fatal(err)
	}
	np, err := netprofile.New(netprofile.Options{
		TraefikFile: traefik,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Profiles = np

	rec := do(t, s, http.MethodGet, "/api/v1/network/profile?platform=switch", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body.String())
	}
	var prof netprofile.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &prof); err != nil {
		t.Fatal(err)
	}
	if prof.CA != nil {
		t.Fatalf("ca block without a CA source: %+v", prof.CA)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/network/ca.pem", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("ca without a source: %d", rec.Code)
	}
}

// One TCP and one HTTP router: enough to prove the profile comes from the
// routing (the NPLN name claims switch families; the matchmaker needs exact).
const testRouting = `
tcp:
  routers:
    stardew:
      entryPoints: [console]
      rule: "HostSNI(` + "`t-9f607adf-lp1.lp1.t.npln.srv.nintendo.net`)" + `"
      service: stardew
      tls: { passthrough: true }
  services:
    stardew:
      loadBalancer:
        servers: [{ address: "openpak-stardew:21010" }]
http:
  routers:
    matchmaker:
      entryPoints: [console]
      rule: "Host(` + "`matchmaker.among.us`)" + `"
      service: among-us
      tls: {}
  services:
    among-us:
      loadBalancer:
        servers: [{ url: "http://openpak-among-us:22600" }]
`

func testPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "OpenPak Console CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
