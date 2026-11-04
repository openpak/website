package pages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// extNet is one network (or Shared services, or Websites) as status.openpak.org rolls it up.
type extNet struct {
	ID, Title, State string
	Up, Total        int
}

// extStatus is the outside checker's answer, trimmed to what /status draws: a state per
// network and the names of whatever is failing. The per-service detail lives on
// status.openpak.org, which this page links to.
type extStatus struct {
	Reached  bool
	Networks map[string]extNet
	Problems []string
	Down     int
}

// extCache keeps a reload storm off status.openpak.org; the checker itself only runs every minute or two.
var extCache struct {
	mu sync.Mutex
	at time.Time
	v  extStatus
}

// externalStatus reads status.openpak.org/api/status: every check Gatus runs from another host.
// Its failure is shown as its own line, never as an outage of the network.
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
				Networks []extNet
				Problems []string
				Down     int
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&page) == nil && len(page.Networks) > 0 {
				v = extStatus{Reached: true, Networks: map[string]extNet{}, Problems: page.Problems, Down: page.Down}
				for _, n := range page.Networks {
					v.Networks[n.ID] = n
				}
			}
		}
	}
	extCache.at, extCache.v = time.Now(), v
	return v
}
