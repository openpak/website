package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// healthChecker is anything with a health probe: the core's HTTP client, media
// storage, the saves and chat clients.
type healthChecker interface {
	Healthy(ctx context.Context) error
}

// check is one row on the monitor. State is the state-row tint: ok, bad, or
// idle for a backend this deployment does not configure — which is a fact
// about the deployment, not an outage, so it is never red.
type check struct {
	Name, State, Detail string
}

func probe(ctx context.Context, name string, h healthChecker) check {
	if h == nil {
		return check{name, "idle", i18n.T("en", "admin.monitor.off")}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := h.Healthy(ctx); err != nil {
		return check{name, "bad", err.Error()}
	}
	return check{name, "ok", i18n.T("en", "status.ok")}
}

type titleCount struct {
	Name    string
	Players int
}

type monitorView struct {
	base
	Services []check
	Servers  []pages.ServerHealth
	CountsOK bool
	Total    int
	Titles   []titleCount
	Push     check
	// External is the availability page's own view of the network, from
	// outside it.
	External []check
}

// handleMonitor is the operator's overview: everything the public status page
// checks, plus what only an operator cares about. Every probe runs at once
// with its own short deadline, so the page costs the slowest probe, not the
// sum of them.
func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v := monitorView{base: s.page(r, ws, i18n.T("en", "admin.monitor")), Services: make([]check, 5)}
	var wg sync.WaitGroup
	wg.Go(func() { v.Services[0] = probe(ctx, i18n.T("en", "status.core"), s.CoreHTTP) })
	wg.Go(func() { v.Services[1] = probe(ctx, i18n.T("en", "status.db"), pinger{s.DB}) })
	wg.Go(func() { v.Services[2] = probe(ctx, i18n.T("en", "status.storage"), s.Media) })
	// A nil *saves.Client in a non-nil interface would answer ErrUnavailable
	// and show red; "not configured" is the truth, so pass a real nil.
	wg.Go(func() {
		var h healthChecker
		if s.Saves != nil {
			h = s.Saves
		}
		v.Services[3] = probe(ctx, i18n.T("en", "admin.monitor.saves"), h)
	})
	wg.Go(func() {
		var h healthChecker
		if s.Chat != nil {
			h = s.Chat
		}
		v.Services[4] = probe(ctx, i18n.T("en", "admin.monitor.chat"), h)
	})
	wg.Go(func() { v.Servers = pages.CheckGameServers(ctx, pages.TitleCatalogue(ctx, s.DB)) })
	wg.Go(func() { v.CountsOK, v.Total, v.Titles = s.playerCounts(ctx) })
	wg.Go(func() { v.Push = s.pushLag(ctx) })
	wg.Go(func() { v.External = s.externalStatus(ctx) })
	wg.Wait()
	s.render(w, http.StatusOK, "admin_monitor.html", v)
}

// pinger gives the store's Ping the health probe's name.
type pinger struct{ store.Store }

func (p pinger) Healthy(ctx context.Context) error { return p.Ping(ctx) }

func (s *Server) playerCounts(ctx context.Context) (bool, int, []titleCount) {
	if s.Players == nil {
		return false, 0, nil
	}
	titles, _, total, err := s.Players.PlayerCounts(ctx)
	if err != nil {
		s.log.Error("player counts", "err", err)
		return false, 0, nil
	}
	out := make([]titleCount, 0, len(titles))
	catalogue := pages.TitleCatalogue(ctx, s.DB)
	for _, t := range titles {
		if name := pages.CatalogueTitleName(catalogue, t.TitleID); name != "" {
			out = append(out, titleCount{name, t.Players})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Players > out[j].Players })
	return true, total, out
}

// pushLag is how far the notifier's cursor trails the core's newest event.
// A few events is normal (it polls on an interval); a growing number means
// notifications are not going out.
func (s *Server) pushLag(ctx context.Context) check {
	name := i18n.T("en", "admin.monitor.push")
	if !s.PushOn {
		return check{name, "idle", i18n.T("en", "admin.monitor.off")}
	}
	cursor, err := s.DB.PushCursor(ctx)
	if err != nil {
		return check{name, "bad", err.Error()}
	}
	head, err := core.EventHead(ctx, s.Core, cursor)
	if err != nil {
		return check{name, "bad", err.Error()}
	}
	lag := head - cursor
	st := "ok"
	if lag > 100 {
		st = "warn"
	}
	return check{name, st, fmt.Sprintf(i18n.T("en", "admin.monitor.push.lag"), lag, cursor, head)}
}

// statusPage is the part of status.openpak.org's /api/status this page reads.
// The statuspage encodes its view structs without tags, hence the capitals.
type statusPage struct {
	Groups []struct {
		Title    string
		Services []struct {
			Name    string
			Up      bool
			Uptime  float64
			Checked string
		}
	}
}

// externalStatus reads the availability page, which checks the network from
// outside it. Its failure is reported as its own fact, amber rather than red:
// the checker being unreachable says nothing about whether OpenPak is up.
func (s *Server) externalStatus(ctx context.Context) []check {
	name := i18n.T("en", "admin.monitor.external.page")
	if s.StatusAPI == "" {
		return []check{{name, "idle", i18n.T("en", "admin.monitor.off")}}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var page statusPage
	if err := getJSON(ctx, s.StatusAPI, "", &page); err != nil {
		return []check{{name, "warn", err.Error()}}
	}
	var out []check
	for _, g := range page.Groups {
		for _, sv := range g.Services {
			c := check{g.Title + " · " + sv.Name, "bad", ""}
			if sv.Up {
				c.State = "ok"
			}
			c.Detail = strconv.FormatFloat(sv.Uptime, 'f', 1, 64) + "% · " + sv.Checked
			out = append(out, c)
		}
	}
	return out
}

// getJSON is the one small HTTP read the admin backends share: GET, optional
// bearer, a 2xx or an error carrying the status, a bounded body. Extra fields
// in the answer are ignored, so a backend can grow without breaking this page.
func getJSON(ctx context.Context, url, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
