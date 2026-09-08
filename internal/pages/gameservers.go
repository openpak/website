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
