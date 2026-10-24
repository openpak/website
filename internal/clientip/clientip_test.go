package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFrom(t *testing.T) {
	trusted := ParsePrefixes(DefaultTrusted + ",garbage")
	cf := ParsePrefixes(DefaultCloudflare)
	for _, c := range []struct {
		name, remote, cf, xff, want string
	}{
		{"direct client, no headers", "203.0.113.7:5000", "", "", "203.0.113.7"},
		{"untrusted peer cannot spoof CF-Connecting-IP", "203.0.113.7:5000", "198.51.100.1", "", "203.0.113.7"},
		{"untrusted peer cannot spoof X-Forwarded-For", "203.0.113.7:5000", "", "198.51.100.1", "203.0.113.7"},

		// Cloudflare -> Traefik: Traefik drops client X-Forwarded-For and writes the
		// TCP peer it saw, a Cloudflare edge address.
		{"Traefik, Cloudflare edge peer, CF header believed", "10.89.2.96:41000", "198.51.100.9", "172.70.1.1", "198.51.100.9"},
		{"Traefik, Cloudflare IPv6 edge peer", "10.89.2.96:41000", "2001:db8::9", "2606:4700:10::ac43:1", "2001:db8::9"},
		{"Traefik, Cloudflare peer, own proxies on the right", "10.89.2.96:41000", "198.51.100.9", "162.158.4.4, 10.89.2.96", "198.51.100.9"},
		{"Traefik, Cloudflare peer, garbage CF header uses the edge", "10.89.2.96:41000", "not-an-ip", "172.70.1.1", "172.70.1.1"},
		{"Traefik, Cloudflare peer, no CF header uses the edge", "10.89.2.96:41000", "", "104.16.5.5", "104.16.5.5"},

		// Direct to the box IP through Traefik with forged headers.
		{"forged CF header from a non-Cloudflare peer", "10.89.2.96:41000", "1.1.1.1", "203.0.113.50", "203.0.113.50"},
		{"forged CF header and forged Cloudflare XFF left of the peer", "10.89.2.96:41000", "1.1.1.1", "172.70.1.1, 203.0.113.50", "203.0.113.50"},

		// Cloudflare -> cloudflared tunnel: X-Forwarded-For ends in the visitor's
		// address, which is not a Cloudflare one.
		{"tunnel, visitor is the right-most hop", "10.89.2.66:41000", "198.51.100.9", "198.51.100.9", "198.51.100.9"},
		{"tunnel, client-written left entries are ignored", "10.89.2.66:41000", "6.6.6.6", "6.6.6.6, 198.51.100.9", "198.51.100.9"},
		{"right-most untrusted hop wins", "10.89.0.4:41000", "", "1.1.1.1, 198.51.100.9, 10.89.0.2", "198.51.100.9"},

		// A trusted peer that sent no forwarding chain is the client itself; a bare
		// CF-Connecting-IP proves nothing.
		{"trusted peer, no headers", "127.0.0.1:41000", "", "", "127.0.0.1"},
		{"trusted peer, CF header without XFF is ignored", "10.89.2.96:41000", "198.51.100.1", "", "10.89.2.96"},
		{"IPv6 trusted peer, CF header without XFF is ignored", "[fd00::5]:443", "2001:db8::9", "", "fd00::5"},
		{"garbage XFF hop keeps the peer", "10.89.0.4:41000", "", "198.51.100.9, nope", "10.89.0.4"},
		{"IPv6 untrusted peer", "[2001:db8::1]:443", "198.51.100.1", "", "2001:db8::1"},
		{"IPv4-mapped XFF hop is unmapped", "10.89.0.4:41000", "", "::ffff:198.51.100.9", "198.51.100.9"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := FromCloudflare(r, trusted, cf); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if got := From(r, trusted); got != c.want {
			t.Errorf("%s (From): got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCloudflareRangesOverride(t *testing.T) {
	trusted := ParsePrefixes(DefaultTrusted)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.89.2.96:1"
	r.Header.Set("CF-Connecting-IP", "198.51.100.1")
	r.Header.Set("X-Forwarded-For", "203.0.113.50")
	if got := FromCloudflare(r, trusted, ParsePrefixes("203.0.113.0/24")); got != "198.51.100.1" {
		t.Errorf("custom range: got %q", got)
	}
	r.Header.Set("X-Forwarded-For", "172.70.1.1")
	if got := FromCloudflare(r, trusted, ParsePrefixes("none")); got != "172.70.1.1" {
		t.Errorf("none: got %q", got)
	}
}

func TestDefaultCloudflareParses(t *testing.T) {
	if n := len(ParsePrefixes(DefaultCloudflare)); n != 22 {
		t.Errorf("DefaultCloudflare parsed to %d prefixes, want 22", n)
	}
}

func TestNoneTrustsNobody(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("CF-Connecting-IP", "198.51.100.1")
	if got := From(r, ParsePrefixes("none")); got != "10.0.0.1" {
		t.Errorf("got %q", got)
	}
}
