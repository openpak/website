package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// psSample is trimmed `podman ps -a --format json` output from a real host:
// two quadlet containers (one, like switch-nexd, whose container name lacks the
// prefix but whose unit has it), one hand-run container with the prefix, and one
// that is not ours.
const psSample = `[
  {"Id": "a1", "Names": ["openpak-website"], "Image": "ghcr.io/openpak/website:latest",
   "State": "running", "Status": "Up 7 days (healthy)", "StartedAt": 1789238737,
   "Labels": {"PODMAN_SYSTEMD_UNIT": "openpak-website.service"}},
  {"Id": "b2", "Names": ["switch-nexd"], "Image": "ghcr.io/openpak/switch-nex:latest",
   "State": "running", "Status": "Up 2 days", "StartedAt": 1789238000,
   "Labels": {"PODMAN_SYSTEMD_UNIT": "openpak-switch-nexd.service", "org.openpak.network": "switch"}},
  {"Id": "c3", "Names": ["openpak-saves-test"], "Image": "docker.io/library/postgres:18-alpine",
   "State": "created", "Status": "Created", "StartedAt": -62135596800, "Labels": null},
  {"Id": "d4", "Names": ["homepage"], "Image": "ghcr.io/gethomepage/homepage:latest",
   "State": "running", "Status": "Up 5 hours", "StartedAt": 1789238000,
   "Labels": {"PODMAN_SYSTEMD_UNIT": "homepage.service"}}
]`

// autoUpdateSample is the shape `podman auto-update --dry-run --format json`
// prints (podman 5/6): Container is "<short id> (<name>)", Updated is
// "false" or "pending" in a dry run, and pod members report the pod's unit.
const autoUpdateSample = `[
    {
        "Unit": "openpak-website.service",
        "Container": "a1b2c3d4e5f6 (openpak-website)",
        "ContainerName": "openpak-website",
        "ContainerID": "a1b2c3d4e5f6ef3aef26c39701b3add9e2b802ebe024db25c79054fa0ccf678f",
        "Image": "ghcr.io/openpak/website:latest",
        "Policy": "registry",
        "Updated": "pending"
    },
    {
        "Unit": "openpak-switch-nexd.service",
        "Container": "eb837ae6d1c7 (switch-nexd)",
        "ContainerName": "switch-nexd",
        "ContainerID": "eb837ae6d1c7ef3aef26c39701b3add9e2b802ebe024db25c79054fa0ccf678f",
        "Image": "ghcr.io/openpak/switch-nex:latest",
        "Policy": "registry",
        "Updated": "false"
    },
    {
        "Unit": "arr-pod.service",
        "Container": "aa563db5cce1 (seerr)",
        "ContainerName": "seerr",
        "ContainerID": "aa563db5cce17ba50c1c483c5a5b1a62b6484af3b9523d4e7fe9ce223ad07b23",
        "Image": "ghcr.io/seerr-team/seerr:latest",
        "Policy": "registry",
        "Updated": "pending"
    }
]`

type fakeHost struct {
	mu    sync.Mutex
	calls [][]string
	fail  string // argv prefix that returns an error
}

func (f *fakeHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	line := strings.Join(argv, " ")
	if f.fail != "" && strings.HasPrefix(line, f.fail) {
		return nil, errors.New("boom")
	}
	switch {
	case strings.HasPrefix(line, "podman ps"):
		return []byte(psSample), nil
	case strings.HasPrefix(line, "podman auto-update"):
		return []byte(autoUpdateSample), nil
	}
	return nil, nil
}

// actions returns the calls other than the list, which every action makes.
func (f *fakeHost) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if l := strings.Join(c, " "); !strings.HasPrefix(l, "podman ps") && !strings.HasPrefix(l, "podman auto-update") {
			out = append(out, l)
		}
	}
	return out
}

func newTestAgent(t *testing.T, key string) (*agent, *fakeHost) {
	t.Helper()
	f := &fakeHost{}
	return &agent{
		run:       f.run,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		key:       key,
		host:      "testhost",
		prefix:    "openpak-",
		protected: splitSet("openpak-website,openpak-account"),
	}, f
}

