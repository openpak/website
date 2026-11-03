package main

import (
	"strings"
	"testing"
)

// Each catalogue title is listed once, every URL is internal, and an Xbox 360 line names the
// title id its x360-xstats path scopes to.
func TestRankingServers(t *testing.T) {
	seen := map[string]bool{}
	for _, rs := range rankingServers {
		if rs.Console == "" || len(rs.Names) == 0 || !strings.HasPrefix(rs.URL, "http://") {
			t.Errorf("%v: incomplete line", rs.Names)
		}
		k := rs.Console + "|" + rs.Names[0]
		if seen[k] {
			t.Errorf("%s listed twice", k)
		}
		seen[k] = true
		if rs.Console == "Xbox 360" {
			if len(rs.TitleIDs) != 1 || !strings.HasSuffix(rs.URL, "/t/"+rs.TitleIDs[0]) {
				t.Errorf("%v: URL %s does not scope to %v", rs.Names, rs.URL, rs.TitleIDs)
			}
		}
		// Spelunky 2 is a vendor-free container on openpak.network (RANKINGS_ADDR=:9090), not a host-network switch-nex process.
		if rs.Console == "Switch" && !strings.HasPrefix(rs.URL, "http://host.containers.internal:") && rs.URL != "http://openpak-spelunky2:9090" {
			t.Errorf("%v: Switch servers run on the host network", rs.Names)
		}
	}
}
