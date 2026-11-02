package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/core"
)

// registerCompanionTitles adds the titles that have companion pages. Each source reads its
// backend through that backend's own API; one whose URL is not configured is left out, so a
// deployment without the backend shows no page for it.
func registerCompanionTitles(comp *companion.Service, links core.Links, accounts accountNames, log *slog.Logger) {
	// Pokémon Global Link: Global Showcase rankings and open GTS listings on every DS / 3DS Pokémon title.
	if pgl := companion.NewPGL(os.Getenv("POKEMON_GLOBAL_LINK_URL")); pgl != nil {
		comp.Register(companion.Source{Match: companion.IsPokemonHandheld, Rankings: pgl, Listings: pgl})
	} else {
		log.Info("companion: POKEMON_GLOBAL_LINK_URL not set; no Pokémon rankings or trades")
	}
	// Every title whose game server implements the rankings contract (docs/rankings-contract.md).
	people := rankingPeople(links, accounts)
	for _, rs := range rankingServers {
		if c := companion.NewContract(rs.URL, people); c != nil {
			c.Fallback = "Player"
			cs := companion.Source{Names: rs.Names, Console: rs.Console, Rankings: c}
			comp.Register(cs)
		}
	}
	// PS3 trophies (/account/trophies): np-trophy's private API, the PSN id from the core's "np" link.
	if np := companion.NewNPTrophy(os.Getenv("NP_TROPHY_URL"), os.Getenv("NP_TROPHY_KEY")); np != nil {
		comp.TrophySource = np
		comp.PSN = func(ctx context.Context, accountID string) string {
			l, err := links.ActiveLinkByAccount(ctx, "np", accountID)
			if err != nil {
				return ""
			}
			return l.SubjectID
		}
	} else {
		log.Info("companion: NP_TROPHY_URL not set; /account/trophies says it is not available")
	}
}

// accountNames is the part of the account core the rankings need to name a player.
type accountNames interface {
	GetAccount(ctx context.Context, accountID string) (core.Account, error)
}

// rankingPeople names the players of a ranking through the account core: a game server sends
// a network player id or an account id, never a name.
func rankingPeople(links core.Links, accounts accountNames) *companion.People {
	// Switch players are ranked by NEX PID, which is not the core link's subject (the BAAS user id):
	// nx-baas holds that map.
	switchNet := adapter.New(os.Getenv("ADAPTER_INTERNAL_URL"), os.Getenv("NX_INTERNAL_KEY"))
	return &companion.People{
		AccountByPID: func(ctx context.Context, network, pid string) (string, error) {
			if network == "switch" {
				a, _, err := switchNet.Player(ctx, pid, "")
				return a, err
			}
			return links.AccountBySubject(ctx, network, pid)
		},
		PIDByAccount: func(ctx context.Context, network, accountID string) (string, error) {
			if network == "switch" {
				_, p, err := switchNet.Player(ctx, "", accountID)
				return p, err
			}
			l, err := links.ActiveLinkByAccount(ctx, network, accountID)
			return l.SubjectID, err
		},
		Name: func(ctx context.Context, accountID string) (string, error) {
			a, err := accounts.GetAccount(ctx, accountID)
			if err != nil {
				return "", err
			}
			if a.DisplayName != "" {
				return a.DisplayName, nil
			}
			return a.Handle, nil
		},
		Avatar: func(_ context.Context, accountID string) string { return "/media/avatars/" + accountID + "/128" },
	}
}
