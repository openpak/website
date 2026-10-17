// SPDX-License-Identifier: AGPL-3.0-only

package api

import "net/http"

func (s *Server) registerBCAT(mux *http.ServeMux) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.BCAT == nil {
			http.Error(w, "BCAT delivery is not configured", http.StatusServiceUnavailable)
			return
		}
		s.BCAT.ServeHTTP(w, r)
	})
	for _, pattern := range []string{
		"GET /api/emulator/v1/bcat/titles",
		"GET /api/emulator/v1/bcat/titles/{title}",
		"GET /api/emulator/v1/bcat/titles/{title}/files/{path...}",
	} {
		mux.Handle(pattern, handler)
	}
}
