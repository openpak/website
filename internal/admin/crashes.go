package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// Crash reports (/admin/crashes): grouped by signature with counts, or one
// row per report; filters on source, version, title, error code, date and
// state; a report's detail has its fields, its file, resolve and a link to a
// help ticket. Ingest is /api/v1/crash-reports and /api/v1/internal/crash-reports.

// crashFilterFrom reads the filter form. Dates are whole UTC days: from is
// inclusive, to includes its whole day.
func crashFilterFrom(q url.Values) (store.CrashFilter, url.Values) {
	f := store.CrashFilter{Source: strings.TrimSpace(q.Get("source")), Version: strings.TrimSpace(q.Get("version")),
		TitleID: strings.TrimSpace(q.Get("title")), ErrorCode: strings.TrimSpace(q.Get("error_code")),
		Signature: strings.TrimSpace(q.Get("signature")), Status: q.Get("status")}
	if f.Status != "resolved" && f.Status != "all" {
		f.Status = "open"
	}
	if d, err := time.Parse("2006-01-02", q.Get("from")); err == nil {
		f.From = d
	}
	if d, err := time.Parse("2006-01-02", q.Get("to")); err == nil {
		f.To = d.AddDate(0, 0, 1)
	}
	keep := url.Values{}
	for _, k := range []string{"source", "version", "title", "error_code", "signature", "status", "from", "to", "view"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	if f.Status == "all" {
		f.Status = ""
	}
	return f, keep
}

func (s *Server) handleCrashes(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f, keep := crashFilterFrom(q)
	status := q.Get("status")
	if status != "resolved" && status != "all" {
		status = "open"
	}
	v := struct {
		base
		Q       url.Values
		Query   string
		Status  string
		List    bool
		Sources []string
		States  []string
		Groups  []store.CrashGroup
		Reports []store.CrashReport
		Names   map[string]string
		Error   string
	}{base: s.page(r, ws, i18n.T("en", "admin.crashes")), Q: q, Query: keep.Encode(), Status: status,
		List: q.Get("view") == "list", Sources: store.CrashSources, States: []string{"open", "resolved", "all"}, Names: pages.TitleCatalogue(r.Context(), s.DB)}
	v.Flash = crashFlash(q.Get("m"))
	var err error
	if v.List {
		v.Reports, err = s.DB.ListCrashes(r.Context(), f, 300)
	} else {
		v.Groups, err = s.DB.CrashGroups(r.Context(), f, 200)
	}
	if err != nil {
		s.log.Error("admin: crashes", "err", err)
		v.Error = err.Error()
	}
	s.render(w, http.StatusOK, "admin_crashes.html", v)
}

func crashFlash(code string) string {
	switch code {
	case "resolved", "reopened", "linked", "unlinked", "noticket":
		return i18n.T("en", "admin.crashes.flash."+code)
	}
	return ""
}

func (s *Server) handleCrash(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.DB.GetCrash(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, c.Fields, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(c.Fields)
	}
	same, _ := s.DB.CrashGroups(r.Context(), store.CrashFilter{Signature: c.Signature}, 1)
	b := s.page(r, ws, fmt.Sprintf("Crash #%d", c.ID))
	b.Flash = crashFlash(r.URL.Query().Get("m"))
	v := struct {
		base
		Crash     store.CrashReport
		Fields    string
		TitleName string
		Group     *store.CrashGroup
	}{base: b, Crash: c, Fields: pretty.String(), TitleName: pages.TitleCatalogue(r.Context(), s.DB)[c.TitleID]}
	if len(same) == 1 {
		v.Group = &same[0]
	}
	s.render(w, http.StatusOK, "admin_crash.html", v)
}

func (s *Server) handleCrashAttachment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r); !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.DB.GetCrash(r.Context(), id)
	if err != nil || c.Attachment.Key == "" || s.CrashFiles == nil {
		http.NotFound(w, r)
		return
	}
	s.CrashFiles.Serve(w, r, c.Attachment.Key, c.Attachment.Name)
}

// handleCrashResolve marks one report, or its whole group (signature in
// the path), resolved or open again.
func (s *Server) handleCrashResolve(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	resolved := r.PostFormValue("resolved") != "0"
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sig := r.PathValue("sig")
	n, err := s.DB.ResolveCrashes(r.Context(), id, sig, resolved, ws.AccountID)
	if err != nil {
		s.log.Error("admin: resolve crash", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	action, flash := "crash.resolve", "resolved"
	if !resolved {
		action, flash = "crash.reopen", "reopened"
	}
	target := "report=" + strconv.FormatInt(id, 10)
	if sig != "" {
		target = "signature=" + sig
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("%s changed=%d", target, n))
	back := r.PostFormValue("back")
	if !strings.HasPrefix(back, "/admin/crashes") {
		back = "/admin/crashes"
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	http.Redirect(w, r, back+sep+"m="+flash, http.StatusSeeOther)
}

// handleCrashTicket links a report to a help ticket (0 or empty unlinks).
func (s *Server) handleCrashTicket(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ticket, _ := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(r.PostFormValue("ticket")), "#"), 10, 64)
	back := fmt.Sprintf("/admin/crashes/%d", id)
	err := s.DB.LinkCrashTicket(r.Context(), id, ticket)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, back+"?m=noticket", http.StatusSeeOther)
		return
	case err != nil:
		s.log.Error("admin: link crash", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	flash := "linked"
	if ticket <= 0 {
		flash = "unlinked"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "crash.ticket", fmt.Sprintf("report=%d ticket=%d", id, ticket))
	http.Redirect(w, r, back+"?m="+flash, http.StatusSeeOther)
}
