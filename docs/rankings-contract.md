# Rankings contract

One read-only internal HTTP JSON API that every OpenPak game server implements for the
rankings it already stores, and one public API and hub on openpak.org that read it.

```
game server  --/internal/rankings/*-->  website (names + avatars via the account core)
                                           |-- /titles/<slug>/rankings   the Rankings tab
                                           |-- /rankings                 the hub, by console
                                           `-- /api/v1/rankings/...      the public API
```

The website never reads a game server's database. A server with nothing stored implements
nothing: a title without boards has no Rankings tab and no hub entry.

## Internal endpoints (game server)

Served by the game server on `RANKINGS_ADDR` (for example `:9090`), on the container network
only: no Traefik route, no published port. Unset means off.

### `GET /internal/rankings/boards`

```json
{"network": "wiiu",
 "boards": [
  {"id": "5", "name": "Coconut Mall", "kind": "time", "order": "asc", "unit": "ms"},
  {"id": "t7s2", "name": "Spring Tournament", "kind": "score", "order": "desc", "season": "2"}
 ]}
```

| Field | Meaning |
| --- | --- |
| `network` | The account-core link namespace the `pid` values belong to: `wiiu` (Wii U and 3DS), `switch`, `wfc` (Wii and DS), `np` (PS3), `x360`, `xbox`. Not needed for rows that carry `account_id`. |
| `id` | Stable short string (a decimal course or category number). |
| `name` | Human name from the game's known categories; where unknown a neutral `Board 3`, never a raw hex id. |
| `kind` | `time` (milliseconds), `score` (points) or `rating` (a skill rating). |
| `order` | `asc` (rank 1 has the lowest score, time trials) or `desc`. |
| `unit` | Optional: `ms`, `points`. |
| `season` | Optional season label when the board belongs to one. |

### `GET /internal/rankings/boards/{id}?limit=&offset=&around=<id>`

```json
{"board": "5", "total": 412, "offset": 0, "limit": 25,
 "rows": [
  {"rank": 1, "pid": "1800000123", "score": 83456, "display": "1:23.456", "updated_at": "2026-10-30T18:02:11Z"}
 ],
 "around": [ ...rows 5 either side of the player... ]}
