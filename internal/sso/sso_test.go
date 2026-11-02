package sso

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func svc() *Service {
	return New([]Client{
		{ID: "opensplat", Secret: "s1", Origins: []string{"https://splat.openpak.org"}},
		{ID: "openkart", Secret: "s2", Origins: []string{"https://kart.openpak.org"}},
	})
}

func TestValidRedirect(t *testing.T) {
	c, _ := svc().Client("opensplat")
	for uri, want := range map[string]bool{
		"https://splat.openpak.org/auth/callback":        true,
		"https://splat.openpak.org":                      true,
		"https://splat.openpak.org/x?y=1":                true,
		"http://splat.openpak.org/auth/callback":         false, // not https
		"https://evil.openpak.org/auth/callback":         false, // another subdomain
		"https://splat.openpak.org.evil.test/cb":         false,
		"https://splat.openpak.org@evil.test/cb":         false, // userinfo
		"https://evil.test/?u=https://splat.openpak.org": false,
		"https://splat.openpak.org:8443/cb":              false, // other port
		"https://splat.openpak.org/cb#frag":              false,
		"//splat.openpak.org/cb":                         false,
		"javascript:alert(1)":                            false,
		"":                                               false,
	} {
		if got := c.ValidRedirect(uri); got != want {
			t.Errorf("ValidRedirect(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestCodeIsSingleUse(t *testing.T) {
	s := svc()
	code := s.Issue("opensplat", "acct", "https://splat.openpak.org/cb", "")
	g, ok := s.Redeem("opensplat", code, "https://splat.openpak.org/cb", "")
	if !ok || g.AccountID != "acct" {
		t.Fatalf("first redeem: %+v %v", g, ok)
	}
	if _, ok := s.Redeem("opensplat", code, "https://splat.openpak.org/cb", ""); ok {
		t.Fatal("a code was redeemed twice")
	}
}

func TestCodeExpires(t *testing.T) {
	s := svc()
	now := time.Now()
	s.now = func() time.Time { return now }
	code := s.Issue("opensplat", "acct", "https://splat.openpak.org/cb", "")
	s.now = func() time.Time { return now.Add(CodeTTL + time.Second) }
	if _, ok := s.Redeem("opensplat", code, "https://splat.openpak.org/cb", ""); ok {
		t.Fatal("an expired code was accepted")
	}
}

func TestRedirectMismatchBurnsTheCode(t *testing.T) {
	s := svc()
	code := s.Issue("opensplat", "acct", "https://splat.openpak.org/cb", "")
	if _, ok := s.Redeem("opensplat", code, "https://splat.openpak.org/other", ""); ok {
		t.Fatal("a different redirect_uri was accepted")
	}
	if _, ok := s.Redeem("opensplat", code, "https://splat.openpak.org/cb", ""); ok {
		t.Fatal("the code survived a failed attempt")
	}
}

func TestCodeBoundToClient(t *testing.T) {
	s := svc()
	code := s.Issue("opensplat", "acct", "https://splat.openpak.org/cb", "")
	if _, ok := s.Redeem("openkart", code, "https://splat.openpak.org/cb", ""); ok {
		t.Fatal("another client redeemed the code")
	}
}

func TestPKCE(t *testing.T) {
	verifier := "a-long-random-verifier-0123456789-abcdefghijklmnop"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	s := svc()
	uri := "https://splat.openpak.org/cb"
	code := s.Issue("opensplat", "acct", uri, challenge)
	if _, ok := s.Redeem("opensplat", code, uri, "wrong"); ok {
		t.Fatal("a wrong verifier was accepted")
	}
	code = s.Issue("opensplat", "acct", uri, challenge)
	if _, ok := s.Redeem("opensplat", code, uri, ""); ok {
		t.Fatal("a missing verifier was accepted")
	}
	code = s.Issue("opensplat", "acct", uri, challenge)
	if _, ok := s.Redeem("opensplat", code, uri, verifier); !ok {
		t.Fatal("the right verifier was refused")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("SSO_CLIENT_OPENMAKER_SECRET", "x")
	t.Setenv("SSO_CLIENT_REWARDS_SECRET", "y")
	t.Setenv("SSO_CLIENT_REWARDS_REDIRECTS", "https://a.openpak.org, https://b.openpak.org")
	got := FromEnv()
	if len(got) != 2 || got[0].ID != "openmaker" || got[0].Origins[0] != "https://maker.openpak.org" || len(got[1].Origins) != 2 {
		t.Fatalf("FromEnv: %+v", got)
	}
}
