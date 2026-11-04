package pages

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/store"
)

// withChecks serves a canned status.openpak.org answer and healthy game servers.
func withChecks(t *testing.T, s *Server, body string) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(api.Close)
	s.StatusAPI = api.URL
	extCache.at = time.Time{}
	old := checkGameServers
	checkGameServers = func(context.Context, ...map[string]string) []ServerHealth { return nil }
	t.Cleanup(func() { checkGameServers = old; extCache.at = time.Time{}; s.live.v = nil })
	s.live.v = nil
}

const extOK = `{"Networks":[{"ID":"switch","Title":"Switch","State":"ok","Up":5,"Total":5},{"ID":"shared","Title":"Shared services","State":"ok","Up":3,"Total":3}],"Problems":null,"Down":0}`
const extBad = `{"Networks":[{"ID":"switch","Title":"Switch","State":"partial","Up":4,"Total":5},{"ID":"shared","Title":"Shared services","State":"ok","Up":3,"Total":3}],"Problems":["Switch: eShop","Switch: Arms"],"Issues":[{"Network":"Switch","Name":"eShop"},{"Network":"Switch","Name":"Arms","Game":true}],"Down":2}`

func TestStatusOverviewNormalAndProblem(t *testing.T) {
	s, fc, db := newTestServer(t)
	_ = db.SeedRequests(context.Background(), []store.Request{{Kind: "title", Name: "ARMS", Console: "Switch", TitleID: "01009B500007C000"}})
	fc.players = []core.TitlePlayers{{TitleID: "01009b500007c000", Namespace: "switch", Players: 3}}
	fc.totalPlayers, fc.networkPlayers = 3, map[string]int{"switch": 3}

	withChecks(t, s, extOK)
	body := get(t, s, "/status").Body.String()
	for _, want := range []string{`class="sv-banner ok"`, "Everything is working", "3 online", `<b data-n="3">3</b> playing`, "Full service status"} {
		if !strings.Contains(body, want) {
			t.Errorf("normal state missing %q", want)
		}
	}
	if strings.Contains(body, "eShop") || strings.Contains(body, "Checked from outside") {
		t.Error("service rows and the old outside-checks section are gone from this page")
	}

	withChecks(t, s, extBad)
	body = get(t, s, "/status").Body.String()
	if !strings.Contains(body, `class="sv-banner warn"`) || !strings.Contains(body, "Switch: eShop and ARMS") || !strings.Contains(body, "Partly down") {
		t.Errorf("a failing check must name itself in the banner and mark its network: %s", body)
	}

	// The poll carries counts only: no account names, ids or emails.
	rec := get(t, s, "/status/live.json")
	var v struct {
		Titles []struct{ Name string }
		Online int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Online != 3 || len(v.Titles) != 1 || v.Titles[0].Name != "ARMS" {
		t.Errorf("live.json: %v %s", err, rec.Body.String())
	}
}

func TestStatusOverviewNobodyPlaying(t *testing.T) {
	s, _, _ := newTestServer(t)
	withChecks(t, s, extOK)
	body := get(t, s, "/status").Body.String()
	if !strings.Contains(body, "It is quiet right now") || strings.Contains(body, `class="sv-card sv-title"`) {
		t.Error("an empty playing list is one friendly line, not an empty list")
	}
}

// The shared stylesheet is a copy of the status repository's; it must not drift.
func TestStatusSharedStyleMatchesTheStatusRepo(t *testing.T) {
	src, err := os.ReadFile("../../../status/shared/statusview.css")
	if err != nil {
		t.Skip("status repository not checked out beside this one")
	}
	have, err := os.ReadFile("../../web/static/statusview.css")
	if err != nil || string(have) != string(src) {
		t.Error("web/static/statusview.css differs from status/shared/statusview.css; run scripts/sync-status-shared.sh")
	}
}

var zeroTime time.Time

func newExt(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
}

func TestGroupedProblemsUseCatalogueNames(t *testing.T) {
	in := []extIssue{{"Switch", "eShop", false}, {"Switch", "Arms", true}, {"Wii U / 3DS", "eShop", false}, {"Switch", "ARMS", true}}
	in = issueNames(in, map[string]catEntry{"X": {Name: "ARMS", Console: "Switch"}})
	if got := groupIssues(in, "and", "and {n} more"); got != "Switch: eShop and ARMS; Wii U / 3DS: eShop" {
		t.Errorf("grouped: %q", got)
	}
}

func TestOverviewCardsFollowTheNetworkOrder(t *testing.T) {
	s, _, _ := newTestServer(t)
	nets := ""
	for _, id := range []string{"xbox360", "switch", "ps4", "wii-ds", "websites", "ps3", "shared", "wiiu-3ds", "xbox"} {
		if nets != "" {
			nets += ","
		}
		nets += `{"ID":"` + id + `","Title":"T","State":"ok","Up":1,"Total":1}`
	}
	withChecks(t, s, `{"Networks":[`+nets+`],"Problems":null,"Down":0}`)
	body := get(t, s, "/status").Body.String()
	last := -1
	for _, id := range []string{"wii-ds", "wiiu-3ds", "switch", "ps3", "ps4", "xbox", "xbox360", "shared", "websites"} {
		i := strings.Index(body, `data-net="`+id+`"`)
		if i < 0 || i < last {
			t.Fatalf("card %s out of order (at %d, after %d)", id, i, last)
		}
		last = i
	}
	for _, fam := range []string{">Nintendo<", ">Sony<", ">Microsoft<", ">OpenPak<"} {
		if strings.Count(body, `class="sv-fam"`+fam) != 1 {
			t.Errorf("one %s heading expected", fam)
		}
	}
	// Every card that counts players says so, a quiet network included.
	i := strings.Index(body, `data-net="ps4"`)
	card, _, _ := strings.Cut(body[i:], "</li>")
	if !strings.Contains(card, "0 online") {
		t.Errorf("the PlayStation 4 card should say 0 online: %s", card)
	}
}
