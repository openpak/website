package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// runner executes one host command and returns its stdout. Every podman and
// systemctl call goes through it — the one seam in this binary, so the tests
// can assert the exact argv without a podman on the machine.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec deadlines. A hung podman must not pin a request (or the unit's lock)
// forever; the pull gets the long one because a cold image is hundreds of MB.
const (
	listTimeout   = 30 * time.Second
	unitTimeout   = 2 * time.Minute
	pullTimeout   = 2 * time.Minute
	updateTimeout = 5 * time.Minute // one registry round trip per container
)

type netRule struct {
	re      *regexp.Regexp
	network string
}

type agent struct {
	run       runner
	log       *slog.Logger
	key       string
	host      string
	prefix    string
	protected map[string]bool
	netMap    []netRule

	// Update cache. Keyed by container name, not unit: auto-update reports a
	// pod's containers under the pod's unit, and the container ID changes
	// every time quadlet recreates the container.
	mu        sync.Mutex
	checkedAt time.Time
	pending   map[string]bool
	checking  sync.Mutex // one dry run at a time; extra requests are dropped

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex // per unit
}

func newAgentFromEnv(log *slog.Logger) (*agent, error) {
	host := os.Getenv("OPSD_HOST_NAME")
	if host == "" {
		host, _ = os.Hostname()
	}
	a := &agent{
		run:       execRun,
		log:       log,
		key:       os.Getenv("OPSD_KEY"),
		host:      host,
		prefix:    envOr("OPSD_PREFIX", "openpak-"),
		protected: splitSet(envOr("OPSD_PROTECTED", "openpak-website,openpak-account")),
	}
	if path := os.Getenv("OPSD_NETWORK_MAP"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("OPSD_NETWORK_MAP: %w", err)
		}
		if a.netMap, err = parseNetworkMap(raw); err != nil {
			return nil, fmt.Errorf("OPSD_NETWORK_MAP %s: %w", path, err)
		}
	}
	return a, nil
}

func splitSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

// parseNetworkMap reads {"<regexp>": "network", ...} keeping the file's
// order, because "first match wins" is meaningless after a Go map has
// shuffled the keys.
func parseNetworkMap(raw []byte) ([]netRule, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("want a JSON object of regexp to network")
	}
	var rules []netRule
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		pattern := t.(string) // object keys are always strings
		var network string
		if err := dec.Decode(&network); err != nil {
			return nil, fmt.Errorf("%q: value must be a string", pattern)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		rules = append(rules, netRule{re, network})
	}
	return rules, nil
}

// --- discovery ---

// psEntry is the part of `podman ps -a --format json` this agent reads.
type psEntry struct {
	ID        string            `json:"Id"`
	Names     []string          `json:"Names"`
	Image     string            `json:"Image"`
	State     string            `json:"State"`
	Status    string            `json:"Status"`
	StartedAt int64             `json:"StartedAt"`
	Labels    map[string]string `json:"Labels"`
}

type service struct {
	Name            string   `json:"name"`
	Unit            string   `json:"unit"`
	Image           string   `json:"image"`
	State           string   `json:"state"`
	Health          string   `json:"health"`
	StartedAt       *string  `json:"started_at"`
	Networks        []string `json:"networks"`
	UpdateAvailable bool     `json:"update_available"`
	Protected       bool     `json:"protected"`
}

