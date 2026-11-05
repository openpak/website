package erasure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ExpectedServices is every service that keeps per-account data and so must
// have an erase step (card openpak/prds#120). Steps wired elsewhere in
// main.go (saves, chat, mods, ...) are listed too, so the admin page shows
// the whole chain. A name here with no step configured is logged on every
// erasure and shown as "not configured"; it does not hold the account back
// (a dev-only service must not block production erasure).
var ExpectedServices = []string{
	"saves", "chat", "leaderboards", "achievements", "title-storage", "matchmaking", "pakka", "mods", "website",
	"nex", "switch-nex", "nn-friends", "nn-juxtaposition", "nn-boss", "nn-account",
	"np-community", "ps5-friends", "ps5-messaging", "xbl",
	"xb-social", "xb-xmessaging", "xb-stfd", "xb-account",
	"x360-profile", "x360-xmessaging", "x360-store", "x360-xstats", "x360-xarb", "x360-xpnfront", "x360-kdc", "x360-xuacs",
	"eos", "klei", "playfab", "fall-guys", "stumble-guys", "ugs",
	"demonware", "t17", "spelunky2", "battlenet", "npln", "among-us", "photon",
	"nx-baas", "splatnet", "plants-vs-zombies",
	"mario-maker", "pokemon-home", "pokemon-global-link", "nx-shop", "nx-penne",
	"wfc",
}

// LastSteps are named steps that must run after the others: services that
// resolve another service's ids (nn-account, x360-xuacs) are erased last.
var LastSteps = map[string]bool{"nn-account": true, "x360-xuacs": true}

// ParseServices reads ERASURE_SERVICES: comma- or newline-separated
// name=url pairs, url being the service's base address on the container
// network (the same name may repeat: one title container each). Whitespace
// and blank entries are ignored.
func ParseServices(spec string) (out []ServiceURL, err error) {
	for _, e := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		name, u, ok := strings.Cut(e, "=")
		name, u = strings.TrimSpace(name), strings.TrimSpace(u)
		if !ok || name == "" || u == "" {
			return nil, fmt.Errorf("erasure: bad ERASURE_SERVICES entry %q (want name=url)", e)
		}
		if _, perr := url.Parse(u); perr != nil {
			return nil, fmt.Errorf("erasure: bad url for %s: %w", name, perr)
		}
		out = append(out, ServiceURL{Name: name, URL: strings.TrimRight(u, "/")})
	}
	return out, nil
}

// ServiceURL is one container to ask.
type ServiceURL struct{ Name, URL string }

// ServiceSteps turns the parsed list into steps, one per name: a name that
// repeats (a game with several title containers) is one step that calls
// every address and fails if any does. Steps in LastSteps are returned in the
// second slice.
func ServiceSteps(list []ServiceURL, key string, hc *http.Client) (first, last []Step) {
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	var order []string
	urls := map[string][]string{}
	for _, s := range list {
		if _, ok := urls[s.Name]; !ok {
			order = append(order, s.Name)
		}
		urls[s.Name] = append(urls[s.Name], s.URL)
	}
	for _, n := range order {
		st := HTTPStep(n, urls[n], key, hc)
		if LastSteps[n] {
			last = append(last, st)
		} else {
			first = append(first, st)
		}
	}
	return first, last
}

// HTTPStep calls DELETE {base}/internal/accounts/{id}/data with the shared
// ACCOUNT_ERASE_KEY on every base address. Every address is tried even when
// one fails, so a retry only repeats idempotent work.
func HTTPStep(name string, bases []string, key string, hc *http.Client) Step {
	return Step{Name: name, Erase: func(ctx context.Context, id string) error {
		if key == "" {
			return fmt.Errorf("%s: ACCOUNT_ERASE_KEY not set", name)
		}
		var firstErr error
		for _, b := range bases {
			if err := callErase(ctx, hc, b, key, id); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", name, err)
			}
		}
		return firstErr
	}}
}

func callErase(ctx context.Context, hc *http.Client, base, key, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/internal/accounts/"+url.PathEscape(id)+"/data", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}
	return nil
}
