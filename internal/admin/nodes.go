package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
)

// nodeRow is one region as the admin page shows it.
type nodeRow struct {
	core.Node
	Seen  string
	Fresh bool // announced within the last few minutes
}

type nodesView struct {
	base
	Nodes []nodeRow
	Key   string // set only on the response to a create, never again
	Error string
}

func (s *Server) renderNodes(w http.ResponseWriter, r *http.Request, ws sessionUser, key, errMsg string, status int) {
	var rows []nodeRow
	if s.Federation != nil {
		nodes, err := s.Federation.ListAllNodes(r.Context())
		if err != nil {
			s.log.Error("list nodes", "err", err)
			errMsg = err.Error()
		}
		for _, n := range nodes {
			row := nodeRow{Node: n, Seen: i18n.T("en", "admin.nodes.never")}
			if !n.LastSeen.IsZero() {
				row.Seen = n.LastSeen.UTC().Format("2006-01-02 15:04 UTC")
				row.Fresh = time.Since(n.LastSeen) < 3*time.Minute
			}
			rows = append(rows, row)
		}
	}
	s.render(w, status, "admin_nodes.html", nodesView{
		base:  base{Asset: pages.AssetVersion(), Admin: true, Path: "/admin/nodes", Lang: "en", Title: i18n.T("en", "admin.nodes"), User: adminUser{ws.DisplayName, ws.Email}},
		Nodes: rows, Key: key, Error: errMsg,
	})
}

type sessionUser struct{ DisplayName, Email string }

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.renderNodes(w, r, sessionUser{ws.DisplayName, ws.Email}, "", "", http.StatusOK)
}

func (s *Server) handleNodeCreate(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.Federation == nil {
		http.Error(w, "federation not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.PostFormValue("id"))
	key, err := s.Federation.CreateNode(r.Context(), id, r.PostFormValue("name"), r.PostFormValue("country"))
	if err != nil {
		s.renderNodes(w, r, sessionUser{ws.DisplayName, ws.Email}, "", err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.create", id)
	// The key is rendered into this one response and nowhere else: the core
	// keeps a hash, so there is no second chance to read it.
	s.renderNodes(w, r, sessionUser{ws.DisplayName, ws.Email}, key, "", http.StatusOK)
}

func (s *Server) handleNodeApprove(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.Federation == nil {
		http.Error(w, "federation not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	approved := r.FormValue("approved") == "true"
	if err := s.Federation.SetNodeApproved(r.Context(), id, approved); err != nil {
		s.renderNodes(w, r, sessionUser{ws.DisplayName, ws.Email}, "", err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.approve", id+"="+r.FormValue("approved"))
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}