func do(t *testing.T, a *agent, method, path, auth string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("X-OpenPak-Actor", "admin@openpak.org")
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestAuthFailsClosed(t *testing.T) {
	a, f := newTestAgent(t, "")
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/services"},
		{"POST", "/v1/services/openpak-website/restart"},
		{"POST", "/v1/updates/check"},
	} {
		if code, _ := do(t, a, c.method, c.path, "Bearer "); code != http.StatusServiceUnavailable {
			t.Errorf("no key: %s %s = %d, want 503", c.method, c.path, code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("an unconfigured agent ran %v", f.calls)
	}

	a, _ = newTestAgent(t, "s3cret")
	for _, auth := range []string{"", "s3cret", "Bearer wrong", "Bearer s3cret2"} {
		if code, _ := do(t, a, "GET", "/v1/services", auth); code != http.StatusUnauthorized {
			t.Errorf("auth %q = %d, want 401", auth, code)
		}
	}
	if code, _ := do(t, a, "GET", "/v1/services", "Bearer s3cret"); code != http.StatusOK {
		t.Errorf("right key = %d, want 200", code)
	}

	// healthz is the one open route, even with no key configured.
	a, _ = newTestAgent(t, "")
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestServicesList(t *testing.T) {
	a, _ := newTestAgent(t, "k")
	var err error
	if a.netMap, err = parseNetworkMap([]byte(`{"^openpak-saves": "core", "^openpak-": "catch-all"}`)); err != nil {
		t.Fatal(err)
	}
	a.checkUpdates(context.Background())

	code, body := do(t, a, "GET", "/v1/services", "Bearer k")
	if code != 200 {
		t.Fatalf("status %d: %v", code, body)
	}
	if body["host"] != "testhost" || body["checked_at"] == nil {
		t.Errorf("host/checked_at = %v / %v", body["host"], body["checked_at"])
	}
	raw, _ := json.Marshal(body["services"])
	var got []service
	json.Unmarshal(raw, &got)

	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	if want := []string{"openpak-saves-test", "openpak-website", "switch-nexd"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v (prefix filter, sorted)", names, want)
	}
	saves, web, nexd := got[0], got[1], got[2]

	if web.Unit != "openpak-website.service" || web.Health != "healthy" || !web.Protected || !web.UpdateAvailable {
		t.Errorf("website = %+v", web)
	}
	if web.StartedAt == nil || *web.StartedAt != "2026-09-12T18:45:37Z" {
		t.Errorf("website started_at = %v", web.StartedAt)
	}
	if !reflect.DeepEqual(nexd.Networks, []string{"switch"}) || nexd.Health != "none" || nexd.UpdateAvailable || nexd.Protected {
		t.Errorf("switch-nexd = %+v", nexd)
	}
	// Label absent: the map decides, in file order.
	if !reflect.DeepEqual(saves.Networks, []string{"core"}) || saves.Unit != "" || saves.StartedAt != nil {
		t.Errorf("saves-test = %+v", saves)
	}
}

func TestNetworks(t *testing.T) {
	a, _ := newTestAgent(t, "k")
	if got := a.networks("openpak-nn-boss", " wiiu, 3ds ,"); !reflect.DeepEqual(got, []string{"wiiu", "3ds"}) {
		t.Errorf("label list = %v", got)
	}
	if got := a.networks("openpak-nn-boss", ""); !reflect.DeepEqual(got, []string{"unassigned"}) {
		t.Errorf("no label, no map = %v", got)
	}
	a.netMap, _ = parseNetworkMap([]byte(`{"^openpak-nn-": "wiiu", "boss": "never"}`))
	if got := a.networks("openpak-nn-boss", ""); !reflect.DeepEqual(got, []string{"wiiu"}) {
		t.Errorf("map, first match = %v", got)
	}
	if got := a.networks("openpak-nn-boss", "3ds"); !reflect.DeepEqual(got, []string{"3ds"}) {
		t.Errorf("label beats map = %v", got)
	}
	a.netMap, _ = parseNetworkMap([]byte(`{"^openpak-nn-friends$": "wiiu, 3ds"}`))
	if got := a.networks("openpak-nn-friends", ""); !reflect.DeepEqual(got, []string{"wiiu", "3ds"}) {
		t.Errorf("map value list = %v", got)
	}
	if _, err := parseNetworkMap([]byte(`{"(": "x"}`)); err == nil {
		t.Error("bad regexp accepted")
	}
	if _, err := parseNetworkMap([]byte(`["x"]`)); err == nil {
		t.Error("non-object accepted")
	}
}

func TestParseAutoUpdate(t *testing.T) {
	got, err := parseAutoUpdate([]byte(autoUpdateSample))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"openpak-website": true, "seerr": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("pending = %v, want %v", got, want)
	}
	if got, err := parseAutoUpdate([]byte("\n")); err != nil || len(got) != 0 {
		t.Errorf("empty output = %v, %v", got, err)
	}
	if _, err := parseAutoUpdate([]byte("Error: bad")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestActions(t *testing.T) {
	cases := []struct {
		path string
		code int
		want []string
	}{
		{"/v1/services/switch-nexd/restart", 200, []string{"systemctl --user restart openpak-switch-nexd.service"}},
		{"/v1/services/switch-nexd/stop", 200, []string{"systemctl --user stop openpak-switch-nexd.service"}},
		{"/v1/services/switch-nexd/start", 200, []string{"systemctl --user start openpak-switch-nexd.service"}},
		{"/v1/services/openpak-website/update", 200, []string{
			"podman pull ghcr.io/openpak/website:latest",
			"systemctl --user restart openpak-website.service",
		}},
		{"/v1/services/openpak-website/restart", 200, []string{"systemctl --user restart openpak-website.service"}},
		// Protected: never stopped.
		{"/v1/services/openpak-website/stop", 409, nil},
		// Not systemd-managed: listed, but hands off.
		{"/v1/services/openpak-saves-test/restart", 409, nil},
		// Outside the prefix, or unknown: not ours.
		{"/v1/services/homepage/restart", 404, nil},
		{"/v1/services/openpak-nope/restart", 404, nil},
		{"/v1/services/switch-nexd/rm", 404, nil},
	}
	for _, c := range cases {
		a, f := newTestAgent(t, "k")
		code, body := do(t, a, "POST", c.path, "Bearer k")
		if code != c.code || body["ok"] != (c.code == 200) || body["message"] == "" {
			t.Errorf("%s = %d %v, want %d", c.path, code, body, c.code)
		}
		if got := f.actions(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s ran %q, want %q", c.path, got, c.want)
		}
	}
}

func TestUpdateStopsAtFailedPull(t *testing.T) {
	a, f := newTestAgent(t, "k")
	f.fail = "podman pull"
	code, _ := do(t, a, "POST", "/v1/services/openpak-website/update", "Bearer k")
	if code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if got := f.actions(); len(got) != 1 {
		t.Errorf("restarted after a failed pull: %q", got)
	}
}

func TestActionBusyUnit(t *testing.T) {
	a, f := newTestAgent(t, "k")
	lock := a.unitLock("openpak-switch-nexd.service")
	lock.Lock()
	code, _ := do(t, a, "POST", "/v1/services/switch-nexd/restart", "Bearer k")
	lock.Unlock()
	if code != http.StatusConflict || len(f.actions()) != 0 {
		t.Errorf("busy unit: %d, ran %q", code, f.actions())
	}
}

func TestUpdatesCheck(t *testing.T) {
	a, _ := newTestAgent(t, "k")
	code, body := do(t, a, "POST", "/v1/updates/check", "Bearer k")
	if code != http.StatusAccepted || body["ok"] != true {
		t.Errorf("check = %d %v", code, body)
	}
}
