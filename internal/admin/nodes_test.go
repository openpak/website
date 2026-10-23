package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// adminFed is the core's node register as far as the admin pages use it.
type adminFed struct {
	core.Federation
	nodes    []core.Node
	locs     []core.NodeLocation
	created  []string // "id country region"
	waiver   string
	revoked  string
	approved map[string]bool
}

func (f *adminFed) ListAllNodes(context.Context) ([]core.Node, error) { return f.nodes, nil }
func (f *adminFed) ListLocations(context.Context) ([]core.NodeLocation, error) {
	return f.locs, nil
}
func (f *adminFed) NodeRequirements(context.Context) (core.NodeRequirements, error) {
	return core.NodeRequirements{NodeCores: 8, NodeRAMGB: 16, NodeDiskGB: 160, ServerCores: 2, ServerRAMGB: 4, ServerBandwidthMbps: 1000,
		MeasuredMbps: 100, ReportStale: 3 * time.Minute, SelfTestMaxAge: 72 * time.Hour}, nil
}
func (f *adminFed) CreateNode(_ context.Context, id, _, country, region string) (string, error) {
	f.created = append(f.created, id+" "+country+" "+region)
	return "opnode_shown_once", nil
}
func (f *adminFed) SetNodeApproved(_ context.Context, id string, approved bool) error {
	if approved && id == "short" {
		return fmt.Errorf("%w: federation: node is below the minimum: total CPU cores 2, minimum 8", core.ErrConflict)
	}
	f.approved[id] = approved
	return nil
}
func (f *adminFed) SetNodeWaiver(_ context.Context, id, reason string, until time.Time) error {
	f.waiver = id + "|" + reason + "|" + until.UTC().Format("2006-01-02")
	return nil
}
func (f *adminFed) RevokeServer(_ context.Context, id string) error { f.revoked = id; return nil }
func (f *adminFed) PutLocation(_ context.Context, l core.NodeLocation) error {
	f.locs = append(f.locs, l)
	return nil
}

func nodesAdmin(t *testing.T) (*http.ServeMux, *adminFed, *store.Memory, *store.MemoryOperators) {
	t.Helper()
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root"})
	f := &adminFed{approved: map[string]bool{}, nodes: []core.Node{{
		ID: "us-test", Name: "US test", Country: "US", Region: "us-east", Approved: true, InRotation: true,
		WaiverReason: "owner test node", Waived: true, TotalCores: 1,
		Servers: []core.NodeServer{{ID: "us-test/vps", Name: "vps", Services: map[string]string{"turn": "198.51.100.7:3478"},
			Report:   core.ServerReport{Cores: 1, RAMMB: 1024, DiskGB: 25, DiskSSD: true, IPv4: "198.51.100.7", PortMbps: 1000},
			DownMbps: 412, DownAt: time.Now(), UpMbps: 380, UpAt: time.Now(), RTTMs: 140, LastSeen: time.Now()}},
	}, {ID: "short", Name: "Short", Country: "AU", Problems: []string{"total CPU cores 2, minimum 8"}}}}
	s.Federation = f
	ops := &store.MemoryOperators{}
	s.Operators = ops
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return mux, f, db, ops
}

func TestNodesPageShowsServersWaiverAndRules(t *testing.T) {
	mux, _, _, _ := nodesAdmin(t)
	rec := get(mux, "/admin/nodes")
	body := rec.Body.String()
	for _, want := range []string{"Minimums waived", "owner test node", "In rotation", "Out of rotation", "us-east",
		"down 412 Mbps", "RTT 140 ms (server-reported)", "198.51.100.7", "can be faked", "total CPU cores 2, minimum 8", "/admin/nodes/servers/revoke"} {
		if !strings.Contains(body, want) {
			t.Errorf("nodes page lacks %q", want)
		}
	}
}

