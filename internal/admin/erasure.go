package admin

import (
	"net/http"

	"github.com/openpak/website/internal/erasure"
	"github.com/openpak/website/internal/i18n"
)

type erasureView struct {
	base
	Steps []erasure.StepReport
	// Missing counts expected services with no step configured.
	Missing int
}

// handleErasure shows every service the account erasure finaliser depends
// on, so a missing or failing step is visible without reading the logs.
func (s *Server) handleErasure(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	v := erasureView{base: s.page(r, ws, i18n.T("en", "admin.erasure"))}
	if s.ErasureReport != nil {
		v.Steps = s.ErasureReport()
	}
	for _, st := range v.Steps {
		if !st.Configured {
			v.Missing++
		}
	}
	s.render(w, http.StatusOK, "admin_erasure.html", v)
}
