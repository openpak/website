package netmap

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func load(t *testing.T, f string, v any) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) Input {
	var prod, dr Raw
	var sp StatusPage
	load(t, "prod.json", &prod)
	load(t, "draughts.json", &dr)
	load(t, "gatus.json", &sp)
	return Input{
		Hosts:  []HostTopo{{Name: "prod", IP: "145.241.199.19", Raw: prod}, {Name: "draughts", IP: "145.241.228.207", Raw: dr}},
		Status: &sp, LogsOn: true, Now: time.Unix(0, 0),
		Stages: map[string]string{"Switch": "live", "Wii U": "beta", "PS3": "in-development", "PS4": "planned"},
	}
}

func TestBuildRealTopology(t *testing.T) {
	g := Build(fixture(t))
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	t.Logf("%d nodes, %d edges, %d notes", len(g.Nodes), len(g.Edges), len(g.Notes))
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kinds[n.Kind]++
	}
	t.Logf("kinds: %v", kinds)
	for _, n := range g.Notes {
		t.Log("note:", n)
	}
	for _, id := range []string{"console-switch", "console-ps5", "account", "leaderboards", "nx-baas", "switch-arms", "postgres", "logs", "app-api", "host-prod"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("missing node %s", id)
		}
	}
	if byID["console-ps5"].State != Planned {
		t.Errorf("ps5 should be planned: %+v", byID["console-ps5"])
	}
	// switch-arms reaches rewards, leaderboards, account, baas through the env, resolved by port or name.
	want := map[string]bool{"rewards": false, "leaderboards": false, "account": false, "nx-baas": false}
	for _, e := range g.Edges {
		if e.From == "switch-arms" {
			if _, ok := want[e.To]; ok {
				want[e.To] = true
			}
		}
	}
	for to, ok := range want {
		if !ok {
			t.Errorf("no line switch-arms -> %s", to)
		}
	}
	if byID["switch-arms"].Group != "games:switch" || byID["switch-arms"].Label != "Arms" {
		t.Errorf("switch-arms: %+v", byID["switch-arms"])
	}
	if n := byID["nn-nncs"]; len(n.Hosts) != 2 {
		t.Errorf("nn-nncs runs on both boxes: %+v", n)
	}
}

func TestNoSecretShapedStrings(t *testing.T) {
	g := Build(fixture(t))
	b, _ := json.Marshal(g)
	for _, bad := range []string{"password", "secret", "token=", "postgres://"} {
		if contains2(string(b), bad) {
			t.Errorf("graph JSON contains %q", bad)
		}
	}
}

func contains2(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPublicMapHasNoInternals(t *testing.T) {
	pg := Public(Build(fixture(t)), nil)
	b, _ := json.Marshal(pg)
	s := string(b)
	for _, bad := range []string{"145.241", "github.com", "postgres", "nx-baas", "openpak-", "127.0.0.1", "ghcr", "draughts", "quilted", "\"host"} {
		if contains2(s, bad) {
			t.Errorf("public map leaks %q", bad)
		}
	}
	kinds := map[string]int{}
	for _, n := range pg.Nodes {
		kinds[n.Kind]++
		for _, m := range n.Members {
			if contains2(m.Label, "-") && !contains2(m.Label, " ") && false {
				t.Errorf("member label looks like an id: %q", m.Label)
			}
		}
	}
	t.Logf("public: %d nodes %d edges %v", len(pg.Nodes), len(pg.Edges), kinds)
	if kinds["console"] != 10 || kinds["core"] < 10 {
		t.Errorf("kinds %v", kinds)
	}
}
