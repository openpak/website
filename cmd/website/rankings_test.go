package main

import (
	"strings"
	"testing"
)

// Each catalogue title is listed once, every URL is internal, and an Xbox 360 line names the
// title id its leaderboards core path scopes to (/t/x360:<title id>).
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
			if len(rs.TitleIDs) != 1 || !strings.HasSuffix(rs.URL, "/t/x360:"+rs.TitleIDs[0]) {
				t.Errorf("%v: URL %s does not scope to %v", rs.Names, rs.URL, rs.TitleIDs)
			}
		}
		// Titles served by the core leaderboards (/t/<title>) are container-network URLs. Spelunky 2 is a vendor-free container on openpak.network (RANKINGS_ADDR=:9090), not a host-network switch-nex process.
		if rs.Console == "Switch" && !strings.HasPrefix(rs.URL, "http://host.containers.internal:") && rs.URL != "http://openpak-spelunky2:9090" && !strings.HasPrefix(rs.URL, "http://openpak-leaderboards:20210/t/") {
			t.Errorf("%v: Switch servers run on the host network", rs.Names)
		}
	}
}

// A title with regional boards lists every regional URL, the first being the rankingServers line,
// and every NPWR id the catalogue holds for the title has a region (so no region's scores are dropped).
func TestRankingRegions(t *testing.T) {
	for key, regions := range rankingRegions {
		var line *struct {
			Names    []string
			Console  string
			URL      string
			TitleIDs []string
		}
		for i := range rankingServers {
			if rankingServers[i].Console+"|"+rankingServers[i].Names[0] == key {
				line = &rankingServers[i]
			}
		}
		if line == nil {
			t.Fatalf("%s: no rankingServers line", key)
		}
		if len(regions) < 2 || regions[0].URL != line.URL {
			t.Errorf("%s: first region must be the line's URL", key)
		}
		seen := map[string]bool{}
		for _, r := range regions {
			if r.Label == "" || seen[r.URL] || !strings.HasPrefix(r.URL, "http://openpak-leaderboards:20210/n/np/t/NPWR") {
				t.Errorf("%s: bad region %+v", key, r)
			}
			seen[r.URL] = true
		}
		for _, id := range regionalNPWR[key] {
			if !seen["http://openpak-leaderboards:20210/n/np/t/"+id] {
				t.Errorf("%s: catalogue id %s has no region", key, id)
			}
		}
	}
}

// regionalNPWR are the comm ids the catalogue migration 0025 stores for the multi-region titles.
var regionalNPWR = map[string][]string{
	"PS3|Demon's Souls":  {"NPWR00881_00", "NPWR01249_00", "NPWR00245_00"},
	"PS3|Armored Core V": {"NPWR03050_00", "NPWR03067_00", "NPWR02037_00"},
}
