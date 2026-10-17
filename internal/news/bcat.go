// SPDX-License-Identifier: AGPL-3.0-only

package news

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// NewBCATProxy keeps the emulator's manifest and file URLs on the website
// origin. The upstream is an operator-configured origin, never client input.
// Empty configuration returns nil so the API can report delivery unavailable.
func NewBCATProxy(origin string) (http.Handler, error) {
	if origin == "" {
		return nil, nil
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("BCAT_URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	u.Path = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 10 * time.Second
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			// These public downloads do not use the player's session or operator keys.
			for _, header := range []string{"Authorization", "Cookie", "X-Internal-Key", "X-Operator-Key"} {
				r.Out.Header.Del(header)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "BCAT delivery unavailable", http.StatusBadGateway)
		},
	}, nil
}
