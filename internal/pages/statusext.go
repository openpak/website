package pages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// extService and extGroup are the outside checker's answer, trimmed to what the status page draws.
type extService struct {
	Name   string
	Up     bool
	Uptime float64
}

// extSvcIn is one service as the checker's API writes it.
type extSvcIn struct {
	Name   string
	Up     bool
	Uptime float64
}

type extGroup struct {
	Title, Note string
	Services    []extService
	// Ports are the game-session-port checks: one line each, tucked behind a summary unless one is down.
	Ports []extService
	Up    int
	Total int
	Fold  bool
}

type extStatus struct {
	Reached bool
	Groups  []extGroup
	Down    int
}

// extCache keeps a reload storm off status.openpak.org; the checker itself only runs every minute or two.
var extCache struct {
	mu sync.Mutex
	at time.Time
	v  extStatus
}

// externalStatus reads status.openpak.org/api/status: every check Gatus runs from another host, grouped as
// that page groups them. Its failure is shown as its own line, never as an outage of the network.
func (s *Server) externalStatus(ctx context.Context) extStatus {
	if s.StatusAPI == "" {
		return extStatus{}
	}
	extCache.mu.Lock()
	defer extCache.mu.Unlock()
	if time.Since(extCache.at) < 20*time.Second {
		return extCache.v
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	v := extStatus{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.StatusAPI, nil)
	if err == nil {
		var resp *http.Response
		if resp, err = http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			var page struct {
				Groups []struct {
					Title, Note string
					Services    []extSvcIn
					Ports       []extSvcIn
				}
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&page) == nil {
				v.Reached = true
				for _, g := range page.Groups {
					eg := extGroup{Title: g.Title, Note: g.Note, Fold: true}
					add := func(in []extSvcIn, out *[]extService, port bool) {
						for _, sv := range in {
							*out = append(*out, extService{sv.Name, sv.Up, sv.Uptime})
							eg.Total++
							if sv.Up {
								eg.Up++
							} else {
								v.Down++
								if port {
									eg.Fold = false
								}
							}
						}
					}
					add(g.Services, &eg.Services, false)
					add(g.Ports, &eg.Ports, true)
					v.Groups = append(v.Groups, eg)
				}
			}
		}
	}
	extCache.at, extCache.v = time.Now(), v
	return v
}
