package pages

import (
	"net/http"

	"github.com/openpak/website/internal/i18n"
)

// handleTurnstileWidget: GET /turnstile-widget, the Turnstile widget alone, for the app's
// webview. The app sends the answer with POST /api/v1/register or /password/forgot; the
// page hands it over through the webview's OpenPakTurnstile channel (static
// turnstile-widget.js). Served from this site so the widget needs no extra hostname.
func (s *Server) handleTurnstileWidget(w http.ResponseWriter, r *http.Request) {
	if s.Turnstile == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, http.StatusOK, "turnstile_widget.html", struct {
		Asset, Lang, Key string
	}{assetVersion(), i18n.FromRequest(r), s.Turnstile.Key()})
}