func (a *agent) services(ctx context.Context) ([]service, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := a.run(ctx, "podman", "ps", "-a", "--format", "json")
	if err != nil {
		return nil, err
	}
	var entries []psEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("podman ps: %w", err)
	}
	a.mu.Lock()
	pending := a.pending
	a.mu.Unlock()

	var list []service
	for _, e := range entries {
		// Ours if the container or its quadlet unit carries the prefix: switch-nexd's
		// container is named without it, but its unit is openpak-switch-nexd.service.
		if len(e.Names) == 0 || !(strings.HasPrefix(e.Names[0], a.prefix) ||
			strings.HasPrefix(e.Labels["PODMAN_SYSTEMD_UNIT"], a.prefix)) {
			continue
		}
		s := service{
			Name:            e.Names[0],
			Unit:            e.Labels["PODMAN_SYSTEMD_UNIT"],
			Image:           e.Image,
			State:           e.State,
			Health:          health(e.Status),
			Networks:        a.networks(e.Names[0], e.Labels["org.openpak.network"]),
			UpdateAvailable: pending[e.Names[0]],
		}
		s.Protected = a.isProtected(s.Name, s.Unit)
		// Podman reports a never-started container as year 1 (negative).
		if e.StartedAt > 0 {
			t := time.Unix(e.StartedAt, 0).UTC().Format(time.RFC3339)
			s.StartedAt = &t
		}
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// health reads the suffix podman appends to Status ("Up 7 days (healthy)").
// ps has no separate health field, and inspecting every container to get one
// would cost a process per row.
func health(status string) string {
	for _, h := range []string{"unhealthy", "healthy", "starting"} {
		if strings.HasSuffix(status, "("+h+")") {
			return h
		}
	}
	return "none"
}

func (a *agent) networks(name, label string) []string {
	if nets := splitList(label); len(nets) > 0 {
		return nets
	}
	// A map value may name several networks too ("wiiu,3ds"), like the label.
	for _, r := range a.netMap {
		if r.re.MatchString(name) {
			if nets := splitList(r.network); len(nets) > 0 {
				return nets
			}
		}
	}
	return []string{"unassigned"}
}

func splitList(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// isProtected accepts either the container name or the unit's base name, so
// OPSD_PROTECTED can be written the way the operator thinks of the service.
func (a *agent) isProtected(name, unit string) bool {
	return a.protected[name] || (unit != "" && a.protected[strings.TrimSuffix(unit, ".service")])
}

// --- updates ---

// autoUpdateEntry is one row of `podman auto-update --dry-run --format json`.
type autoUpdateEntry struct {
	Unit          string `json:"Unit"`
	Container     string `json:"Container"`
	ContainerName string `json:"ContainerName"`
	ContainerID   string `json:"ContainerID"`
	Image         string `json:"Image"`
	Policy        string `json:"Policy"`
	Updated       string `json:"Updated"` // "pending" in a dry run when the registry has newer
}

func parseAutoUpdate(raw []byte) (map[string]bool, error) {
	var rows []autoUpdateEntry
	// Guard an empty stdout (no auto-update containers) so it reads as "none
	// pending" rather than a parse error.
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]bool{}, nil
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("podman auto-update: %w", err)
	}
	pending := map[string]bool{}
	for _, r := range rows {
		if r.Updated == "pending" {
			pending[r.ContainerName] = true
		}
	}
	return pending, nil
}

// checkUpdates runs the dry run and replaces the cache. A check already in
// flight makes this a no-op: the answer it brings back is just as fresh.
func (a *agent) checkUpdates(ctx context.Context) {
	if !a.checking.TryLock() {
		return
	}
	defer a.checking.Unlock()
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	out, err := a.run(ctx, "podman", "auto-update", "--dry-run", "--format", "json")
	if err != nil {
		a.log.Warn("update check failed", "err", err)
		return
	}
	pending, err := parseAutoUpdate(out)
	if err != nil {
		a.log.Warn("update check failed", "err", err)
		return
	}
	a.mu.Lock()
	a.pending, a.checkedAt = pending, time.Now().UTC()
	a.mu.Unlock()
	a.log.Info("update check done", "pending", len(pending))
}

func (a *agent) updateLoop(ctx context.Context, every time.Duration) {
	a.checkUpdates(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.checkUpdates(ctx)
		}
	}
}

// --- actions ---

// httpError carries the status an action failure should be answered with.
type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func (a *agent) unitLock(unit string) *sync.Mutex {
	a.locksMu.Lock()
	defer a.locksMu.Unlock()
	if a.locks == nil {
		a.locks = map[string]*sync.Mutex{}
	}
	m, ok := a.locks[unit]
	if !ok {
		m = &sync.Mutex{}
		a.locks[unit] = m
	}
	return m
}

