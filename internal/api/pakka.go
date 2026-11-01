package api

import (
	"net/http"

	"github.com/openpak/website/internal/pakkachat"
	"github.com/openpak/website/internal/store"
)

// Pakka, the assistant, for the app. The bearer decides who is asking; the
// request is then forwarded to Pakka for that account (internal/pakkachat).
// Pakka refuses child, supervised and unknown-age accounts itself.

func (s *Server) pakkaRoute(method, pattern string) http.HandlerFunc {
	var rt pakkachat.Route
	for _, r := range pakkachat.Routes {
		if r.Method == method && r.Pattern == pattern {
			rt = r
		}
	}
	return s.auth(func(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
		if s.Pakka == nil {
			writeError(w, http.StatusServiceUnavailable, "Pakka is not available on this deployment")
			return
		}
		s.Pakka.Forward(w, r, pakkachat.Principal{AccountID: tok.AccountID, CoreSessionID: tok.CoreSessionID}, rt)
	})
}

func (s *Server) registerPakka(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	d := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("DELETE /api/v1"+pattern, h) }
	g("/pakka/state", s.pakkaRoute("GET", "/state"))
	h("/pakka/consent", s.pakkaRoute("POST", "/consent"))
	d("/pakka/consent", s.pakkaRoute("DELETE", "/consent"))
	g("/pakka/conversations", s.pakkaRoute("GET", "/conversations"))
	h("/pakka/conversations", s.pakkaRoute("POST", "/conversations"))
	g("/pakka/conversations/{id}", s.pakkaRoute("GET", "/conversations/{id}"))
	d("/pakka/conversations/{id}", s.pakkaRoute("DELETE", "/conversations/{id}"))
	h("/pakka/conversations/{id}/messages", s.pakkaRoute("POST", "/conversations/{id}/messages"))
	// Pakka's own reads (service key plus a grant minted for one request); not the app's API.
	mux.HandleFunc("GET /internal/pakka/tool/{name}", func(w http.ResponseWriter, r *http.Request) {
		if s.Pakka == nil {
			http.NotFound(w, r)
			return
		}
		s.Pakka.ToolHandler(w, r)
	})
}
