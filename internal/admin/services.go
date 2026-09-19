package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// OpsAPI talks to openpak-opsd, the host agent that owns the containers
// (docs/opsd.md). The website never gets the podman socket: every read and
// every restart goes through the agent on that host, over its keyed API.
type OpsAPI struct {
	Hosts []OpsHost
	Key   string

	// The nav badge ("N updates") is on every admin page, so the count is
	// cached rather than asking every host on every render.
	mu      sync.Mutex
	pending int
	fetched time.Time
}

// OpsHost is one host's agent.
type OpsHost struct{ Name, URL string }

// NewOpsAPI parses WEBSITE_OPSD_HOSTS ("prod=http://host:20013,draughts=...")
// and returns nil when no host or no key is configured.
func NewOpsAPI(hosts, key string) *OpsAPI {
	var hs []OpsHost
	for _, h := range strings.Split(hosts, ",") {
		name, u, ok := strings.Cut(strings.TrimSpace(h), "=")
		if ok && name != "" && u != "" {
			hs = append(hs, OpsHost{Name: name, URL: strings.TrimRight(u, "/")})
		}
	}
	if len(hs) == 0 || key == "" {
		return nil
	}
	return &OpsAPI{Hosts: hs, Key: key}
}

// opsService is one container as the agent reports it (GET /v1/services).
type opsService struct {
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

type opsHostView struct {
	Name      string
	Error     string
	CheckedAt *string
	Services  []opsService
}

// list asks every host at once; one host being down is that host's row, not
// an error page.
func (o *OpsAPI) list(ctx context.Context) []opsHostView {
	out := make([]opsHostView, len(o.Hosts))
	var wg sync.WaitGroup
	for i, h := range o.Hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var body struct {
				CheckedAt *string      `json:"checked_at"`
				Services  []opsService `json:"services"`
			}
			v := opsHostView{Name: h.Name}
			if err := getJSON(ctx, h.URL+"/v1/services", o.Key, &body); err != nil {
				v.Error = err.Error()
			} else {
				v.CheckedAt, v.Services = body.CheckedAt, body.Services
				sort.Slice(v.Services, func(a, b int) bool { return v.Services[a].Name < v.Services[b].Name })
			}
			out[i] = v
		}()
	}
	wg.Wait()
	o.remember(out)
	return out
}

func (o *OpsAPI) remember(hosts []opsHostView) {
	n := 0
	for _, h := range hosts {
		for _, s := range h.Services {
			if s.UpdateAvailable {
				n++
			}
		}
	}
	o.mu.Lock()
	o.pending, o.fetched = n, time.Now()
	o.mu.Unlock()
}

// Pending is the update count for the nav badge. A stale count is refreshed
// with a short timeout; if the hosts are slow the page renders with the last
// known number rather than waiting on them.
func (o *OpsAPI) Pending(ctx context.Context) int {
	o.mu.Lock()
	fresh := time.Since(o.fetched) < time.Minute
	n := o.pending
	o.mu.Unlock()
	if fresh {
		return n
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	o.list(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pending
}

func (o *OpsAPI) host(name string) (OpsHost, bool) {
	for _, h := range o.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return OpsHost{}, false
}

// post sends one action to a host's agent and returns its message. The actor
// header lands in the agent's own log, so the host records who pressed it too.
func (o *OpsAPI) post(ctx context.Context, h OpsHost, path, actor string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL+path, bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+o.Key)
	req.Header.Set("X-OpenPak-Actor", actor)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	if resp.StatusCode/100 != 2 {
		if body.Message == "" {
			body.Message = resp.Status
		}
		return "", fmt.Errorf("%s: %s", h.Name, body.Message)
	}
	return body.Message, nil
}

var opsActions = []string{"restart", "start", "stop", "update"}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.renderServices(w, r, ws, "", http.StatusOK)
}