func (a *agent) act(ctx context.Context, name, action string) (string, error) {
	switch action {
	case "restart", "stop", "start", "update":
	default:
		return "", &httpError{http.StatusNotFound, "unknown action " + action}
	}
	// Re-read the list rather than trusting anything the caller sent: the
	// name must be a container this agent manages, and the unit and image
	// come from podman, never from the request.
	list, err := a.services(ctx)
	if err != nil {
		return "", &httpError{http.StatusBadGateway, err.Error()}
	}
	var svc *service
	for i := range list {
		if list[i].Name == name {
			svc = &list[i]
		}
	}
	switch {
	case svc == nil:
		return "", &httpError{http.StatusNotFound, "no managed container named " + name}
	case svc.Unit == "":
		return "", &httpError{http.StatusConflict, name + " is not systemd-managed (no PODMAN_SYSTEMD_UNIT label); manage it by hand"}
	case action == "stop" && svc.Protected:
		return "", &httpError{http.StatusConflict, name + " is protected: it can be restarted or updated, never stopped"}
	}

	// One action per unit at a time. A second click while the first is
	// running is refused rather than queued: it is almost always a
	// duplicate, and a queued stop behind an update is a surprise.
	lock := a.unitLock(svc.Unit)
	if !lock.TryLock() {
		return "", &httpError{http.StatusConflict, "another action on " + svc.Unit + " is still running"}
	}
	defer lock.Unlock()

	if action == "update" {
		pctx, cancel := context.WithTimeout(ctx, pullTimeout)
		_, err := a.run(pctx, "podman", "pull", svc.Image)
		cancel()
		if err != nil {
			return "", &httpError{http.StatusBadGateway, "pull failed: " + err.Error()}
		}
		action = "restart"
	}
	uctx, cancel := context.WithTimeout(ctx, unitTimeout)
	defer cancel()
	if _, err := a.run(uctx, "systemctl", "--user", action, svc.Unit); err != nil {
		return "", &httpError{http.StatusBadGateway, err.Error()}
	}
	return svc.Unit + ": " + action + " done", nil
}

// --- HTTP ---

func (a *agent) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /v1/services", a.auth(http.HandlerFunc(a.handleServices)))
	mux.Handle("POST /v1/services/{name}/{action}", a.auth(http.HandlerFunc(a.handleAction)))
	mux.Handle("POST /v1/updates/check", a.auth(http.HandlerFunc(a.handleCheck)))
	return mux
}

// auth is the same fail-closed bearer check as the website's internal
// surface: no key configured means nobody gets in, not everybody.
func (a *agent) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.key == "" {
			reply(w, http.StatusServiceUnavailable, false, "OPSD_KEY is not configured")
			return
		}
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+a.key)) != 1 {
			reply(w, http.StatusUnauthorized, false, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *agent) handleServices(w http.ResponseWriter, r *http.Request) {
	list, err := a.services(r.Context())
	if err != nil {
		a.log.Error("list services", "err", err)
		reply(w, http.StatusBadGateway, false, err.Error())
		return
	}
	if list == nil {
		list = []service{} // "services": [] for the UI, never null
	}
	a.mu.Lock()
	var checked *string
	if !a.checkedAt.IsZero() {
		t := a.checkedAt.Format(time.RFC3339)
		checked = &t
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"host": a.host, "checked_at": checked, "services": list})
}

func (a *agent) handleAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	actor := actorOf(r)
	// Detached from the request: a closed browser tab must not kill a pull
	// or a restart halfway. The exec deadlines still bound it.
	ctx := context.WithoutCancel(r.Context())
	a.log.Info("action requested", "service", name, "action", action, "actor", actor, "remote", r.RemoteAddr)
	msg, err := a.act(ctx, name, action)
	if err != nil {
		code := http.StatusInternalServerError
		var he *httpError
		if errors.As(err, &he) {
			code = he.code
		}
		a.log.Info("action refused or failed", "service", name, "action", action, "actor", actor, "status", code, "err", err)
		reply(w, code, false, err.Error())
		return
	}
	a.log.Info("action done", "service", name, "action", action, "actor", actor)
	if action == "update" {
		// The container now runs the new image; clear its flag now so the
		// dashboard does not show a stale badge, then confirm with a real
		// check in the background.
		a.mu.Lock()
		if a.pending[name] {
			p := make(map[string]bool, len(a.pending))
			for k, v := range a.pending {
				p[k] = v
			}
			delete(p, name)
			a.pending = p
		}
		a.mu.Unlock()
		go a.checkUpdates(context.Background())
	}
	reply(w, http.StatusOK, true, msg)
}

func (a *agent) handleCheck(w http.ResponseWriter, r *http.Request) {
	a.log.Info("update check requested", "actor", actorOf(r))
	go a.checkUpdates(context.Background())
	reply(w, http.StatusAccepted, true, "update check started")
}

// actorOf is who the website says clicked. Informational only — the key is
// the authentication — so it is logged, capped, and never trusted.
func actorOf(r *http.Request) string {
	a := r.Header.Get("X-OpenPak-Actor")
	if len(a) > 100 {
		a = a[:100]
	}
	return a
}

func reply(w http.ResponseWriter, code int, ok bool, msg string) {
	writeJSON(w, code, map[string]any{"ok": ok, "message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
