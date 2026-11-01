# Title companion pages and play activity

Per-title pages under `https://openpak.org/titles/<slug>` and the JSON API beside them, so
the OpenPak app can show the same data. One shell, a few shared components, and a data source
per title.

## Pages

| URL | What | Sign-in |
| --- | --- | --- |
| `/titles/<slug>` | Title header, tab strip, the features the title has | no |
| `/titles/<slug>/rankings?board=<id>` | A ranking board: podium, list, "around you", board picker | no (marks your own rows when signed in) |
| `/titles/<slug>/stats` | The signed-in player's stats for the title (a stat card) | yes |
| `/titles/<slug>/listings` | A title's public listings as a card (Pokémon GTS) | no |
| `/account/trophies` | PS3 trophies: totals by grade, points, one row per game | yes |
| `/account/activity` | Play activity: time, sessions and last played per title and network | yes |

A title appears in the titles page's details dialog with a "Rankings and stats" button only
when it has a companion feature. A title with no feature has no page (404).

The slug is the lower-case name with hyphens (`mario-kart-wii`). Two catalogue titles with the
same name (a game on two consoles) get the console appended (`minecraft-switch`).

## JSON API

Served under `/api/v1`, documented in `openapi/openapi.yaml`. They live under `/companion/`
because `/api/v1/titles/` belongs to the mod catalogue.

| Endpoint | Auth | Answer |
| --- | --- | --- |
| `GET /api/v1/companion/titles` | none | `{"titles":[{slug,name,console,status,title_id,features}]}` |
| `GET /api/v1/companion/titles/{slug}` | none | one title; 404 if it has no feature |
| `GET /api/v1/companion/titles/{slug}/rankings?board=` | none (optional bearer marks your rows) | `{boards,board,label,entries,around,updated}` |
| `GET /api/v1/companion/titles/{slug}/stats` | bearer | `{heading,linked,stats,rows}` |
| `GET /api/v1/companion/titles/{slug}/listings` | none | `{heading,linked,stats,rows}` |
| `GET /api/v1/me/trophies` | bearer | `{heading,linked,stats,rows}`; `heading` is the PSN online id |
| `GET /api/v1/me/activity` | bearer | `{titles,total_seconds,sessions,networks}` |

Errors are the usual `{"error","code"}`. `503` means the deployment does not have that backend,
`502` that it did not answer.

## Adding a title

`internal/companion` holds the service. A title registers a `Source` in
`cmd/website/companion.go`:

```go
c.Register(companion.Source{
    Names:    []string{"Mario Kart Wii"}, // catalogue name, case-insensitive
    Console:  "Wii",                       // "" matches any console
    Rankings: myRankings,                  // optional: Boards() and Ranking(board, account)
    Stats:    myStats,                     // optional: Stats(account) returning a Card
})
```

The page, the tab strip, the JSON and the titles-dialog link follow from what is registered.
A source wraps a client for the backend's own API; the website never reads another service's
database.

## Play activity

`/account/activity` and `GET /api/v1/me/activity` read the account core's `GetPlayHistory`
(gRPC, the `play_sessions` log fed from presence), once per network
(`companion.ActivityNetworks`). It covers every account, not only supervised consoles; the core
keeps the 20 most recent titles per network and counts a session from when the game was online
with the network. Per-day usage of supervised consoles is the parental page
(`/account/parental`, `GET /api/v1/me/parental-controls/{moonID}/activity`).

## Previews

`TestWritePreview` renders the companion pages with the other pages (see deploy.md,
"Styling"); the files are `companion-rankings.html`, `companion-stats.html` and
`activity.html`.

## Rewards

`/account/rewards` is one page with four tabs (`?tab=missions|shop|collection|settings`, the companion
pages' tab strip): a header with the Pak Points balance, today's play points against the cap and one
suggested next step; Missions (ready to claim, in progress, completed, recent points); Shop (a tile shelf
filterable by `?kind=`, each tile opening a confirm dialog from `#r-<item id>`, which needs
`/static/rewards.js` like the titles page needs `titles.js`); Collection (the Founder seal, then owned
items by kind, equip and unequip); Settings (showcase visibility, pause earning). Actions are POSTs that
redirect back to a tab with a typed message (`m=`, success `notice ok`, error `notice bad`). The person's
own account-core session is the bearer, so the service decides who is asking. Item previews are
proxied at `/account/rewards/preview/{id}`; art is shown as square tiles (badges, icons) or wide cards
(themes), whatever its size. `/admin/rewards` (admins only, in the admin System Settings shell and nav, left
out when `REWARDS_URL` is unset) shows the totals, the abuse flags queue with a resolve form and
redemptions by item; it uses the admin key and the admin's account id. Previews:
`TestWritePreviewRewards` (set `REWARDS_ART_DIR` to the item art) and the admin `TestWritePreview`, both
against `internal/rewards/rewardstest`, a fake of the service.

Environment: `REWARDS_URL` (private address, `http://openpak-rewards:20180`),
`REWARDS_INTERNAL_KEY`, `REWARDS_ADMIN_KEY`. Without `REWARDS_URL` the page says rewards are not
available.

## Data sources and configuration

| Feature | Backend API read | Website environment |
| --- | --- | --- |
| Mario Kart Wii rankings (32 course boards, worldwide) | wfc-race `GET /api/mkw/leaderboard` (v0.2.0), header `X-WFC-Internal-Key` | `WFC_RACE_URL` (`http://openpak-wfc-race:20208`), `WFC_RACE_INTERNAL_KEY` (falls back to `WFC_INTERNAL_KEY`) |
| Pokémon (DS and 3DS titles): Global Showcase rankings and GTS listings | pokemon-global-link public JSON `/api/v1/showcase`, `/gts/listings`, `/battle/formats` | `POKEMON_GLOBAL_LINK_URL` (`http://openpak-pokemon-global-link:20160`) |
| PS3 trophies | np-trophy `GET /v1/users/{account}/trophies`, bearer key; the PSN id from the core's `np` link | `NP_TROPHY_URL` (`http://openpak-np-trophy:8530`), `NP_TROPHY_KEY` |
| Play activity | the account core's `GetPlayHistory` (gRPC) | the core connection the site already has |
| Rewards | rewards service `/v1/me...` and admin routes | `REWARDS_URL`, `REWARDS_INTERNAL_KEY`, `REWARDS_ADMIN_KEY` |

A source whose URL is unset is left out. Pokémon species are PKHeX ids and the service sends no
names, so rows read "Pokémon #25". The PS3 trophy service stores unlocks only, so a game is its
trophy set id and there are no trophy names or icons. Mario Kart Wii needs no pid-to-name
mapping: the player's Mii name comes from the stored player info. Ghost download is not on the
site: wfc-sake only serves the nearest slower ghost, not a chosen player's.

## Not built, and why

- Xbox 360 gamercard and achievements: x360-progress and x360-profile answer only console
  requests, and x360-progress is a stub that returns empty lists and stores nothing, so there is
  no achievement or gamerscore data to read.
- Mario Kart 8 Deluxe, Mario Tennis Aces, ARMS rankings: the Switch NEX server (`switch-nexd`)
  acknowledges score uploads without storing them and has no read API, so no ranking exists.
- Monster Hunter Generations Ultimate events and guild cards, Mario vs. Donkey Kong: Tipping Stars
  levels: no read API, no evidence of stored data, and the code has no guild-card or level
  semantics.
