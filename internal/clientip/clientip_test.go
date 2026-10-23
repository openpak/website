package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFrom(t *testing.T) {
	trusted := ParsePrefixes(DefaultTrusted + ",garbage")
	for _, c := range []struct {
		name, remote, cf, xff, want string
	}{
		{"direct client, no headers", "203.0.113.7:5000", "", "", "203.0.113.7"},
		{"untrusted peer cannot spoof CF-Connecting-IP", "203.0.113.7:5000", "198.51.100.1", "", "203.0.113.7"},
		{"untrusted peer cannot spoof X-Forwarded-For", "203.0.113.7:5000", "", "198.51.100.1", "203.0.113.7"},
		{"Traefik with Cloudflare header", "10.89.0.4:41000", "198.51.100.9", "198.51.100.9, 172.70.1.1", "198.51.100.9"},
		{"Traefik, right-most untrusted hop wins", "10.89.0.4:41000", "", "1.1.1.1, 198.51.100.9, 10.89.0.2", "198.51.100.9"},
		{"client-written left entries are ignored", "10.89.0.4:41000", "", "6.6.6.6, 198.51.100.9", "198.51.100.9"},
		{"trusted peer, no headers", "127.0.0.1:41000", "", "", "127.0.0.1"},
		{"garbage CF header falls through to XFF", "10.89.0.4:41000", "not-an-ip", "198.51.100.9", "198.51.100.9"},
		{"garbage XFF hop keeps the peer", "10.89.0.4:41000", "", "198.51.100.9, nope", "10.89.0.4"},
		{"IPv6 peer", "[2001:db8::1]:443", "198.51.100.1", "", "2001:db8::1"},
		{"IPv6 trusted peer", "[fd00::5]:443", "2001:db8::9", "", "2001:db8::9"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := From(r, trusted); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
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
