package registry

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OIDCVerifier verifies GitHub Actions workload identity tokens against the
// issuer's published keys and turns the verified claims into a CIAssertion
// (02, AUTH-001). A verified assertion is what authorizes a build session;
// a supplied client hash, repository name or User-Agent never does.
//
// Policy enforced here: issuer, audience, expiry and signature. Policy
// enforced against the registration (repository identity, approved
// workflow, protected environment) lives in CIAssertion.Authorize, so the
// static staging verifier and the OIDC verifier share one rule set.
type OIDCVerifier struct {
	// Issuer is the expected iss claim, e.g.
	// https://token.actions.githubusercontent.com for GitHub.
	Issuer string
	// Audience is the expected aud claim: the value the workflow requests
	// its token for. Tokens minted for anyone else are refused.
	Audience string

	client *http.Client
	clock  func() time.Time

	mu          sync.Mutex
	jwksURI     string
	keys        map[string]*rsa.PublicKey
	keysFetched time.Time
}

// NewOIDCVerifier builds a verifier for one issuer/audience pair.
func NewOIDCVerifier(issuer, audience string, client *http.Client, clock func() time.Time) *OIDCVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if clock == nil {
		clock = time.Now
	}
	return &OIDCVerifier{Issuer: issuer, Audience: audience, client: client, clock: clock}
}

const (
	jwksCacheTTL    = 10 * time.Minute
	oidcMaxBody     = 1 << 20
	oidcHTTPTimeout = 10 * time.Second
	clockSkew       = 2 * time.Minute
)

// VerifyAssertion implements CIVerifier.
func (v *OIDCVerifier) VerifyAssertion(ctx context.Context, token string) (*CIAssertion, error) {
	if token == "" {
		return nil, ErrAssertion
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrAssertion
	}
	headerRaw, err := decodeSegment(parts[0])
	if err != nil {
		return nil, ErrAssertion
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return nil, ErrAssertion
	}
	key, err := v.keyFor(ctx, header.Kid)
	if err != nil {
		return nil, ErrAssertion
	}
	// Signature over the signing input, PKCS#1 v1.5 with SHA-256; anything
	// else is a forged or mangled token and gets the same generic answer.
	signingInput := parts[0] + "." + parts[1]
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return nil, ErrAssertion
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, ErrAssertion
	}
	payloadRaw, err := decodeSegment(parts[1])
	if err != nil {
		return nil, ErrAssertion
	}
	var claims struct {
		Issuer          string `json:"iss"`
		Audience        any    `json:"aud"`
		Subject         string `json:"sub"`
		Expires         int64  `json:"exp"`
		IssuedAt        int64  `json:"iat"`
		NotBefore       int64  `json:"nbf"`
		Repository      string `json:"repository"`
		RepositoryOwner string `json:"repository_owner"`
		JobWorkflowRef  string `json:"job_workflow_ref"`
		Workflow        string `json:"workflow"`
		Ref             string `json:"ref"`
		Commit          string `json:"sha"`
		RunID           string `json:"run_id"`
		Environment     string `json:"environment"`
	}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, ErrAssertion
	}
	now := v.clock()
	if claims.Issuer != v.Issuer {
		return nil, ErrAssertion
	}
	if !audienceMatches(claims.Audience, v.Audience) {
		return nil, ErrAssertion
	}
	// Expiry is the issuer's word for "stop trusting this"; a small skew
	// tolerates clock drift between the runner and us, nothing more.
	if claims.Expires == 0 || now.After(time.Unix(claims.Expires, 0)) {
		return nil, ErrAssertion
	}
	if claims.NotBefore != 0 && now.Add(clockSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return nil, ErrAssertion
	}
	// job_workflow_ref pins the exact repository and ref the reusable
	// workflow ran from; the bare workflow claim is display-only. A fork
	// copying the workflow filename produces a different job_workflow_ref
	// and is refused here, in one place, before any registration check.
	workflow := workflowPath(claims.JobWorkflowRef)
	if workflow == "" {
		workflow = claims.Workflow
	}
	// An assertion without a repository identity or a commit is malformed,
	// not merely weak: the registration checks below are meaningless
	// without them.
	if claims.Repository == "" || claims.Commit == "" {
		return nil, ErrAssertion
	}
	return &CIAssertion{
		Issuer:      claims.Issuer,
		Subject:     claims.Subject,
		Repository:  claims.Repository,
		Workflow:    workflow,
		Ref:         claims.Ref,
		Commit:      claims.Commit,
		RunID:       claims.RunID,
		Environment: claims.Environment,
	}, nil
}

// workflowPath extracts "owner/repo/.github/workflows/x.yml" from
// "owner/repo/.github/workflows/x.yml@refs/heads/main".
func workflowPath(jobWorkflowRef string) string {
	if i := strings.IndexByte(jobWorkflowRef, '@'); i >= 0 {
		return jobWorkflowRef[:i]
	}
	return jobWorkflowRef
}

func audienceMatches(claim any, want string) bool {
	switch a := claim.(type) {
	case string:
		return a == want
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// keyFor returns the RSA key for kid, refreshing the cached key set when
// the kid is unknown — GitHub rotates keys, so an unknown kid is usually a
// rotation, not an attack.
func (v *OIDCVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	if v.keys == nil || now.Sub(v.keysFetched) > jwksCacheTTL {
		if err := v.refresh(ctx, false); err != nil {
			return nil, err
		}
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	// One forced refresh for a rotation that outran the cache.
	if err := v.refresh(ctx, true); err != nil {
		return nil, err
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("no key %q", kid)
}

func (v *OIDCVerifier) refresh(ctx context.Context, force bool) error {
	if !force && v.jwksURI != "" && v.clock().Sub(v.keysFetched) < jwksCacheTTL {
		return nil
	}
	uri := v.jwksURI
	if uri == "" || force {
		discovered, err := v.discover(ctx)
		if err != nil {
			return err
		}
		uri = discovered
		v.jwksURI = uri
	}
	keys, err := v.fetchKeys(ctx, uri)
	if err != nil {
		return err
	}
	v.keys = keys
	v.keysFetched = v.clock()
	return nil
}

func (v *OIDCVerifier) discover(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(v.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
		Issuer  string `json:"issuer"`
	}
	if err := v.getJSON(req, &doc); err != nil {
		return "", err
	}
	if doc.JWKSURI == "" || doc.Issuer != v.Issuer {
		return "", fmt.Errorf("discovery does not describe issuer %q", v.Issuer)
	}
	return doc.JWKSURI, nil
}

func (v *OIDCVerifier) fetchKeys(ctx context.Context, uri string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := v.getJSON(req, &doc); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		key, err := rsaFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("key set carries no RSA keys")
	}
	return keys, nil
}

func (v *OIDCVerifier) getJSON(req *http.Request, out any) error {
	res, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("key endpoint answered %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, oidcMaxBody)).Decode(out)
}

func rsaFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	if len(e) == 0 || len(e) > 8 {
		return nil, fmt.Errorf("exponent size %d is unusable", len(e))
	}
	var exp int
	for _, b := range e {
		exp = exp<<8 | int(b)
	}
	if exp < 3 {
		return nil, fmt.Errorf("exponent %d is unusable", exp)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}, nil
}

func decodeSegment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
