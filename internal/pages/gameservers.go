package pages

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// The game servers OpenPak runs, and where each one answers a health check.
//
// Every server now has an HTTP health endpoint, including Photon: its game
// listeners are UDP and cannot be probed, so it grew one TCP port for exactly
// this. Before that, its row had to say "not checked", which was honest and
// useless. A URL here means the row is a real answer rather than an inference.
type gameServer struct {
	Name    string
	TitleID string
	// Health is an HTTP URL that answers 200 when the server is up. Empty
	// means this server cannot be checked, and its row says so.
	Health string
}

// healthURL reads a health endpoint from the environment. The literal "off"
// disables the check, for a deployment that does not run that server: its row
// then says so rather than reporting a server that was never there as down.
func healthURL(key, def string) string {
	v := getenv(key, def)
	if v == "off" {
		return ""
	}
	return v
}

func gameServers() []gameServer {
	// Alphabetical. TitleID left blank where not yet recorded rather than guessed.
	//
	// The Switch-era NEX titles all run inside the one switch-nexd container on the host
	// network, which answers GET /healthz on each title's base+2 (switch-nex v0.2.16). From
	// the website's container the host is host.containers.internal. MHGU stays on the nex-go
	// lineage in its own container and keeps its container name.
	return []gameServer{
		{Name: "Animal Crossing: New Horizons", TitleID: "01006F8002326000", Health: nexd("ACNH_HEALTH_URL", 22182)},
		{Name: "ARMS", TitleID: "01009B500007C000", Health: nexd("ARMS_HEALTH_URL", 22142)},
		{Name: "Clubhouse Games: 51 Worldwide Classics", Health: nexd("CLUBHOUSE_HEALTH_URL", 22872)},
		{Name: "Kirby's Dream Buffet", Health: nexd("KIRBY_HEALTH_URL", 22382)},
		{Name: "Luigi's Mansion 3", TitleID: "0100DCA0064A6000", Health: nexd("LM3_HEALTH_URL", 22192)},
		{Name: "Mario Golf: Super Rush", TitleID: "0100C9C00E25C800", Health: nexd("GOLF_HEALTH_URL", 22132)},
		{Name: "Mario Kart 8 Deluxe", Health: nexd("MK8D_HEALTH_URL", 22152)},
		{Name: "Mario Party Superstars", TitleID: "01006FE013472000", Health: nexd("MPS_HEALTH_URL", 22112)},
		{Name: "Mario Strikers: Battle League", Health: nexd("STRIKERS_HEALTH_URL", 22302)},
		{Name: "Mario Tennis Aces", TitleID: "0100BDE00862A000", Health: nexd("MTA_HEALTH_URL", 22172)},
		{Name: "Minecraft", Health: nexd("MINECRAFT_HEALTH_URL", 22312)},
		{Name: "Monster Hunter Generations Ultimate", Health: healthURL("MHGU_HEALTH_URL", "http://openpak-monster-hunter-generations-ultimate:22122/healthz")},
		{Name: "Outbound", TitleID: "0100ED9024EB8000", Health: healthURL("PHOTON_HEALTH_URL", "http://openpak-photon:8080/health")},
		{Name: "Splatoon 2", TitleID: "0100F8F0000A2000", Health: nexd("SPLATOON2_HEALTH_URL", 22322)},
		{Name: "Splatoon 3", TitleID: "0100C2500FC20000", Health: healthURL("SPLATOON3_HEALTH_URL", "http://openpak-splatoon-3:21013/health")},
		{Name: "Stardew Valley", TitleID: "0100E65002BB8000", Health: healthURL("STARDEW_HEALTH_URL", "http://openpak-stardew:21011/health")},
		{Name: "Super Mario 3D World + Bowser's Fury", Health: nexd("SM3DW_HEALTH_URL", 22372)},
		{Name: "Super Mario Bros. 35", TitleID: "0100277011F1A000", Health: nexd("SMB35_HEALTH_URL", 22162)},
		{Name: "Super Mario Maker 2", Health: nexd("SMM2_HEALTH_URL", 22332)},
		{Name: "Super Mario Odyssey", TitleID: "0100000000010000", Health: nexd("SMO_HEALTH_URL", 22342)},
		{Name: "Super Smash Bros. Ultimate", Health: nexd("SSBU_HEALTH_URL", 22352)},
	}
}

// nexd is a title served by switch-nexd: its health port on the host.
func nexd(key string, port int) string {
	return healthURL(key, "http://host.containers.internal:"+strconv.Itoa(port)+"/healthz")
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// healthy asks a server's health endpoint. A short timeout on purpose: this
// runs while a person waits for the page, and a slow answer is worse than an
// unknown one.
func healthy(ctx context.Context, url string) bool {
	if url == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// ServerHealth is one game server's answer, for the public status page and the
// admin monitor alike.
type ServerHealth struct {
	Name, TitleID string
	// Checkable is false for a server this deployment has no health URL for;
	// Up is then meaningless and the row says "not checked".
	Checkable, Up bool
}

// CheckGameServers asks every game server at once. In parallel on purpose:
// one after another, a handful of dead servers at two seconds each made the
// status page slowest exactly when someone was looking at it because
// something was down. Order follows gameServers().
func CheckGameServers(ctx context.Context) []ServerHealth {
	servers := gameServers()
	out := make([]ServerHealth, len(servers))
	var wg sync.WaitGroup
	for i, g := range servers {
		out[i] = ServerHealth{Name: g.Name, TitleID: g.TitleID, Checkable: g.Health != ""}
		if out[i].Checkable {
			wg.Go(func() { out[i].Up = healthy(ctx, g.Health) })
		}
	}
	wg.Wait()
	return out
}

// TitleName is titleName for the admin pages: the name OpenPak knows a title
// id by, or the id itself.
func TitleName(id string) string { return titleName(id) }
