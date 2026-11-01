package companion

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/core"
)

// ActivityNetworks are the core networks (presence namespaces) the play history is asked of. The
// core answers per namespace, so a network it has never heard of simply returns nothing.
var ActivityNetworks = []string{"switch", "wiiu", "3ds", "wfc", "xbox", "x360", "np"}

// ActivityTitle is one title an account has played on one network.
type ActivityTitle struct {
	Network  string    `json:"network"`
	TitleID  string    `json:"title_id"`
	Title    string    `json:"title"` // "" when the catalogue does not know the id
	Icon     bool      `json:"icon"`  // /titles/icon/<title_id> can draw it
	Sessions int       `json:"sessions"`
	Seconds  int64     `json:"seconds"`
	First    time.Time `json:"first_played"`
	Last     time.Time `json:"last_played"`
}

// Activity is an account's play history across every network, newest first.
type Activity struct {
	Titles       []ActivityTitle `json:"titles"`
	TotalSeconds int64           `json:"total_seconds"`
	Sessions     int             `json:"sessions"`
	// Networks lists the networks with any history, in the order Titles first meets them.
	Networks []string `json:"networks"`
}

// Activity reads an account's play history: what the core recorded from presence, for every
// account (not only supervised consoles, whose per-day usage is on the parental page).
func (s *Service) Activity(ctx context.Context, accountID string) (Activity, error) {
	if s.History == nil {
		return Activity{}, ErrUnavailable
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		entries []core.PlayEntry
		failed  int
	)
	for _, ns := range ActivityNetworks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			got, err := s.History.PlayHistory(cctx, accountID, ns, 20)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				return
			}
			entries = append(entries, got...)
		}()
	}
	wg.Wait()
	// Every network failing is an outage; one failing costs only its rows.
	if failed == len(ActivityNetworks) {
		return Activity{}, ErrUnavailable
	}
	var catalogue map[string]string
	if s.Catalogue != nil {
		catalogue = s.Catalogue(ctx)
	} else {
		catalogue = defaultCatalogue(s, ctx)
	}
	out := Activity{Titles: []ActivityTitle{}, Networks: []string{}}
	for _, e := range entries {
		t := ActivityTitle{Network: e.Namespace, TitleID: e.TitleID, Sessions: e.Sessions,
			Seconds: int64(e.Total / time.Second), First: e.First, Last: e.Last}
		if s.Namer != nil {
			t.Title, t.Icon = s.Namer(ctx, catalogue, e.Namespace, e.TitleID)
		} else {
			t.Title = catalogue[strings.ToUpper(e.TitleID)]
		}
		out.Titles = append(out.Titles, t)
		out.TotalSeconds += t.Seconds
		out.Sessions += t.Sessions
	}
	sort.SliceStable(out.Titles, func(i, j int) bool { return out.Titles[i].Last.After(out.Titles[j].Last) })
	seen := map[string]bool{}
	for _, t := range out.Titles {
		if !seen[t.Network] {
			seen[t.Network] = true
			out.Networks = append(out.Networks, t.Network)
		}
	}
	return out, nil
}

func defaultCatalogue(s *Service, ctx context.Context) map[string]string {
	reqs, err := s.DB.ListRequests(ctx, "title", "")
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, r := range reqs {
		for _, id := range append([]string{r.TitleID}, r.TitleIDs...) {
			if id != "" && r.Name != "" {
				out[strings.ToUpper(id)] = r.Name
			}
		}
	}
	return out
}
