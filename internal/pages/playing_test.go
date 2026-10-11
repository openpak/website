package pages

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

func TestWebsiteUnknownTitlesCountOnlyAsOnline(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("catalogue_game_%t", known), func(t *testing.T) {
			s, fc, db := newTestServer(t)
			fc.players = []core.TitlePlayers{
				{TitleID: "010025901a268000", Namespace: "switch", Players: 1},
			}
			fc.totalPlayers = 1
			inGame := 0
			if known {
				if err := db.SeedRequests(context.Background(), []store.Request{
					{Kind: "title", Name: "ARMS", Console: "Switch", TitleID: "01009B500007C000"},
				}); err != nil {
					t.Fatal(err)
				}
				fc.players = append(fc.players, core.TitlePlayers{
					TitleID: "01009b500007c000", Namespace: "switch", Players: 2,
				})
				fc.totalPlayers += 2
				inGame = 2
			}
			fc.networkPlayers = map[string]int{"switch": fc.totalPlayers}
			for _, path := range []string{"/", "/status"} {
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				body := rec.Body.String()
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d", path, rec.Code)
				}
				if strings.Contains(strings.ToUpper(body), "010025901A268000") {
					t.Errorf("%s lists an unknown title", path)
				}
				if path == "/" {
					for _, want := range []string{
						fmt.Sprintf("<b>%d</b> Online now", fc.totalPlayers),
						fmt.Sprintf("<b>%d</b> In a game", inGame),
					} {
						if !strings.Contains(body, want) {
							t.Errorf("%s missing %q", path, want)
						}
					}
					if known && !strings.Contains(body, "ARMS") {
						t.Error("the homepage should show a catalogue title's name")
					}
				} else if !strings.Contains(body, fmt.Sprintf("%d online", fc.totalPlayers)) {
					t.Error("unknown titles must still count toward the status online total")
				}
			}
		})
	}
}
