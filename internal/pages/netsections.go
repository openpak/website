package pages

import "net/http"

// Retired edit routes fail closed, including old bookmarks and cached forms.
// A handle is chosen once at signup; changing the display name is separate.
func (s *Server) handleNetworkField(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.postGuard(w, r); !ok {
		return
	}
	http.Error(w, "Your handle is permanent and cannot be changed.", http.StatusGone)
}
