// Package clientip finds the address a request really came from. Production has
// two paths in: Cloudflare -> cloudflared (tunnel) -> this process, and
// Cloudflare or a direct client -> Traefik -> this process. Either way
// r.RemoteAddr is a proxy of ours; keying a rate limit on it would put every
// visitor in one bucket.
//
// Forwarding headers are believed only when the direct peer is a trusted proxy
// (WEBSITE_TRUSTED_PROXIES, comma-separated CIDRs or bare IPs; default the
// private and loopback ranges, where Traefik and cloudflared sit). From anyone
// else they are ignored, so a client cannot pick its own rate-limit key.
//
// Behind a trusted peer, X-Forwarded-For is walked from the right past our own
// proxies; the first hop we do not run is the address that connected to our
// edge (Traefik drops X-Forwarded-For sent from the internet, Cloudflare
// appends the visitor's address). CF-Connecting-IP is believed only when that
// hop is a Cloudflare edge address (WEBSITE_CLOUDFLARE_RANGES, default the
// published ranges below); otherwise the hop itself is the client. So anyone
// who reaches the box directly and sends a forged CF-Connecting-IP is keyed on
// their own address.
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

// DefaultCloudflare is what WEBSITE_CLOUDFLARE_RANGES falls back to: Cloudflare's
// published edge ranges (https://www.cloudflare.com/ips-v4 and /ips-v6, 2026-10-24).
const DefaultCloudflare = "173.245.48.0/20,103.21.244.0/22,103.22.200.0/22,103.31.4.0/22," +
	"141.101.64.0/18,108.162.192.0/18,190.93.240.0/20,188.114.96.0/20,197.234.240.0/22," +
	"198.41.128.0/17,162.158.0.0/15,104.16.0.0/13,104.24.0.0/14,172.64.0.0/13,131.0.72.0/22," +
	"2400:cb00::/32,2606:4700::/32,2803:f800::/32,2405:b500::/32,2405:8100::/32," +
	"2a06:98c0::/29,2c0f:f248::/32"

// Of is the client address for r under the configured trusted proxies and
// Cloudflare ranges.
func Of(r *http.Request) string { return FromCloudflare(r, trusted(), cloudflare()) }

var trusted = sync.OnceValue(func() []netip.Prefix {
	v := strings.TrimSpace(os.Getenv("WEBSITE_TRUSTED_PROXIES"))
	if v == "" {
		v = DefaultTrusted
	}
	return ParsePrefixes(v)
})

var cloudflare = sync.OnceValue(func() []netip.Prefix {
	v := strings.TrimSpace(os.Getenv("WEBSITE_CLOUDFLARE_RANGES"))
	if v == "" {
		v = DefaultCloudflare
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

// From is Of with an explicit trusted list and the default Cloudflare ranges.
func From(r *http.Request, trustedNets []netip.Prefix) string {
	return FromCloudflare(r, trustedNets, ParsePrefixes(DefaultCloudflare))
}

// FromCloudflare is Of with explicit lists (tests, other binaries).
func FromCloudflare(r *http.Request, trustedNets, cfNets []netip.Prefix) string {
	peer := r.RemoteAddr
	if h, _, err := net.SplitHostPort(peer); err == nil {
		peer = h
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil || !inAny(addr, trustedNets) {
		return peer
	}
	// Walk X-Forwarded-For from the right, past our own proxies: the first hop we
	// do not run is the one that connected to our edge. Anything left of it was
	// written by the client and proves nothing.
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return addr.Unmap().String()
		}
		a = a.Unmap()
		if inAny(a, trustedNets) {
			continue
		}
		// A Cloudflare edge connected: it sets CF-Connecting-IP to the visitor's
		// address and overwrites any copy the visitor sent.
		if inAny(a, cfNets) {
			if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
				return cf.Unmap().String()
			}
		}
		return a.String()
	}
	return addr.Unmap().String()
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
