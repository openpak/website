// Package clientip finds the address a request really came from. Production is
// Cloudflare -> Traefik -> this process, so r.RemoteAddr is Traefik's; keying a
// rate limit on it would put every visitor in one bucket.
//
// Forwarding headers are believed only when the direct peer is a trusted proxy
// (WEBSITE_TRUSTED_PROXIES, comma-separated CIDRs or bare IPs; default the
// private and loopback ranges, where Traefik and cloudflared sit). From anyone
// else they are ignored, so a client cannot pick its own rate-limit key.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
)

// DefaultTrusted is what WEBSITE_TRUSTED_PROXIES falls back to: the podman
// network, the host, and IPv6 unique-local addresses.
const DefaultTrusted = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7"

// Of is the client address for r under the configured trusted proxies.
func Of(r *http.Request) string { return From(r, trusted()) }

var trusted = sync.OnceValue(func() []netip.Prefix {
	v := strings.TrimSpace(os.Getenv("WEBSITE_TRUSTED_PROXIES"))
	if v == "" {
		v = DefaultTrusted
	}
	return ParsePrefixes(v)
})

// ParsePrefixes reads a comma-separated list of CIDRs and bare IPs. Entries that
// do not parse are dropped: trusting less is the safe way to be wrong. "none"
// trusts nobody.
func ParsePrefixes(list string) []netip.Prefix {
	var out []netip.Prefix
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(f); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out
}

// From is Of with an explicit trusted list (tests, other binaries).
func From(r *http.Request, trustedNets []netip.Prefix) string {
	peer := r.RemoteAddr
	if h, _, err := net.SplitHostPort(peer); err == nil {
		peer = h
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil || !inAny(addr, trustedNets) {
		return peer
	}
	// Cloudflare sets this to the visitor's address and overwrites any copy the
	// visitor sent.
	if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
		return cf.Unmap().String()
	}
	// Otherwise walk X-Forwarded-For from the right, past our own proxies: the
	// first hop we do not run is the one that connected to us. Anything left of
	// it was written by the client and proves nothing.
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer
		}
		if !inAny(a, trustedNets) {
			return a.Unmap().String()
		}
	}
	return peer
}

func inAny(a netip.Addr, nets []netip.Prefix) bool {
	a = a.Unmap()
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
