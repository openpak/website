package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/netmap"
)

// Topology asks every host agent for its quadlet-derived topology (GET
// /v1/topology). One host being down is that host's Err, not a failure of the
// map. IPs maps a host name to its public address, so a reference such as
// 145.241.199.19:20070 can be resolved to the service behind it.
func (o *OpsAPI) Topology(ctx context.Context, ips map[string]string) []netmap.HostTopo {
	out := make([]netmap.HostTopo, len(o.Hosts))
	var wg sync.WaitGroup
	for i, h := range o.Hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := netmap.HostTopo{Name: h.Name, IP: ips[h.Name]}
			if err := getJSON(ctx, h.URL+"/v1/topology", o.Key, &t.Raw); err != nil {
				t.Err = err.Error()
			}
			out[i] = t
		}()
	}
	wg.Wait()
	return out
}

type networkView struct {
	base
}

// handleNetwork is the staff network map: every node of the network, drawn
// from live data (docs/network-map.md). The page is a shell; the graph arrives
// from /admin/network/data so it can refresh without a reload.
func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "admin_network.html", networkView{s.page(r, ws, i18n.T("en", "admin.network"))})
}

// handleNetworkData is the full graph as JSON, admin-gated like the page.
func (s *Server) handleNetworkData(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r); !ok {
		return
	}
	if s.NetMap == nil {
		http.Error(w, "network map is not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(s.NetMap.Graph(r.Context()))
}
