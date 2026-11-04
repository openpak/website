// Package netorder is the one order networks are shown in: family, then generation, oldest first
// (Nintendo, PlayStation, Xbox), then Shared services and Websites. networks.json is a copy of the
// status repository's shared/networks.json (scripts/sync-status-shared.sh); the status page on
// status.openpak.org, /status and the network map all read it, so they cannot disagree.
package netorder

import (
	_ "embed"
	"encoding/json"
)

//go:embed networks.json
var raw []byte

// Net is one entry. Consoles are the network map's console ids; Site is the name this site's
// networks list uses ("" for Shared services and Websites).
type Net struct {
	ID, Title, Family string
	Consoles          []string
	Site              string
}

// All is the order.
var All = func() []Net {
	var n []Net
	if err := json.Unmarshal(raw, &n); err != nil {
		panic("netorder: " + err.Error())
	}
	return n
}()

// ConsoleRank is where a map console id sits in the order; unknown ids sort last.
func ConsoleRank(id string) int {
	for i, n := range All {
		for j, c := range n.Consoles {
			if c == id {
				return i*10 + j
			}
		}
	}
	return len(All) * 10
}