func (s *Server) renderServices(w http.ResponseWriter, r *http.Request, ws store.WebSession, errMsg string, status int) {
	network, host := r.URL.Query().Get("network"), r.URL.Query().Get("host")
	var hosts []opsHostView
	var networks, hostNames []string
	if s.Ops != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		all := s.Ops.list(ctx)
		seen := map[string]bool{}
		for _, h := range all {
			hostNames = append(hostNames, h.Name)
			for _, svc := range h.Services {
				for _, n := range svc.Networks {
					if !seen[n] {
						seen[n] = true
						networks = append(networks, n)
					}
				}
			}
		}
		sort.Strings(networks)
		// The dropdowns narrow the view; the agents are asked for everything
		// either way, so the badge count stays whole.
		for _, h := range all {
			if host != "" && h.Name != host {
				continue
			}
			if network != "" {
				var keep []opsService
				for _, svc := range h.Services {
					if slices.Contains(svc.Networks, network) {
						keep = append(keep, svc)
					}
				}
				h.Services = keep
			}
			hosts = append(hosts, h)
		}
	}
	flash := ""
	if a, n := r.URL.Query().Get("done"), r.URL.Query().Get("svc"); slices.Contains(opsActions, a) && n != "" {
		flash = i18n.T("en", "admin.services.done."+a) + " " + n
	}
	if r.URL.Query().Get("checked") != "" {
		flash = i18n.T("en", "admin.services.checking")
	}
	b := s.page(r, ws, i18n.T("en", "admin.services"))
	b.Flash = flash
	s.render(w, status, "admin_services.html", struct {
		base
		Configured          bool
		Error               string
		Hosts               []opsHostView
		Networks, HostNames []string
		Network, Host       string
		Actions             []string // stop has its own form, with the tick box
	}{b, s.Ops != nil, errMsg, hosts, networks, hostNames, network, host, []string{"restart", "start", "update"}})
}

// backQuery keeps the filters across an action's redirect.
func backQuery(network, host string) string {
	q := url.Values{}
	if network != "" {
		q.Set("network", network)
	}
	if host != "" {
		q.Set("host", host)
	}
	return q.Encode()
}

// handleServiceAction restarts, starts, stops or updates one container.
// Stopping takes a tick box: it is the one action that leaves something down.
// The agent refuses to stop the website and the core itself; that rule lives
// there, where it cannot be bypassed by a form.
func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Ops == nil {
		http.Error(w, i18n.T("en", "admin.services.off"), http.StatusServiceUnavailable)
		return
	}
	hostName, name, action := r.PostFormValue("host"), r.PostFormValue("name"), r.PostFormValue("action")
	h, known := s.Ops.host(hostName)
	if !known || name == "" || !slices.Contains(opsActions, action) {
		http.Error(w, "unknown host, service or action", http.StatusBadRequest)
		return
	}
	// The filters ride along in the form, so an error re-render keeps them.
	r.URL.RawQuery = backQuery(r.PostFormValue("network"), r.PostFormValue("filter_host"))
	if action == "stop" && r.PostFormValue("confirm") != "yes" {
		s.renderServices(w, r, ws, i18n.T("en", "admin.services.confirm"), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute) // an update pulls an image
	defer cancel()
	_, err := s.Ops.post(ctx, h, "/v1/services/"+url.PathEscape(name)+"/"+action, ws.Email)
	detail := hostName + "/" + name
	if err != nil {
		_ = s.DB.AddAudit(r.Context(), ws.AccountID, "service."+action+".failed", detail+": "+err.Error())
		s.renderServices(w, r, ws, err.Error(), http.StatusBadGateway)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "service."+action, detail)
	q := r.URL.Query()
	q.Set("done", action)
	q.Set("svc", name)
	http.Redirect(w, r, "/admin/services?"+q.Encode(), http.StatusSeeOther)
}

// handleServicesCheck asks every agent to look for new images now rather
// than at its next scheduled check.
func (s *Server) handleServicesCheck(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Ops == nil {
		http.Error(w, i18n.T("en", "admin.services.off"), http.StatusServiceUnavailable)
		return
	}
	var failed []string
	for _, h := range s.Ops.Hosts {
		if _, err := s.Ops.post(r.Context(), h, "/v1/updates/check", ws.Email); err != nil {
			failed = append(failed, err.Error())
		}
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "service.check", strings.Join(failed, "; "))
	if len(failed) > 0 {
		s.renderServices(w, r, ws, strings.Join(failed, "; "), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/admin/services?checked=1", http.StatusSeeOther)
}
