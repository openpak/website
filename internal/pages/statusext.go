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

type extGroup struct {
	Title, Note string
	Services    []extService
	Up          int
	// Fold is true for a long list, which the page tucks behind a summary unless something is down.
	Fold bool
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
					Services    []struct {
						Name   string
						Up     bool
						Uptime float64
					}
				}
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&page) == nil {
				v.Reached = true
				for _, g := range page.Groups {
					eg := extGroup{Title: g.Title, Note: g.Note, Fold: len(g.Services) > 14}
					for _, sv := range g.Services {
						eg.Services = append(eg.Services, extService{sv.Name, sv.Up, sv.Uptime})
						if sv.Up {
							eg.Up++
						} else {
							v.Down++
							eg.Fold = false
						}
					}
					v.Groups = append(v.Groups, eg)
				}
			}
		}
	}
	extCache.at, extCache.v = time.Now(), v
	return v
}
