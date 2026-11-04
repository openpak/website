package pages

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/netorder"
)

// The downloads page reads like the status pages: the four families (Nintendo, Sony, Microsoft,
// OpenPak) from the shared network order, each console under its family oldest first with what can
// be downloaded for it. A console with nothing to download is left out, and a client whose repository
// has no public release yet is not offered (it appears once a release exists).

// consoleNames maps the network map's console ids to the console names the downloads and the
// platform list use.
var consoleNames = map[string]string{
	"wii": "Wii", "ds": "DS", "wiiu": "Wii U", "3ds": "3DS", "switch": "Switch",
	"ps3": "PS3", "psp": "PSP", "vita": "Vita", "ps4": "PS4", "ps5": "PS5",
	"xbox": "Xbox", "xbox360": "Xbox 360", "xboxone": "Xbox One",
}

// consoleLabels is how a console is named in a heading, where the short name is ambiguous.
var consoleLabels = map[string]string{"Xbox": "Original Xbox", "Vita": "PS Vita"}

// dlCard is a client with the console as a heading names it.
type dlCard struct {
	download
	Label string
}

type dlGroup struct {
	Console, Label, Status string
	Cards                  []dlCard
}

type dlFamily struct {
	Name, ID string
	Groups   []dlGroup
}

// releaseExists reports whether a client's "latest release" link leads somewhere. ReleaseCheck nil
// (tests, a deployment without outbound access) offers every client as before.
func (s *Server) releaseExists(ctx context.Context, url string) bool {
	if url == "" || s.ReleaseCheck == nil {
		return true
	}
	return s.ReleaseCheck(ctx, url)
}

// downloadFamilies groups the clients by family and console in the shared order.
func (s *Server) downloadFamilies(ctx context.Context, stages map[string]string) []dlFamily {
	status := map[string]string{}
	for _, p := range platformsAt(stages) {
		status[p.Console] = p.Status
	}
	all := append(append([]download{}, emulators...), downloads...)
	var out []dlFamily
	add := func(fam string, g dlGroup) {
		if len(out) == 0 || out[len(out)-1].Name != fam {
			out = append(out, dlFamily{Name: fam, ID: strings.ToLower(fam)})
		}
		f := &out[len(out)-1]
		f.Groups = append(f.Groups, g)
	}
	for _, n := range netorder.All {
		for _, id := range n.Consoles {
			name := consoleNames[id]
			g := dlGroup{Console: name, Label: name, Status: status[name]}
			if l, ok := consoleLabels[name]; ok {
				g.Label = l
			}
			var real int
			for _, d := range all {
				if d.Console != name {
					continue
				}
				if d.URL != "" {
					if !s.releaseExists(ctx, d.URL) {
						continue
					}
					real++
				}
				g.Cards = append(g.Cards, dlCard{d, g.Label})
			}
			// Nothing to download: no group, and a lone "not public yet" card does not make one.
			if real > 0 {
				add(n.Family, g)
			}
		}
	}
	// OpenPak's own clients: the phone app, and any desktop client that gets a release.
	var oc []dlCard
	for _, d := range apps {
		if s.releaseExists(ctx, d.URL) {
			oc = append(oc, dlCard{d, d.Console})
		}
	}
	if len(oc) > 0 {
		add("OpenPak", dlGroup{Console: "", Label: "", Cards: oc})
	}
	return out
}

// GitHubReleaseCheck answers ReleaseCheck from the GitHub API, remembering each answer for half an
// hour so the page does not spend the API's anonymous allowance. A failed lookup keeps the last
// answer, and offers the client when there is none: an outage must not empty the page.
func GitHubReleaseCheck() func(ctx context.Context, url string) bool {
	type entry struct {
		ok bool
		at time.Time
	}
	var mu sync.Mutex
	seen := map[string]entry{}
	return func(ctx context.Context, url string) bool {
		mu.Lock()
		e, have := seen[url]
		mu.Unlock()
		if have && time.Since(e.at) < 30*time.Minute {
			return e.ok
		}
		api := strings.Replace(url, "https://github.com/", "https://api.github.com/repos/", 1)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
		if err != nil {
			return true
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := http.DefaultClient.Do(req)
		ok := true
		if err == nil {
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				ok = true
			case http.StatusNotFound:
				ok = false
			default:
				ok = !have || e.ok
			}
		} else if have {
			ok = e.ok
		}
		mu.Lock()
		seen[url] = entry{ok, time.Now()}
		mu.Unlock()
		return ok
	}
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	s.render(w, http.StatusOK, "downloads.html", struct {
		baseData
		Families []dlFamily
	}{s.data(r, v, i18n.T(i18n.FromRequest(r), "downloads.title")), s.downloadFamilies(r.Context(), s.consoleStages(r.Context()))})
}
