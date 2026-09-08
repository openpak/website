package pages

import (
	"context"
	"net"
	"os"
	"time"
)

// The game servers OpenPak runs, and how each one can be checked.
//
// Not every service can be probed honestly. Stardew's server accepts TCP, so a
// connection is a real answer. Photon — which carries Outbound — speaks UDP
// only, and a UDP socket cannot be "connected" to from here in any way that
// distinguishes a running server from a silent one. Rather than paint it green
// on the strength of nothing, its row says what is actually known: how many
// people are on it, and that its health is not directly checked. Giving Photon
// a health port is the fix, and it belongs in that server, not in a guess here.
type gameServer struct {
	Name    string
	TitleID string
	// Addr is a TCP address to connect to. Empty means this server has no TCP
	// surface and cannot be checked from here.
	Addr string
	Note string
}

func gameServers() []gameServer {
	return []gameServer{
		{
			Name: "Outbound", TitleID: "0100ED9024EB8000",
			Note: "Runs on Photon, which is UDP only: player numbers are live, its health is not directly checked.",
		},
		{
			Name: "Stardew Valley", TitleID: "0100E65002BB8000",
			Addr: getenv("STARDEW_ADDR", "openpak-stardew:21010"),
		},
	}
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// reachable reports whether something is accepting connections there. A short
// timeout on purpose: this runs while a person waits for the page, and a slow
// answer is worse than an unknown one.
func reachable(ctx context.Context, addr string) bool {
	if addr == "" {
		return false
	}
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
