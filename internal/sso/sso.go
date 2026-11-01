// Package sso lets sites on other openpak.org subdomains (OpenSplat, OpenKart, OpenMaker,
// OpenVerse, rewards) sign people in with their openpak.org login. The website's session cookie is host-only,
// so a subdomain cannot see it; instead the subsite sends the browser to /account/sso/authorize,
// gets back a single-use code of about a minute, and its server exchanges the code at
// POST /internal/sso/token using its own client secret. The subsite then keeps its own session.
//
// The check that matters is the redirect: a code only ever goes to an https origin registered
// for that client, so a link made by someone else cannot send a signed-in person's code elsewhere.
package sso

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// CodeTTL is how long a code can be exchanged.
const CodeTTL = 60 * time.Second

// Client is a registered subsite.
type Client struct {
	ID      string
	Secret  string
	Origins []string // exact https origins a code may be sent to, "https://opensplat.openpak.org"
}

// builtin names the subsites and the host each gets unless its REDIRECTS variable says otherwise.
var builtin = []struct{ ID, Env, Host string }{
	{"opensplat", "OPENSPLAT", "opensplat.openpak.org"},
	{"openkart", "OPENKART", "openkart.openpak.org"},
	{"openmaker", "OPENMAKER", "openmaker.openpak.org"},
	{"openverse", "OPENVERSE", "openverse.openpak.org"},
	{"rewards", "REWARDS", "rewards.openpak.org"},
}

// FromEnv registers the clients whose SSO_CLIENT_<NAME>_SECRET is set. SSO_CLIENT_<NAME>_REDIRECTS,
// when set, is a comma-separated list of https origins replacing the default host.
func FromEnv() []Client {
	var out []Client
	for _, b := range builtin {
		secret := os.Getenv("SSO_CLIENT_" + b.Env + "_SECRET")
		if secret == "" {
			continue
		}
		origins := []string{"https://" + b.Host}
		if v := os.Getenv("SSO_CLIENT_" + b.Env + "_REDIRECTS"); v != "" {
			origins = nil
			for _, o := range strings.Split(v, ",") {
				if o = strings.TrimSpace(o); o != "" {
					origins = append(origins, o)
				}
			}
		}
		out = append(out, Client{ID: b.ID, Secret: secret, Origins: origins})
	}
	return out
}

// Grant is what a code stands for.
type Grant struct {
	Client      string
	AccountID   string
	RedirectURI string
	Challenge   string // PKCE S256 challenge, "" when the client did not use one
	expires     time.Time
}

// Service holds the registered clients and the outstanding codes. Codes live in memory: they
// last a minute, and a restart only costs a person one more click.
type Service struct {
	mu      sync.Mutex
	clients map[string]Client
	codes   map[string]Grant
	now     func() time.Time
}

// New returns a Service over the given clients.
func New(clients []Client) *Service {
	s := &Service{clients: map[string]Client{}, codes: map[string]Grant{}, now: time.Now}
	for _, c := range clients {
		s.clients[c.ID] = c
	}
	return s
}

// Enabled reports whether any client is registered.
func (s *Service) Enabled() bool { return s != nil && len(s.clients) > 0 }

// Client looks a client up by id.
func (s *Service) Client(id string) (Client, bool) {
	if s == nil {
		return Client{}, false
	}
	c, ok := s.clients[id]
	return c, ok
}

// ValidRedirect reports whether uri is an https URL on one of the client's registered origins:
// the scheme, host and port must match exactly; no userinfo, no fragment.
func (c Client) ValidRedirect(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	for _, o := range c.Origins {
		ou, err := url.Parse(o)
		if err == nil && ou.Scheme == "https" && strings.EqualFold(ou.Host, u.Host) {
			return true
		}
	}
	return false
}

// SecretOK compares a presented secret in constant time.
func (c Client) SecretOK(secret string) bool {
	return secret != "" && subtle.ConstantTimeCompare([]byte(c.Secret), []byte(secret)) == 1
}

// Issue mints a single-use code for the account. Expired codes are swept on the way.
func (s *Service) Issue(client, accountID, redirectURI, challenge string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, g := range s.codes {
		if now.After(g.expires) {
			delete(s.codes, k)
		}
	}
	s.codes[code] = Grant{Client: client, AccountID: accountID, RedirectURI: redirectURI, Challenge: challenge, expires: now.Add(CodeTTL)}
	return code
}

// Redeem consumes a code. It is gone after the first call whatever the outcome, so a wrong
// redirect_uri or verifier burns it rather than allowing a second guess. ok is false for an
// unknown, used, expired, other-client or mismatching code.
func (s *Service) Redeem(client, code, redirectURI, verifier string) (Grant, bool) {
	s.mu.Lock()
	g, found := s.codes[code]
	delete(s.codes, code)
	now := s.now()
	s.mu.Unlock()
	if !found || now.After(g.expires) || g.Client != client {
		return Grant{}, false
	}
	if subtle.ConstantTimeCompare([]byte(g.RedirectURI), []byte(redirectURI)) != 1 {
		return Grant{}, false
	}
	if g.Challenge != "" {
		sum := sha256.Sum256([]byte(verifier))
		if verifier == "" || subtle.ConstantTimeCompare([]byte(g.Challenge), []byte(base64.RawURLEncoding.EncodeToString(sum[:]))) != 1 {
			return Grant{}, false
		}
	}
	return g, true
}

// AnyClientAccepts reports whether uri is a valid redirect for some registered client; the
// logout page does not know which subsite sent the person.
func (s *Service) AnyClientAccepts(uri string) bool {
	if s == nil {
		return false
	}
	for _, c := range s.clients {
		if c.ValidRedirect(uri) {
			return true
		}
	}
	return false
}
