# The network map

Two views of one graph, both drawn from what is deployed right now:

| View | URL | Who | What it shows |
| --- | --- | --- | --- |
| Staff map | `/admin/network` | admin accounts only (same gate as every `/admin` page) | every node, its status, repo, version, logs link, database and dependencies |
| Public map | `/status` (section "Network map"), data at `/status/map.json` | everyone | consoles, the services each uses, the shared core; plain-language text and live status |

The public map is derived from the staff graph by `netmap.Public`, which drops every address,
port, repository, database, server name and raw service id. A test (`TestPublicMapHasNoInternals`)
fails if one leaks.

## Where the data comes from

Nothing is a hand-kept list of services.

1. **Quadlets, through `openpak-opsd`.** Each box's agent answers `GET /v1/topology`: for every
   `openpak-*.container` quadlet its unit, description, image and version, published ports,
   `Requires=`/`Wants=`, state and health, plus every reference to another service found in the
   `Environment=` lines and the quadlet's own `EnvironmentFile`s. For a reference the agent reports
   only the variable's *name* and the scheme, host and port (and a database name for a Postgres,
   Mongo or Redis URL). Never a value: variables named KEY, TOKEN, SECRET or PASSWORD are skipped
   unless they are plainly an address, URL user-info is dropped before parsing, and an env file
   that more than eight quadlets include (the shared `.env`) is not read at all, because it would
   draw a line from everything to everything.
2. **Resolution (`internal/netmap/build.go`).** A reference is matched to a node by container
   name, by unit name, by container IP, or by published port on the box it names (`127.0.0.1`,
   `host.containers.internal` and the box's own public address all mean "this box"; the other box's
   address means that box). A service on both boxes (`nn-nncs`, `pakka`) is one node.
3. **Status.** The container's state and health from opsd, with the availability checks from
   `status.openpak.org/api/status` laid over it (a running container whose public check fails is
   degraded, and says which check). A console is as healthy as its services; its stage (live, beta,
   in development, planned) comes from the `/features` rows, the same source as `/status`.
4. **Names and descriptions.** The quadlet `Description=` is the staff description. Public text is
   in `internal/netmap/public.go` (plain-language names for the shared services and the per-console
   services); a service missing from those tables still appears, with a generic line.

A service deployed tomorrow is on the map tomorrow. One whose name says nothing about its console
lands in the **Unassigned** lane and is listed under "Could not wire" so someone notices. To place
it: give the quadlet `Label=org.openpak.network=switch` (or `wiiu,3ds`), or add it to opsd's
`OPSD_NETWORK_MAP`, or (for a name with no hint) `networkOverride` in `internal/netmap/classify.go`.

## Networks, kinds and lines

The ten console networks are Switch, Wii U, 3DS, Wii, DS, PS3, PS4, PS5, Xbox 360 and Xbox
(`consoleSpec`). Node kinds: `adapter` (a console's own services, by `nx-`, `nn-`, `wfc-`, `np-`,
`ps4-`, `x360-`, `xb-` prefix), `game` (a game server), `vendor` (eos, demonware, photon, playfab,
klei, shiro, t17, ugs, battlenet), `core` (account, chat, saves, rewards, leaderboards,
achievements, title-storage, parental, news/BCAT, voice, turn, pakka, registry, connectivity, logs),
`site` (website, the Open sites, the app API), `infra` (Postgres, Redis, Mongo, MinIO, Traefik/edge,
console DNS, ZeroTier) and `host` (the two servers). Lines are `calls` (an address in the
environment), `store` (the target is a database), `needs` (Requires/Wants) and `network` (a
console uses a DNS or ZeroTier node).

Virtual nodes (drawn from knowledge, not from a quadlet): the App API, Logs, ZeroTier and the
two servers. ZeroTier has no probe, so it shows "not monitored" rather than a made-up green.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `WEBSITE_OPSD_HOSTS`, `WEBSITE_OPSD_KEY` | (existing) | the host agents; without them there is no map |
| `NETMAP_HOST_IPS` | `prod=145.241.199.19,draughts=145.241.228.207` | each agent's box by public address, so a reference to that address resolves |
| `STATUS_API_URL` | (existing) | the availability page |

The graph is cached for 20 seconds and a failed refresh keeps serving the last good one. The map
stores nothing: there is no table to back up.

## Using the staff map

Search (name or description; Enter opens the first hit), filter by network or by status ("needs
attention" shows only degraded and down nodes), zoom with the wheel or pinch, drag to pan. Large
game groups are folded into one node ("Switch games") and open when searched, filtered or
selected. Clicking a node opens the side panel: what it does, status, host, repo, version, logs
(`/admin/logs?service=<unit>`), databases, and what it uses and is used by. The **List** view is
the accessible twin of the map and the layout on phones.
