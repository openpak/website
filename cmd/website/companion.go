package main

import (
	"cmp"
	"context"
	"log/slog"
	"os"

	"github.com/openpak/website/internal/companion"
	"github.com/openpak/website/internal/core"
)

// registerCompanionTitles adds the titles that have companion pages. Each source reads its
// backend through that backend's own API; one whose URL is not configured is left out, so a
// deployment without the backend shows no page for it.
func registerCompanionTitles(c *companion.Service, links core.Links, log *slog.Logger) {
	// Pokémon Global Link: Global Showcase rankings and open GTS listings on every DS / 3DS Pokémon title.
	if pgl := companion.NewPGL(os.Getenv("POKEMON_GLOBAL_LINK_URL")); pgl != nil {
		c.Register(companion.Source{Match: companion.IsPokemonHandheld, Rankings: pgl, Listings: pgl})
	} else {
		log.Info("companion: POKEMON_GLOBAL_LINK_URL not set; no Pokémon rankings or trades")
	}
	// Mario Kart Wii time trials: wfc-race's JSON leaderboard.
	if mkw := companion.NewMKWRace(os.Getenv("WFC_RACE_URL"), cmp.Or(os.Getenv("WFC_RACE_INTERNAL_KEY"), os.Getenv("WFC_INTERNAL_KEY"))); mkw != nil {
		c.Register(companion.Source{Names: []string{"Mario Kart Wii"}, Console: "Wii", Rankings: mkw})
	} else {
		log.Info("companion: WFC_RACE_URL not set; no Mario Kart Wii rankings")
	}
	// PS3 trophies (/account/trophies): np-trophy's private API, the PSN id from the core's "np" link.
	if np := companion.NewNPTrophy(os.Getenv("NP_TROPHY_URL"), os.Getenv("NP_TROPHY_KEY")); np != nil {
		c.TrophySource = np
		c.PSN = func(ctx context.Context, accountID string) string {
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
