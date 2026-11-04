package pages

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openpak/website/internal/netmap"
	"os"
	"path/filepath"
	"testing"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// TestWriteStatusPreview renders /status in its four states (normal, busy, nobody playing, a
// problem) for a human to look at. Runs only when PREVIEW_DIR is set.
//
//	PREVIEW_DIR=$PWD/.preview go test ./internal/pages/ -run WriteStatusPreview
func TestWriteStatusPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR")
	}
	t.Chdir("../..")
	_ = os.MkdirAll(dir, 0o755)

	nets := func(bad string) string {
		out := `{"Networks":[`
		for i, n := range []struct{ id, title string }{{"switch", "Switch"}, {"wiiu-3ds", "Wii U / 3DS"}, {"wii-ds", "Wii / DS"}, {"xbox360", "Xbox 360"}, {"xbox", "Original Xbox"}, {"ps3", "PlayStation 3"}, {"ps4", "PlayStation 4"}, {"shared", "Shared services"}, {"websites", "Websites"}} {
			st := "ok"
			if n.id == bad {
				st = "partial"
			}
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`{"ID":%q,"Title":%q,"State":%q,"Up":9,"Total":10}`, n.id, n.title, st)
		}
		out += `],`
		if bad != "" {
			return out + `"Problems":["Switch: eShop","Switch: Arms"],"Down":2}`
		}
		return out + `"Problems":null,"Down":0}`
	}

	s, fc, db := newTestServer(t)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "title", Name: "Mario Kart 8 Deluxe", Console: "Switch", Status: "live", TitleID: "0100152000022000"},
		{Kind: "title", Name: "Splatoon 3", Console: "Switch", Status: "live", TitleID: "0100C2500FC20000"},
		{Kind: "title", Name: "Stardew Valley", Console: "Switch", Status: "live", TitleID: "0100E65002BB8000"},
		{Kind: "title", Name: "Splatoon", Console: "Wii U", Status: "beta", TitleID: "0005000010176900"},
		{Kind: "title", Name: "Mario Kart 7", Console: "3DS", Status: "beta", TitleID: "0004000000030800"},
		{Kind: "title", Name: "Mario Kart Wii", Console: "Wii", Status: "beta", TitleID: "0001000052534245"},
		{Kind: "title", Name: "Super Mario Maker", Console: "Wii U", Status: "beta", TitleID: "000500001018DC00"},
		{Kind: "title", Name: "Killzone 2", Console: "PS3", Status: "live", TitleID: "BCUS98123"},
	})
	// The public map, from the recorded production topology.
	var prod, dr netmap.Raw
	var sp netmap.StatusPage
	for f, v := range map[string]any{"prod.json": &prod, "draughts.json": &dr, "gatus.json": &sp} {
		b, err := os.ReadFile("internal/netmap/testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	s.NetMap = &netmap.Service{
		Topology: func(context.Context) []netmap.HostTopo {
			return []netmap.HostTopo{{Name: "prod", IP: "145.241.199.19", Raw: prod}, {Name: "draughts", IP: "145.241.228.207", Raw: dr}}
		},
		Status: func(context.Context) *netmap.StatusPage { return &sp },
		Stages: func(context.Context) map[string]string {
			return map[string]string{"Switch": "live", "Wii U": "beta", "PS3": "in-development", "PS4": "planned"}
		},
		LogsOn: true,
	}
	_ = os.MkdirAll(filepath.Join(dir, "status"), 0o755)
	write2 := func(path, body string) { _ = os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644) }
	write2("status/map.json", get(t, s, "/status/map.json").Body.String())

	old := checkGameServers
	checkGameServers = func(context.Context, ...map[string]string) []ServerHealth { return nil }
	defer func() { checkGameServers = old }()

	render := func(name, ext string) {
		api := newExt(ext)
		defer api.Close()
		s.StatusAPI = api.URL
		extCache.at = zeroTime
		s.live.v = nil
		write(t, dir, name, get(t, s, "/status").Body.String())
		write2("status/"+name+".live.json", get(t, s, "/status/live.json").Body.String())
	}

	fc.players = []core.TitlePlayers{
		{TitleID: "0100152000022000", Namespace: "switch", Players: 14}, {TitleID: "0100C2500FC20000", Namespace: "switch", Players: 9},
		{TitleID: "0100E65002BB8000", Namespace: "switch", Players: 3}, {TitleID: "0005000010176900", Namespace: "wiiu", Players: 7},
		{TitleID: "0004000000030800", Namespace: "3ds", Players: 5}, {TitleID: "0001000052534245", Namespace: "wfc", Players: 4},
		{TitleID: "000500001018DC00", Namespace: "wiiu", Players: 2}, {TitleID: "BCUS98123", Namespace: "np", Players: 1},
	}
	fc.totalPlayers = 61
	fc.networkPlayers = map[string]int{"switch": 26, "wiiu": 9, "3ds": 5, "wfc": 4, "np": 1}
	render("status-busy", nets(""))
	render("status-problem", nets("switch"))
	fc.players, fc.totalPlayers, fc.networkPlayers = nil, 0, map[string]int{}
	render("status-quiet", nets(""))
	fc.players = fc.players[:0]
	fc.players = []core.TitlePlayers{{TitleID: "0100E65002BB8000", Namespace: "switch", Players: 1}}
	fc.totalPlayers, fc.networkPlayers = 1, map[string]int{"switch": 1}
	render("status-normal", nets(""))
}