func TestApproveRefusalNamesTheShortfall(t *testing.T) {
	mux, f, db, _ := nodesAdmin(t)
	rec := postForm(mux, "/admin/nodes/short/approve", url.Values{"csrf": {"tok-sess-admin"}, "approved": {"true"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "total CPU cores 2, minimum 8") || f.approved["short"] {
		t.Fatalf("refused approval: %d", rec.Code)
	}
	if rec := postForm(mux, "/admin/nodes/us-test/approve", url.Values{"csrf": {"tok-sess-admin"}, "approved": {"true"}}); rec.Code != http.StatusSeeOther || !f.approved["us-test"] {
		t.Fatalf("approval: %d", rec.Code)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "federation.node.approve" {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestWaiverNeedsAReasonAndIsAudited(t *testing.T) {
	mux, f, db, _ := nodesAdmin(t)
	if rec := postForm(mux, "/admin/nodes/us-test/waiver", url.Values{"csrf": {"tok-sess-admin"}, "reason": {""}}); rec.Code != http.StatusBadRequest || f.waiver != "" {
		t.Fatalf("waiver without reason: %d %q", rec.Code, f.waiver)
	}
	rec := postForm(mux, "/admin/nodes/us-test/waiver", url.Values{"csrf": {"tok-sess-admin"}, "reason": {"owner test node"}, "until": {"2026-12-31"}})
	if rec.Code != http.StatusSeeOther || f.waiver != "us-test|owner test node|2026-12-31" {
		t.Fatalf("waiver: %d %q", rec.Code, f.waiver)
	}
	audit, _ := db.ListAudit(context.Background(), 10)
	if len(audit) != 1 || audit[0].Action != "federation.node.waiver" || !strings.Contains(audit[0].Detail, "owner test node") {
		t.Fatalf("audit: %+v", audit)
	}
	if rec := postForm(mux, "/admin/nodes/servers/revoke", url.Values{"csrf": {"tok-sess-admin"}, "server": {"us-test/vps"}}); rec.Code != http.StatusSeeOther || f.revoked != "us-test/vps" {
		t.Fatalf("revoke server: %d", rec.Code)
	}
}

func TestApplicationReview(t *testing.T) {
	mux, f, _, ops := nodesAdmin(t)
	app, _ := ops.CreateOperatorApplication(context.Background(), store.OperatorApplication{AccountID: "acct-op", Email: "op@x.test",
		Location: "sa-east", Country: "BR", City: "São Paulo", Provider: "Hetzner", TermsAt: time.Now(),
		Servers: []store.OperatorServerPlan{{Cores: 8, RAMGB: 16, DiskGB: 200, DiskSSD: true, BandwidthMbps: 1000, Roles: []string{"photon"}}}})
	if rec := get(mux, "/admin/nodes/applications"); !strings.Contains(rec.Body.String(), "São Paulo") || !strings.Contains(rec.Body.String(), "Hetzner") {
		t.Fatalf("pending list: %s", rec.Body)
	}
	id := fmt.Sprint(app.ID)
	if rec := postForm(mux, "/admin/nodes/applications/"+id+"/reject", url.Values{"csrf": {"tok-sess-admin"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reject without reason: %d", rec.Code)
	}
	if rec := postForm(mux, "/admin/nodes/applications/"+id+"/info", url.Values{"csrf": {"tok-sess-admin"}, "note": {"Which datacentre?"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("ask: %d", rec.Code)
	}
	if a, _ := ops.GetOperatorApplication(context.Background(), app.ID); a.Status != store.OpInfo || a.AdminNote != "Which datacentre?" {
		t.Fatalf("after ask: %+v", a)
	}
	rec := postForm(mux, "/admin/nodes/applications/"+id+"/approve", url.Values{"csrf": {"tok-sess-admin"}, "node_id": {"br-sao"}, "name": {"São Paulo"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "opnode_shown_once") || len(f.created) != 1 || f.created[0] != "br-sao BR sa-east" {
		t.Fatalf("approve: %d %v", rec.Code, f.created)
	}
	if a, _ := ops.GetOperatorApplication(context.Background(), app.ID); a.Status != store.OpApproved || a.NodeID != "br-sao" {
		t.Fatalf("after approve: %+v", a)
	}
	// The key was in that one response; the list does not carry it again.
	if rec := get(mux, "/admin/nodes/applications?status="); strings.Contains(rec.Body.String(), "opnode_shown_once") {
		t.Fatal("key shown twice")
	}
}

func TestLocationsPage(t *testing.T) {
	mux, f, _, _ := nodesAdmin(t)
	if rec := get(mux, "/admin/nodes/locations"); !strings.Contains(rec.Body.String(), "No locations yet") {
		t.Fatalf("empty plan: %s", rec.Body)
	}
	rec := postForm(mux, "/admin/nodes/locations", url.Values{"csrf": {"tok-sess-admin"}, "code": {"sa-east"}, "country": {"BR"}, "city": {"São Paulo"}, "wanted": {"2"}, "status": {"open"}})
	if rec.Code != http.StatusSeeOther || len(f.locs) != 1 || f.locs[0].Wanted != 2 {
		t.Fatalf("save: %d %+v", rec.Code, f.locs)
	}
}
