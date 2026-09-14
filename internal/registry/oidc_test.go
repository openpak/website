package registry

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// jwksServer mints RS256 tokens the way GitHub Actions does and serves the
// matching key set, so the verifier's HTTP path runs for real.
type jwksServer struct {
	ts     *httptest.Server
	key    *rsa.PublicKey
	priv   *rsa.PrivateKey
	kid    string
	iss    string
	aud    string
	reqs   int
	badRes int
}

func newJWKS(t *testing.T) *jwksServer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	j := &jwksServer{priv: priv, key: &priv.PublicKey, kid: "test-key-1", iss: "placeholder", aud: "openpak-test"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		j.reqs++
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   j.iss,
			"jwks_uri": j.iss + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		if j.badRes > 0 {
			j.badRes--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		j.reqs++
		n := j.key.N.Bytes()
		e := big.NewInt(int64(j.key.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"alg": "RS256",
				"kid": j.kid,
				"n":   base64.RawURLEncoding.EncodeToString(n),
				"e":   base64.RawURLEncoding.EncodeToString(e),
			}},
		})
	})
	j.ts = httptest.NewServer(mux)
	j.iss = j.ts.URL
	t.Cleanup(j.ts.Close)
	return j
}

// mint signs one token with the given claims; overrides change what the
// token says without touching the key set.
func (j *jwksServer) mint(t *testing.T, now time.Time, overrides map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": j.kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{
		"iss":              j.iss,
		"aud":              j.aud,
		"exp":              now.Add(5 * time.Minute).Unix(),
		"iat":              now.Unix(),
		"sub":              "repo:openpak/citron:ref:refs/heads/main",
		"repository":       "openpak/citron",
		"repository_owner": "openpak",
		"job_workflow_ref": "openpak/citron/.github/workflows/release.yml@refs/heads/main",
		"ref":              "refs/heads/main",
		"sha":              "abc123",
		"run_id":           "123456",
		"environment":      "production",
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signingInput := encode(header) + "." + encode(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, j.priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + encode(sig)
}

func TestOIDCVerifierAcceptsARealToken(t *testing.T) {
	j := newJWKS(t)
	// The verifier is constructed with its own clock so expiry tests are
	// deterministic without sleeping.
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	v := NewOIDCVerifier(j.iss, j.aud, j.ts.Client(), func() time.Time { return now })
	assertion, err := v.VerifyAssertion(context.Background(), j.mint(t, now, nil))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if assertion.Repository != "openpak/citron" || assertion.Workflow != "openpak/citron/.github/workflows/release.yml" {
		t.Fatalf("claim mapping: %+v", assertion)
	}
	if assertion.Environment != "production" || assertion.Commit != "abc123" || assertion.Ref != "refs/heads/main" {
		t.Fatalf("claim mapping: %+v", assertion)
	}
	if assertion.Subject != "repo:openpak/citron:ref:refs/heads/main" {
		t.Fatalf("subject: %+v", assertion)
	}
}

func TestOIDCVerifierRejections(t *testing.T) {
	j := newJWKS(t)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := now
	v := NewOIDCVerifier(j.iss, j.aud, j.ts.Client(), func() time.Time { return clock })

	cases := []struct {
		name      string
		overrides map[string]any
	}{
		{"wrong audience", map[string]any{"aud": "someone-elses-audience"}},
		{"expired token", map[string]any{"exp": now.Add(-time.Minute).Unix()}},
		{"not yet valid", map[string]any{"nbf": now.Add(time.Hour).Unix()}},
		{"foreign issuer", map[string]any{"iss": "https://evil.example"}},
		{"missing repository", map[string]any{"repository": nil}},
		{"empty commit", map[string]any{"sha": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.VerifyAssertion(context.Background(), j.mint(t, now, tc.overrides)); err == nil {
				t.Fatal("accepted")
			}
		})
	}

	t.Run("forged signature", func(t *testing.T) {
		token := j.mint(t, now, nil)
		parts := strings.Split(token, ".")
		forged, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var claims map[string]any
		json.Unmarshal(forged, &claims)
		claims["repository"] = "openpak/someone-else"
		payload, _ := json.Marshal(claims)
		if _, err := v.VerifyAssertion(context.Background(),
			parts[0]+"."+base64.RawURLEncoding.EncodeToString(payload)+"."+parts[2]); err == nil {
			t.Fatal("a re-signed payload was accepted")
		}
	})
	t.Run("garbage", func(t *testing.T) {
		if _, err := v.VerifyAssertion(context.Background(), "not.a.token"); err == nil {
			t.Fatal("garbage accepted")
		}
		if _, err := v.VerifyAssertion(context.Background(), ""); err == nil {
			t.Fatal("empty accepted")
		}
	})
	t.Run("token minted for another key", func(t *testing.T) {
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		saved := j.priv
		j.priv = other
		defer func() { j.priv = saved }()
		if _, err := v.VerifyAssertion(context.Background(), j.mint(t, now, nil)); err == nil {
			t.Fatal("a foreign key's signature was accepted")
		}
	})
}

// Key rotation: a token signed by a new key is accepted after the forced
// refresh, because the key set is refetched when an unknown kid appears.
func TestOIDCVerifierFollowsKeyRotation(t *testing.T) {
	j := newJWKS(t)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := now
	v := NewOIDCVerifier(j.iss, j.aud, j.ts.Client(), func() time.Time { return clock })
	// Warm the cache with the old key.
	if _, err := v.VerifyAssertion(context.Background(), j.mint(t, now, nil)); err != nil {
		t.Fatal(err)
	}
	// The issuer rotates: new key, same kid namespace, new key id.
	newPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	j.priv = newPriv
	j.key = &newPriv.PublicKey
	j.kid = "test-key-2"
	clock = clock.Add(jwksCacheTTL * 2) // the cache is stale; refresh is allowed
	// Mint at the advanced clock: the token must be fresh when verified.
	if _, err := v.VerifyAssertion(context.Background(), j.mint(t, clock, nil)); err != nil {
		t.Fatalf("token under the rotated key rejected: %v", err)
	}
}

// The verified assertion must satisfy the same registration policy the
// static verifier faces: repository identity, approved workflow,
// environment.
func TestOIDCAssertionAgainstRegistration(t *testing.T) {
	j := newJWKS(t)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	v := NewOIDCVerifier(j.iss, j.aud, j.ts.Client(), func() time.Time { return now })
	client := Client{
		Repository:  "openpak/citron",
		Status:      ClientProductionApproved,
		Environment: EnvProduction,
		Workflows:   []string{"openpak/citron/.github/workflows/release.yml"},
	}
	assertion, err := v.VerifyAssertion(context.Background(), j.mint(t, now, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := assertion.Authorize(client); err != nil {
		t.Fatalf("honest workflow refused: %v", err)
	}
	// A fork minting its own token carries a different job_workflow_ref:
	// same filename upstream, different repository prefix, refused.
	fork := j.mint(t, now, map[string]any{
		"job_workflow_ref": "someone/citron-fork/.github/workflows/release.yml@refs/heads/main",
		"repository":       "someone/citron-fork",
	})
	a2, err := v.VerifyAssertion(context.Background(), fork)
	if err != nil {
		t.Fatal(err)
	}
	if err := a2.Authorize(client); err == nil {
		t.Fatal("a fork's workflow passed the approved-workflow check")
	}
	// Production registration, staging assertion: refused.
	staging := j.mint(t, now, map[string]any{"environment": "staging"})
	a3, err := v.VerifyAssertion(context.Background(), staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := a3.Authorize(client); err == nil {
		t.Fatal("a staging-environment assertion opened a production session")
	}
}
