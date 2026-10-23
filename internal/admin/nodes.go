package admin

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// nodeRow is one region as the admin page shows it.
type nodeRow struct {
	core.Node
	Seen        string
	Fresh       bool // a server announced within the last few minutes
	Totals      string
	WaiverUntil string // "" = no expiry
	Servers     []serverRow
}

// serverRow is one server's latest report, ready to print.
type serverRow struct {
	core.NodeServer
	Roles    string
	Specs    string
	Address  string
	Seen     string
	SelfTest string
}

type nodesView struct {
	base
	Nodes     []nodeRow
	Locations []core.NodeLocation
	Req       *core.NodeRequirements
	Key       string // set only on the response to a create or rotate, never again
	KeyNode   string
	Error     string
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return i18n.T("en", "admin.nodes.never")
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func gbOf(mb int64) string { return fmt.Sprintf("%.1f GB", float64(mb)/1024) }

func toNodeRow(n core.Node) nodeRow {
	row := nodeRow{Node: n, Seen: stamp(n.LastSeen), Fresh: !n.LastSeen.IsZero() && time.Since(n.LastSeen) < 3*time.Minute,
		Totals: fmt.Sprintf("%d cores · %s RAM · %d GB SSD · %d servers", n.TotalCores, gbOf(n.TotalRAMMB), n.TotalDiskGB, len(n.Servers))}
	if !n.WaiverUntil.IsZero() {
		row.WaiverUntil = n.WaiverUntil.UTC().Format("2006-01-02")
	}
	for _, v := range n.Servers {
		roles := make([]string, 0, len(v.Services))
		for k, a := range v.Services {
			roles = append(roles, k+" "+a)
		}
		sort.Strings(roles)
		disk := "HDD"
		if v.Report.DiskSSD {
			disk = "SSD"
		}
		addr := v.Report.IPv4
		if v.Report.IPv6 != "" {
			addr += " · " + v.Report.IPv6
		}
		if v.ObservedIP != "" && v.ObservedIP != v.Report.IPv4 {
			addr += " (seen from " + v.ObservedIP + ")"
		}
		st := "none yet"
		if !v.DownAt.IsZero() || !v.UpAt.IsZero() {
			st = fmt.Sprintf("down %.0f Mbps (%s) · up %.0f Mbps (%s)", v.DownMbps, stamp(v.DownAt), v.UpMbps, stamp(v.UpAt))
			if v.RTTMs > 0 {
				st += fmt.Sprintf(" · RTT %d ms (server-reported)", v.RTTMs)
			}
		}
		row.Servers = append(row.Servers, serverRow{NodeServer: v, Roles: strings.Join(roles, ", "),
			Specs:   fmt.Sprintf("%d cores · %s RAM · %d GB %s · port %d Mbps", v.Report.Cores, gbOf(v.Report.RAMMB), v.Report.DiskGB, disk, v.Report.PortMbps),
			Address: addr, Seen: stamp(v.LastSeen), SelfTest: st})
	}
	return row
}

func (s *Server) renderNodes(w http.ResponseWriter, r *http.Request, ws store.WebSession, key, keyNode, errMsg string, status int) {
	var rows []nodeRow
	var locs []core.NodeLocation
	var req *core.NodeRequirements
	if s.Federation != nil {
		nodes, err := s.Federation.ListAllNodes(r.Context())
		if err != nil {
			s.log.Error("list nodes", "err", err)
			errMsg = err.Error()
		}
		for _, n := range nodes {
			rows = append(rows, toNodeRow(n))
		}
		locs, _ = s.Federation.ListLocations(r.Context())
		if rq, err := s.Federation.NodeRequirements(r.Context()); err == nil {
			req = &rq
		}
	}
	b := s.page(r, ws, i18n.T("en", "admin.nodes"))
	b.Path = "/admin/nodes" // also rendered from the POSTs
	s.render(w, status, "admin_nodes.html", nodesView{
		base:  b,
		Nodes: rows, Locations: locs, Req: req, Key: key, KeyNode: keyNode, Error: errMsg,
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.renderNodes(w, r, ws, "", "", r.URL.Query().Get("error"), http.StatusOK)
}

// fedReady answers 503 when the core's register is not wired.
func (s *Server) fedReady(w http.ResponseWriter) bool {
	if s.Federation == nil {
		http.Error(w, "federation not configured", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// handleNodeCreate registers a node anywhere: admins are not bound by the
// coverage plan, applicants are.
func (s *Server) handleNodeCreate(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	id := strings.TrimSpace(r.PostFormValue("id"))
	key, err := s.Federation.CreateNode(r.Context(), id, r.PostFormValue("name"), r.PostFormValue("country"), r.PostFormValue("region"))
	if err != nil {
		s.renderNodes(w, r, ws, "", "", err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.create", id)
	// The key is rendered into this one response and nowhere else: the core
	// keeps a hash, so there is no second chance to read it.
	s.renderNodes(w, r, ws, key, id, "", http.StatusOK)
}

func (s *Server) handleNodeRotateKey(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	id := r.PathValue("id")
	key, err := s.Federation.RotateNodeKey(r.Context(), id)
	if err != nil {
		s.renderNodes(w, r, ws, "", "", err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.rotate_key", id)
	s.renderNodes(w, r, ws, key, id, "", http.StatusOK)
}

// handleNodeApprove flips approval. The core refuses to approve a node below
// the minimum and says what is short; that message is shown as it is.
func (s *Server) handleNodeApprove(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	id := r.PathValue("id")
	approved := r.PostFormValue("approved") == "true"
	if err := s.Federation.SetNodeApproved(r.Context(), id, approved); err != nil {
		s.renderNodes(w, r, ws, "", "", "Not approved: "+coreMessage(err), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.approve", id+"="+r.PostFormValue("approved"))
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}

// coreMessage strips the core client's error prefix, leaving the core's words.
func coreMessage(err error) string {
	msg := err.Error()
	for _, p := range []error{core.ErrConflict, core.ErrBlocked, core.ErrTooSoon} {
		msg = strings.TrimPrefix(msg, p.Error()+": ")
	}
	return msg
}

// handleNodeWaiver sets or clears the hardware waiver. A reason is required;
// the expiry is optional. Both land in the audit log.
func (s *Server) handleNodeWaiver(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	id := r.PathValue("id")
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	var until time.Time
	if r.PostFormValue("clear") == "true" {
		reason = ""
	} else {
		if reason == "" {
			s.renderNodes(w, r, ws, "", "", "A waiver needs a reason.", http.StatusBadRequest)
			return
		}
		if d := r.PostFormValue("until"); d != "" {
			t, err := time.Parse("2006-01-02", d)
			if err != nil {
				s.renderNodes(w, r, ws, "", "", "Expiry must be a date (YYYY-MM-DD).", http.StatusBadRequest)
				return
			}
			until = t.Add(24*time.Hour - time.Second) // through the end of that day, UTC
		}
	}
	if err := s.Federation.SetNodeWaiver(r.Context(), id, reason, until); err != nil {
		s.renderNodes(w, r, ws, "", "", "Waiver not saved: "+coreMessage(err), http.StatusBadRequest)
		return
	}
	detail := id + " cleared"
	if reason != "" {
		detail = id + " reason=" + strconv.Quote(reason)
		if !until.IsZero() {
			detail += " until=" + until.UTC().Format("2006-01-02")
		}
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.node.waiver", detail)
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}

func (s *Server) handleServerRevoke(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	id := r.PostFormValue("server")
	if err := s.Federation.RevokeServer(r.Context(), id); err != nil {
		s.renderNodes(w, r, ws, "", "", err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.server.revoke", id)
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}

// --- Coverage plan ---

type locationsView struct {
	base
	Locations []core.NodeLocation
	Edit      core.NodeLocation
	Error     string
}

func (s *Server) renderLocations(w http.ResponseWriter, r *http.Request, ws store.WebSession, errMsg string, status int) {
	var locs []core.NodeLocation
	if s.Federation != nil {
		var err error
		if locs, err = s.Federation.ListLocations(r.Context()); err != nil {
			errMsg = err.Error()
		}
	}
	v := locationsView{base: s.page(r, ws, i18n.T("en", "admin.locations")), Locations: locs, Error: errMsg, Edit: core.NodeLocation{Wanted: 1, Status: "open"}}
	v.Path = "/admin/nodes/locations"
	if code := r.URL.Query().Get("edit"); code != "" {
		for _, l := range locs {
			if l.Code == code {
				v.Edit = l
			}
		}
	}
	s.render(w, status, "admin_locations.html", v)
}

func (s *Server) handleLocations(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.renderLocations(w, r, ws, "", http.StatusOK)
}

func (s *Server) handleLocationSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	wanted, err := strconv.Atoi(r.PostFormValue("wanted"))
	if err != nil {
		s.renderLocations(w, r, ws, "Wanted must be a number.", http.StatusBadRequest)
		return
	}
	l := core.NodeLocation{Code: r.PostFormValue("code"), Country: r.PostFormValue("country"), City: r.PostFormValue("city"),
		Wanted: wanted, Status: r.PostFormValue("status")}
	if err := s.Federation.PutLocation(r.Context(), l); err != nil {
		s.renderLocations(w, r, ws, "Not saved: "+coreMessage(err), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.location.put", fmt.Sprintf("%s %s %q wanted=%d %s", l.Code, l.Country, l.City, l.Wanted, l.Status))
	http.Redirect(w, r, "/admin/nodes/locations", http.StatusSeeOther)
}

func (s *Server) handleLocationDelete(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || !s.fedReady(w) {
		return
	}
	code := r.PathValue("code")
	if err := s.Federation.DeleteLocation(r.Context(), code); err != nil {
		s.renderLocations(w, r, ws, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.location.delete", code)
	http.Redirect(w, r, "/admin/nodes/locations", http.StatusSeeOther)
}

// --- Applications ---

type applicationsView struct {
	base
	Apps    []store.OperatorApplication
	Filter  string
	Key     string // the new node's key, on the approve response only
	KeyNode string
	Error   string
}

func (s *Server) renderApplications(w http.ResponseWriter, r *http.Request, ws store.WebSession, filter, key, keyNode, errMsg string, status int) {
	var apps []store.OperatorApplication
	if s.Operators != nil {
		var err error
		if apps, err = s.Operators.ListOperatorApplications(r.Context(), filter); err != nil {
			errMsg = err.Error()
		}
	}
	v := applicationsView{base: s.page(r, ws, i18n.T("en", "admin.applications")), Apps: apps, Filter: filter, Key: key, KeyNode: keyNode, Error: errMsg}
	v.Path = "/admin/nodes/applications"
	s.render(w, status, "admin_applications.html", v)
}

func (s *Server) handleApplications(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	filter := r.URL.Query().Get("status")
	if filter == "" && !r.URL.Query().Has("status") {
		filter = store.OpPending
	}
	s.renderApplications(w, r, ws, filter, "", "", "", http.StatusOK)
}

var nodeSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// handleApplicationDecide is approve (creates the node and shows its key
// once), reject (with a reason) or info (ask the applicant something).
func (s *Server) handleApplicationDecide(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Operators == nil || !s.fedReady(w) {
		http.Error(w, "applications not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	app, err := s.Operators.GetOperatorApplication(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	fail := func(msg string) {
		s.renderApplications(w, r, ws, store.OpPending, "", "", msg, http.StatusBadRequest)
	}
	switch r.PathValue("action") {
	case "approve":
		if app.Status != store.OpPending && app.Status != store.OpInfo {
			fail("Only a pending application can be approved.")
			return
		}
		nodeID := strings.TrimSpace(r.PostFormValue("node_id"))
		if !nodeSlugRe.MatchString(nodeID) {
			fail("Node slug: a-z, 0-9 and dashes, 2 to 32 characters.")
			return
		}
		name := strings.TrimSpace(r.PostFormValue("name"))
		if name == "" {
			name = app.City
		}
		key, err := s.Federation.CreateNode(r.Context(), nodeID, name, app.Country, app.Location)
		if err != nil {
			fail("Node not created: " + coreMessage(err))
			return
		}
		if err := s.Operators.DecideOperatorApplication(r.Context(), id, store.OpApproved, note, nodeID, ws.AccountID); err != nil {
			s.log.Error("application approve", "err", err)
		}
		_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.application.approve", fmt.Sprintf("%d node=%s", id, nodeID))
		// The node exists but is not approved: it is approved on the nodes
		// page once its servers report and meet the minimum.
		s.renderApplications(w, r, ws, store.OpPending, key, nodeID, "", http.StatusOK)
		return
	case "reject":
		if note == "" {
			fail("A rejection needs a reason; the applicant sees it.")
			return
		}
		err = s.Operators.DecideOperatorApplication(r.Context(), id, store.OpRejected, note, "", ws.AccountID)
	case "info":
		if note == "" {
			fail("Write the question the applicant should answer.")
			return
		}
		err = s.Operators.DecideOperatorApplication(r.Context(), id, store.OpInfo, note, "", ws.AccountID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		fail(err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "federation.application."+r.PathValue("action"), strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/admin/nodes/applications", http.StatusSeeOther)
}
