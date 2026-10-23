package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

type pagesFed struct {
	core.Federation
	locs []core.NodeLocation
}

func (f *pagesFed) NodeRequirements(context.Context) (core.NodeRequirements, error) {
	return core.NodeRequirements{NodeCores: 8, NodeRAMGB: 16, NodeDiskGB: 160, ServerCores: 2, ServerRAMGB: 4, ServerBandwidthMbps: 1000,
		MeasuredMbps: 100, AvailabilityPct: 99.5, SlackPct: 10, ReportStale: 3 * time.Minute, SelfTestMaxAge: 72 * time.Hour,
		Roles: []string{"photon", "turn", "stun", "npln_relay"}}, nil
}
func (f *pagesFed) ListLocations(context.Context) ([]core.NodeLocation, error) { return f.locs, nil }

func goodPlan() url.Values {
	v := url.Values{"location": {"sa-east"}, "city": {"São Paulo"}, "provider": {"Hetzner"}, "email": {"op@x.test"}, "terms": {"on"}}
	for _, p := range []string{"s0_", "s1_"} {
		v.Set(p+"cores", "4")
		v.Set(p+"ram", "8")
		v.Set(p+"disk", "80")
		v.Set(p+"ssd", "on")
		v.Set(p+"bw", "1000")
		v.Add(p+"roles", "photon")
	}
	return v
}

func TestOperatorApplyOnlyWhereWanted(t *testing.T) {
	s, fc, _ := newTestServer(t)
	email, _ := seedTwoAccounts(fc)
	ops := &store.MemoryOperators{}
	s.Federation = &pagesFed{}
	s.Operators = ops
	lg := login(t, s, email, fc.accounts[email])

	// Nothing open: the page offers only the waitlist.
	rec := authedRequest(s, lg, http.MethodGet, "/account/operator", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/account/operator/waitlist") || strings.Contains(rec.Body.String(), `name="location"`) {
		t.Fatalf("closed page: %d", rec.Code)
	}
	// A hand-made application for a place nobody wants is refused kindly.
	v := goodPlan()
	rec = authedRequest(s, lg, http.MethodPost, "/account/operator", &v)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "not looking for nodes there yet") {
		t.Fatalf("unwanted location: %d", rec.Code)
	}
	// The location policy: no node in mainland China or Russia, not even a waitlist row.
	for _, c := range []string{"cn", "RU"} {
		x := url.Values{"country": {c}, "email": {"op@x.test"}}
		if rec := authedRequest(s, lg, http.MethodPost, "/account/operator/waitlist", &x); rec.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(rec.Body.String(), "mainland China or Russia") {
			t.Fatalf("waitlist %s: %d", c, rec.Code)
		}
	}
	w := url.Values{"country": {"br"}, "email": {"op@x.test"}}
	if rec := authedRequest(s, lg, http.MethodPost, "/account/operator/waitlist", &w); rec.Code != http.StatusSeeOther {
		t.Fatalf("waitlist: %d", rec.Code)
	}
	if apps, _ := ops.ListOperatorApplications(context.Background(), store.OpWaitlist); len(apps) != 1 || apps[0].Country != "BR" {
		t.Fatalf("waitlist row: %+v", apps)
	}

	// Open the location: the form appears and checks the plan.
	s.Federation = &pagesFed{locs: []core.NodeLocation{{Code: "sa-east", Country: "BR", City: "São Paulo", Wanted: 1, Status: "open", Accepting: true}}}
	rec = authedRequest(s, lg, http.MethodGet, "/account/operator", nil)
	if !strings.Contains(rec.Body.String(), `value="sa-east"`) || !strings.Contains(rec.Body.String(), "99.5") {
		t.Fatalf("open page lacks the location or the figures")
	}
	short := goodPlan()
	short.Set("s1_cores", "")
	short.Del("terms")
	rec = authedRequest(s, lg, http.MethodPost, "/account/operator", &short)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Total CPU cores: 4; the minimum is 8.") ||
		!strings.Contains(body, "Accept the operator terms") || !strings.Contains(body, `value="Hetzner"`) {
		t.Fatalf("short plan: %d", rec.Code)
	}
	v = goodPlan()
	if rec := authedRequest(s, lg, http.MethodPost, "/account/operator", &v); rec.Code != http.StatusSeeOther {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	apps, _ := ops.ListOperatorApplications(context.Background(), store.OpPending)
	if len(apps) != 1 || apps[0].Country != "BR" || len(apps[0].Servers) != 2 || apps[0].TermsAt.IsZero() || apps[0].Servers[0].Roles[0] != "photon" {
		t.Fatalf("stored application: %+v", apps)
	}
	// One in review at a time; the applicant sees its status.
	v = goodPlan()
	if rec := authedRequest(s, lg, http.MethodPost, "/account/operator", &v); rec.Code != http.StatusConflict {
		t.Fatalf("second application: %d", rec.Code)
	}
	_ = ops.DecideOperatorApplication(context.Background(), apps[0].ID, store.OpInfo, "Which datacentre?", "", "acct-admin")
	rec = authedRequest(s, lg, http.MethodGet, "/account/operator", nil)
	if !strings.Contains(rec.Body.String(), "Which datacentre?") || !strings.Contains(rec.Body.String(), "/account/operator/reply") {
		t.Fatal("question not shown to the applicant")
	}
	r := url.Values{"id": {strconv.FormatInt(apps[0].ID, 10)}, "reply": {"Falkenstein"}}
	if rec := authedRequest(s, lg, http.MethodPost, "/account/operator/reply", &r); rec.Code != http.StatusSeeOther {
		t.Fatalf("reply: %d", rec.Code)
	}
	if a, _ := ops.GetOperatorApplication(context.Background(), apps[0].ID); a.Status != store.OpPending || a.Reply != "Falkenstein" {
		t.Fatalf("after reply: %+v", a)
	}
}

func TestOperatorTermsPage(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.Federation = &pagesFed{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/operators", nil))
	body := rec.Body.String()
	for _, want := range []string{"8 per node, 2 per server", "160 GB NVMe or SSD", "1000 Mbps symmetric", "99.5 %", "3 minutes", "72 hours",
		"Do not log or keep player data", "Do not modify", "at any time", "Security contact", "can be faked",
		"Data processing agreement", "within 7 days", "within 72 hours", "dedicated to OpenPak", "Fedora CoreOS", "mainland China and Russia"} {
		if !strings.Contains(body, want) {
			t.Errorf("/operators lacks %q", want)
		}
	}
}
