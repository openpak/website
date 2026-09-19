package admin

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// handleTitles is every title with its status and notes, editable in place.
// Titles only: a feature row's status follows the catalog, so editing it here
// would be undone by the next catalog refresh.
func (s *Server) handleTitles(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	titles, err := s.DB.ListRequests(r.Context(), "title", "")
	if err != nil {
		s.log.Error("list titles", "err", err)
	}
	sort.SliceStable(titles, func(i, j int) bool {
		if titles[i].Console != titles[j].Console {
			return titles[i].Console < titles[j].Console
		}
		return titles[i].Name < titles[j].Name
	})
	b := s.page(r, ws, i18n.T("en", "admin.titles"))
	if r.URL.Query().Get("m") == "saved" {
		b.Flash = i18n.T("en", "msg.saved")
	}
	s.render(w, http.StatusOK, "admin_titles.html", struct {
		base
		Titles   []store.Request
		Statuses []string
	}{b, titles, pages.TitleStatuses()})
}

func (s *Server) handleTitleSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	err := pages.SetTitleStatus(r.Context(), s.DB, ws.AccountID, id, r.PostFormValue("status"), r.PostFormValue("notes"))
	if errors.Is(err, pages.ErrBadTitleStatus) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		s.log.Error("set title status", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/titles?m=saved#t"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