```

* `limit` defaults to 25, at most 100. `offset` defaults to 0.
* `rank` is the board position, 1-based, counted the way the game's own ranking query counts.
* A row identifies its player by `pid` (decimal string, the id the server stores) or by
  `account_id` (an OpenPak account id), whichever the table holds. **Servers never send
  display names or avatars.**
* `display` is the formatted score: `m:ss.mmm` for time boards, a number with thousands
  separators otherwise, or the game's own form.
* `around` is present only when the `around` query parameter named a player (a `pid` or
  `account_id`, the same kind the rows carry) who is on the board. It holds the rows up to 5
  either side of them.
* An unknown board is `404 {"error":"no such board"}`; a store that is down is
  `503 {"error":"store unavailable"}`.

## Shared Go helper

`rankingsapi/rankingsapi.go` in this repo is standard-library only. Game servers **copy the
file** into their repo (the precedent of the other shared code here), implement `Source`
over their stored rankings and start it:

```go
rankingsapi.Serve(os.Getenv("RANKINGS_ADDR"), "wiiu", mySource)
```

`Window` builds a `Page` from a rank-ordered row slice; servers with big tables page in SQL
instead. `Client` is what the website reads a server with.

## Website

* `cmd/website/rankings.go` maps each catalogue title to its server's internal URL. Add a
  line when a server gains the contract.
* `internal/companion/contract.go` is the one generic client: it reads boards and rows, and
  resolves every row's player through `internal/companion/people.go` (the account core: a
  `pid` becomes an account through the link of the network, then the account's display name
  and `/media/avatars/<account>/128`). A player nobody has linked is shown as "Player".
  Boards are cached for 60 seconds, pages for 15.
* A title shows the Rankings tab only while its server answers and has at least one board.

## Public API

Read-only, no sign-in, cached 30 seconds, `Access-Control-Allow-Origin: *`.

| Endpoint | Answer |
| --- | --- |
| `GET /api/v1/rankings` | `{"games":[{slug,name,console,boards}]}` |
| `GET /api/v1/rankings/{slug}` | `{slug,name,console,boards:[{id,name,kind,order,unit,season}]}` |
| `GET /api/v1/rankings/{slug}/{board}?limit=&offset=&around=<account_id>` | `{slug,board,total,offset,limit,rows,around}` |

A row is `{rank, account_id, name, avatar, score, display, updated_at}`; `avatar` is a
site-relative URL (`https://openpak.org` + path). `around` is an OpenPak **account id** here
(the website turns it into the server's `pid`). OpenKart, OpenSplat and the app read this API.
The same data is in the older `/api/v1/companion/titles/{slug}/rankings`.

## Adding a game

1. Copy `rankingsapi.go`, implement `Source`, call `Serve` at startup, document `RANKINGS_ADDR`.
2. Set `RANKINGS_ADDR` in the game server's quadlet. Do not publish the port.
3. Add the title (catalogue name and console, exactly) and the server URL to
   `cmd/website/rankings.go`.

## Games and boards today

| Game | Server (repo, tag) | Boards |
| --- | --- | --- |
| Mario Kart Wii | wfc-race v0.4.1 (wfc-common v0.2.2) | 32 worldwide courses, time |
| Mario Kart 8 Deluxe | switch-nex v0.9.1 | course time trials and tournament seasons |
| Mario Kart 8, Mario Kart 7 | nex v1.4.12 | one board per course, time |
| Pikmin 3, Splatoon (Wii U), Puyo Puyo Tetris, Sonic & All-Stars Racing Transformed, Terraria (Wii U), Dr. Luigi, Dr. Mario: Miracle Cure | nex v1.4.12 | one board per stored category (neutral "Board N" where the game's category is unknown) |
| Puyo Puyo!! 20th Anniversary, Pokken Tournament, Super Smash Bros. for Nintendo 3DS and for Wii U, Yo-kai Watch 2, Monster Hunter 4 Ultimate, Monster Hunter 4, Monster Hunter Stories, Monster Hunter XX, Ice Station Z, Resident Evil: The Mercenaries 3D, Super Mario Maker | nex v1.5.0 | category boards where the game stores scores (Tetris Axis: no catalogue row yet) |
| Mario Golf: Super Rush, Super Mario Bros. 35, Mario Tennis Aces, Splatoon 2, Kirby's Dream Buffet, Super Smash Bros. Ultimate, Clubhouse Games: 51, Risk of Rain, Risk of Rain Returns, Risk of Rain 2, Mario Strikers: Battle League, Pac-Man 99, TMNT: Shredder's Revenge | switch-nex v0.10.0 | one board per stored category; host network, `http://host.containers.internal:<port>` (network `switch`, pid = unsigned decimal NEX pid) |
| Xbox 360: Halo 3 (4D5307E6), Halo: Reach (4D53085B), Halo 4 (4D530919), Call of Duty 4 (415607E6) and the other 26 Xbox 360 catalogue rows | leaderboards core, fed by x360-xstats v0.7.0 (x360-xarb v0.2.0 reports through it) | one rating board per game and view. The core serves `http://openpak-leaderboards:20210/t/x360:<title id>` (the unmapped title key, `x360:` and the 8-digit hex title id) with only that title's boards; the catalogue row carries the hex id in `title_ids` and the website matches on it. Other 360 titles: add the id to the catalogue and a line in `rankings.go` |
| PlayStation 3 scores (and any network's, once its adapter moves over) | leaderboards v0.1.0, fed by np-score v0.3.0 | the neutral leaderboards core serves the contract itself for every title it holds: `http://openpak-leaderboards:20210/t/<title key>` per title, `/` for all of them, `/n/<network>/t/<title>` for one network's view of per-network boards. A PS3 row needs its NPWR communication id first in the catalogue `title_ids` (set for 29 of 31 rows in v0.87.0) and a line in `rankings.go` with `/n/np/t/<NPWR>`. Terraria PS3 has no catalogue row. Regional comm ids of one game (Demon's Souls, Armored Core V) are listed but only the first has a rankings line |

A game with no scored board has no Rankings tab and no hub entry; boards appear as soon as a
score is stored.

## Deployment notes

* Servers on `openpak.network` listen on `RANKINGS_ADDR=:9090` and publish nothing.
* Mario Kart 8 Deluxe runs on the host network, so it listens on `:22855`, the website reaches it at
  `http://host.containers.internal:22855`, and an `INPUT ! -i lo ... REJECT` rule (like the connectivity
  API's, in `/etc/iptables/rules.v4`) keeps the port off the public side.
* Check after a deploy from outside the box: `/internal/rankings/boards` must not answer on any of these hosts or ports.
