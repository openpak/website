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

`/account/rewards` shows points, missions (claim), the shop (redeem), the inventory (equip) and
the showcase/pause settings, through the rewards service (`internal/rewards`). The person's own
account-core session is the bearer, so the service decides who is asking. Item previews are
proxied at `/account/rewards/preview/{id}`. `/admin/rewards` (admins only) shows the totals and
the abuse flags queue with a resolve form; it uses the admin key and the admin's account id.

Environment: `REWARDS_URL` (private address, `http://openpak-rewards:20180`),
`REWARDS_INTERNAL_KEY`, `REWARDS_ADMIN_KEY`. Without `REWARDS_URL` the page says rewards are not
available.
