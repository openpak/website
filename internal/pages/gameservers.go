package pages

import (
	"context"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
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

// gameServerHealth is where each served title answers a health check, keyed by
// title id. Deployment detail, and the only part of a game server this file
// still owns: the title's name and whether OpenPak serves it belong to the
// catalogue, which is where they are edited (2026-09-20 -- names and ids typed
// here as well had already drifted, and nine rows carried no id at all, so
// their players were never counted).
//
// The Switch-era NEX titles all run inside the one switch-nexd container on the
// host network, which answers GET /healthz on each title's base+2 (switch-nex
// v0.2.16). From the website's container the host is host.containers.internal.
// MHGU stays on the nex-go lineage in its own container and keeps its name.
func gameServerHealth() map[string]string {
	return map[string]string{
		"01006F8002326000": nexd("ACNH_HEALTH_URL", 22182),
		"01009B500007C000": nexd("ARMS_HEALTH_URL", 22142),
		"010047700D540000": nexd("CLUBHOUSE_HEALTH_URL", 22872),
		"0100A8E016236000": nexd("KIRBY_HEALTH_URL", 22382),
		"0100DCA0064A6000": nexd("LM3_HEALTH_URL", 22192),
		"0100C9C00E25C800": nexd("GOLF_HEALTH_URL", 22132),
		"0100152000022000": nexd("MK8D_HEALTH_URL", 22152),
		"01006FE013472000": nexd("MPS_HEALTH_URL", 22112),
		"010019401051C000": nexd("STRIKERS_HEALTH_URL", 22302),
		"0100BDE00862A000": nexd("MTA_HEALTH_URL", 22172),
		"0100D71004694000": nexd("MINECRAFT_HEALTH_URL", 22312),
		"0100770008DD8000": healthURL("MHGU_HEALTH_URL", "http://openpak-monster-hunter-generations-ultimate:22122/healthz"),
		"0100ED9024EB8000": healthURL("PHOTON_HEALTH_URL", "http://openpak-photon:8080/health"),
		"0100F8F0000A2000": nexd("SPLATOON2_HEALTH_URL", 22322),
		"0100C2500FC20000": healthURL("SPLATOON3_HEALTH_URL", "http://openpak-splatoon-3:21013/health"),
		"0100E65002BB8000": healthURL("STARDEW_HEALTH_URL", "http://openpak-stardew:21011/health"),
		"010028600EBDA000": nexd("SM3DW_HEALTH_URL", 22372),
		"0100277011F1A000": nexd("SMB35_HEALTH_URL", 22162),
		"01009B90006DC000": nexd("SMM2_HEALTH_URL", 22332),
		"0100000000010000": nexd("SMO_HEALTH_URL", 22342),
		"01006A800016E000": nexd("SSBU_HEALTH_URL", 22352),
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
// catalogue, when given, names each server: the catalogue is where a title's
// name is edited, and a name typed here as well is one that drifts. A title
// the catalogue does not carry falls back to its id, which is honest and says
// plainly that the catalogue is missing it.
func CheckGameServers(ctx context.Context, catalogue ...map[string]string) []ServerHealth {
	named := map[string]string{}
	if len(catalogue) > 0 && catalogue[0] != nil {
		named = catalogue[0]
	}
	servers := make([]gameServer, 0, len(gameServerHealth()))
	for titleID, health := range gameServerHealth() {
		name := named[strings.ToUpper(titleID)]
		if name == "" {
			name = titleName(titleID)
		}
		servers = append(servers, gameServer{Name: name, TitleID: titleID, Health: health})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

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
