package pages

import (
	"context"
	"io"
	"net/http"
	"os"
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
	return []gameServer{
		{
			Name: "Outbound", TitleID: "0100ED9024EB8000",
			Health: healthURL("PHOTON_HEALTH_URL", "http://openpak-photon:8080/health"),
		},
		{
			Name: "Stardew Valley", TitleID: "0100E65002BB8000",
			Health: healthURL("STARDEW_HEALTH_URL", "http://openpak-stardew:21011/health"),
		},
		{
			Name: "Splatoon 3", TitleID: "0100C2500FC20000",
			Health: healthURL("SPLATOON3_HEALTH_URL", "http://openpak-splatoon-3:21013/health"),
		},
		// NEX titles on the shared host: /healthz on the block's +2 port, reachable on
		// openpak.network. TitleID left blank where not yet recorded rather than guessed.
		{Name: "Mario Kart 8 Deluxe", Health: healthURL("MK8D_HEALTH_URL", "http://openpak-mario-kart-8-deluxe:22152/healthz")},
		{Name: "Splatoon 2", TitleID: "0100F8F0000A2000", Health: healthURL("SPLATOON2_HEALTH_URL", "http://openpak-splatoon-2:22322/healthz")},
		{Name: "Super Smash Bros. Ultimate", Health: healthURL("SSBU_HEALTH_URL", "http://openpak-super-smash-bros-ultimate:22352/healthz")},
		{Name: "Super Mario Odyssey", TitleID: "0100000000010000", Health: healthURL("SMO_HEALTH_URL", "http://openpak-super-mario-odyssey:22342/healthz")},
		{Name: "Super Mario Maker 2", Health: healthURL("SMM2_HEALTH_URL", "http://openpak-super-mario-maker-2:22332/healthz")},
		{Name: "Animal Crossing: New Horizons", TitleID: "01006F8002326000", Health: healthURL("ACNH_HEALTH_URL", "http://openpak-animal-crossing-new-horizons:22182/healthz")},
		{Name: "ARMS", TitleID: "01009B500007C000", Health: healthURL("ARMS_HEALTH_URL", "http://openpak-arms:22142/healthz")},
		{Name: "Mario Golf: Super Rush", TitleID: "0100C9C00E25C800", Health: healthURL("GOLF_HEALTH_URL", "http://openpak-mario-golf-super-rush:22132/healthz")},
		{Name: "Mario Party Superstars", TitleID: "01006FE013472000", Health: healthURL("MPS_HEALTH_URL", "http://openpak-mario-party-superstars:22112/healthz")},
		{Name: "Mario Tennis Aces", TitleID: "0100BDE00862A000", Health: healthURL("MTA_HEALTH_URL", "http://openpak-mario-tennis-aces:22172/healthz")},
		{Name: "Mario Strikers: Battle League", Health: healthURL("STRIKERS_HEALTH_URL", "http://openpak-mario-strikers:22302/healthz")},
		{Name: "Luigi's Mansion 3", TitleID: "0100DCA0064A6000", Health: healthURL("LM3_HEALTH_URL", "http://openpak-luigis-mansion-3:22192/healthz")},
		{Name: "Monster Hunter Generations Ultimate", Health: healthURL("MHGU_HEALTH_URL", "http://openpak-monster-hunter-generations-ultimate:22122/healthz")},
		{Name: "Super Mario Bros. 35", TitleID: "0100277011F1A000", Health: healthURL("SMB35_HEALTH_URL", "http://openpak-super-mario-bros-35:22162/healthz")},
		{Name: "Minecraft", Health: healthURL("MINECRAFT_HEALTH_URL", "http://openpak-minecraft:22312/healthz")},
	}
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
