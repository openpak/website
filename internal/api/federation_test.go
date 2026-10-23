package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
)

// fakeFed implements the federation calls the API relays; anything else
// panics through the nil embedded interface.
type fakeFed struct {
	core.Federation
	observed  string
	report    core.ServerReport
	started   map[string]bool
	recorded  map[string]float64
	rtt       int
	selfBytes int64
}

func (f *fakeFed) JoinServerAs(_ context.Context, nodeKey, name string) (string, string, error) {
	if nodeKey != "opnode_good" {
		return "", "", fmt.Errorf("core: internal credentials rejected: x")
	}
	return "au-sydney/" + name, "opsrv_new", nil
}

func (f *fakeFed) AnnounceAs(_ context.Context, key string, _ map[string]string, rep core.ServerReport, observed string) (core.AnnounceResult, error) {
	f.observed, f.report = observed, rep
	return core.AnnounceResult{NodeID: "au-sydney", ServerID: "au-sydney/game1", Problems: []string{"total CPU cores 2, minimum 8"}, SelfTestDue: true}, nil
}

func (f *fakeFed) StartSelfTestAs(_ context.Context, _, dir string) (string, int64, error) {
	if f.started[dir] {
		return "", 0, fmt.Errorf("%w: self-test already run; next one after 2026-10-24T00:00:00Z", core.ErrTooSoon)
	}
	f.started[dir] = true
	return "au-sydney/game1", f.selfBytes, nil
}

func (f *fakeFed) RecordSelfTest(_ context.Context, id, dir string, mbps float64, rtt int) error {
	f.recorded[dir] = mbps
	f.rtt = rtt
	return nil
}

func TestFederationServerDoors(t *testing.T) {
	srv, _, _ := newTestAPI(t)
	ff := &fakeFed{started: map[string]bool{}, recorded: map[string]float64{}, selfBytes: 1 << 20}
	srv.Federation = ff

	if rec := do(t, srv, http.MethodPost, "/api/v1/federation/join", "opnode_good", map[string]string{"name": "game1"}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"key":"opsrv_new"`) {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv, http.MethodPost, "/api/v1/federation/join", "opnode_bad", map[string]string{"name": "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("join with unknown key: %d", rec.Code)
	}
	// A node key cannot announce: it is told to join first.
	if rec := do(t, srv, http.MethodPost, "/api/v1/federation/announce", "opnode_good", map[string]any{}); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "join") {
		t.Fatalf("node key announcing: %d %s", rec.Code, rec.Body)
	}
	rec := do(t, srv, http.MethodPost, "/api/v1/federation/announce", "opsrv_x", map[string]any{
		"services": map[string]string{"photon": "1.2.3.4:22002"},
		"report":   map[string]any{"cores": 2, "ram_mb": 4096, "disk_gb": 40, "disk_ssd": true, "ipv4": "1.2.3.4", "port_mbps": 1000},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"selftest_due":true`) || !strings.Contains(rec.Body.String(), "minimum 8") {
		t.Fatalf("announce: %d %s", rec.Code, rec.Body)
	}
	if ff.report.Cores != 2 || ff.report.IPv4 != "1.2.3.4" || ff.observed == "" {
		t.Fatalf("report or observed address not relayed: %+v %q", ff.report, ff.observed)
	}

	// Download leg: home streams the reserved bytes and records the rate itself.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/selftest", nil)
	req.Header.Set("Authorization", "Bearer opsrv_x")
	got := httptest.NewRecorder()
	mux.ServeHTTP(got, req)
	if got.Code != http.StatusOK || got.Body.Len() != 1<<20 || ff.recorded["down"] <= 0 {
		t.Fatalf("download leg: %d %d bytes, recorded %v", got.Code, got.Body.Len(), ff.recorded)
	}
	// Upload leg: a short body records nothing; the full one records, with the RTT.
	ff.started["up"] = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/federation/selftest", bytes.NewReader(make([]byte, 1000)))
	req.Header.Set("Authorization", "Bearer opsrv_x")
	got = httptest.NewRecorder()
	mux.ServeHTTP(got, req)
	if got.Code != http.StatusBadRequest || ff.recorded["up"] != 0 {
		t.Fatalf("short upload: %d %v", got.Code, ff.recorded)
	}
	ff.started["up"] = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/federation/selftest", bytes.NewReader(make([]byte, 1<<20)))
	req.Header.Set("Authorization", "Bearer opsrv_x")
	req.Header.Set("X-OpenPak-RTT-Ms", "187")
	got = httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(got, req)
	if got.Code != http.StatusOK || ff.recorded["up"] <= 0 || ff.rtt != 187 || time.Since(start) > 10*time.Second {
		t.Fatalf("upload leg: %d %s %v rtt=%d", got.Code, got.Body, ff.recorded, ff.rtt)
	}
	// Once a day: the second try in the interval is 429 with the core's words.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/federation/selftest", nil)
	req.Header.Set("Authorization", "Bearer opsrv_x")
	got = httptest.NewRecorder()
	mux.ServeHTTP(got, req)
	if got.Code != http.StatusTooManyRequests || !strings.Contains(got.Body.String(), "next one after") {
		t.Fatalf("second self-test: %d %s", got.Code, got.Body)
	}
}

func (f *fakeFed) ListNodes(context.Context) ([]core.Node, error) {
	return []core.Node{{ID: "us-test", Country: "US", Region: "us-east", Approved: true, InRotation: true,
		WaiverReason: "owner test node", Waived: true, Servers: []core.NodeServer{{ID: "us-test/a"}}}}, nil
}

// The public list carries the region and never the waiver or server detail.
func TestRegionsHideWaiver(t *testing.T) {
	srv, _, _ := newTestAPI(t)
	srv.Federation = &fakeFed{}
	rec := do(t, srv, http.MethodGet, "/api/v1/regions", "", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"region":"us-east"`) || strings.Contains(body, "waiver") ||
		strings.Contains(body, "owner test") || strings.Contains(body, "us-test/a") {
		t.Fatalf("regions: %d %s", rec.Code, body)
	}
}
