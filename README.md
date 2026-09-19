# OpenPak website

openpak.org: the public site and the account portal (PRD `prds/website-prd.md`).
Server-rendered Go service — registration, verification, sign-in, recovery,
profile with friend code and console links, friends, invitations view,
moderation, site configuration, status, and the versioned mobile API under
`/api/v1`. No JavaScript is required for any journey (WEB-10).

- Module `github.com/openpak/website`, Go 1.27, AGPL-3.0-only (`LICENSE`).
- The account core is consumed **only** through its versioned contracts:
  HTTP `/v1/*` for the web journeys and gRPC (Identity, Links, Social,
  Events) for everything else. The website never reads core storage and
  never forks the core.

## Layout

```
cmd/website/            the openpak.org server (site, portal, /admin, /api/v1)
cmd/openpak-registry/   v2 network registry service, /api/v2/network/* (docs/registry.md)
cmd/openpak-opsd/       per-host agent behind /admin/services (docs/opsd.md)
cmd/openpak-policy/     operator console for v2 policy: publish, rollback, withdraw, inspect
cmd/openpak-network/    CI-side fetch tool: authenticate, fetch, verify a v2 bundle
internal/accounts/      account actions shared by the pages and the API
internal/accountpb/     vendored account-core gRPC stubs + .proto sources
internal/adapter/       client for the Switch (and Wii U/3DS) adapter internal API
internal/admin/         role-gated operator/moderation pages under /admin
internal/api/           /api/v1 mobile API (+ openapi/openapi.yaml contract)
internal/chat/          client for the chat service's internal API
internal/config/        environment configuration
internal/core/          account-core clients (HTTP /v1/*, gRPC) + interfaces
internal/fetchtool/     library behind cmd/openpak-network
internal/i18n/          single string catalogue (WEB-09)
internal/media/         avatar pipeline (validate, resize 256/512, storage)
internal/metrics/       minimal /metrics + pprof listener on a service's +2 port
internal/netprofile/    /api/v1/network profile generated from Traefik routing
internal/news/          news service client (public read API + operator gRPC)
internal/newspb/        generated news service gRPC stubs
internal/pages/         server-rendered pages + embedded templates
internal/policy/        v2 canonical policy, schemas, deterministic generation
internal/push/          FCM notifier driven by the core event stream
internal/ratelimit/     fixed-window limiter (login, token minting, API)
internal/registry/      v2 revisions, promotion and TUF signing
internal/registryhttp/  HTTP surface of the registry service (/api/v2, /internal/status)
internal/saves/         client for the saves service's internal API
internal/session/       cookie session wrapper, CSRF, bearer hashing
internal/store/         website-owned Postgres (config, tokens, audit, media, titles)
internal/wfc/           asks nn-wfc which console a Wii/DS friend code belongs to
web/src/                Tailwind input (input.css)
web/static/             built site.css (committed), icon.svg, consoles/ icons,
                        saves.js + titles.js (progressive enhancement only)
scripts/                css.sh — rebuilds web/static/site.css with Tailwind
```

### UI

One shell for every page — public and signed-in alike (`header`/`footer` in
`internal/pages/templates/_layout.html`); the nav swaps its links for a
session, and the moderation link appears only for accounts in
`WEBSITE_ADMIN_ACCOUNT_IDS` (`pages.Server.AdminAccountIDs` mirrors the
allowlist for display only; `/admin` authorization stays in `internal/admin`
and remains fail-closed).

`web/static/site.css` is the whole design system. It is deliberately plain:
near-black text on white, hairline rules instead of shadows, one accent used
only for links and focus, ink-filled primary buttons, and a `--col` reading
column so a list row keeps its actions beside the row. All UI copy lives in
`internal/i18n`, and the render CSP is `style-src 'self'`, so templates carry
no inline `style` attributes.

## Building

Everything needed is in this repository. The account core's contract stubs are
vendored in `internal/accountpb` (see its README for how to refresh them when
the core's contracts move), so no sibling checkout of `account` is required —
locally or in CI.

```
git clone git@github.com:Openpak/website.git
cd website
cp example.env .env 2>/dev/null || true        # then export the values
go vet ./... && go test -race ./...
go run ./cmd/website
```

## Runbook

### Dependencies

- PostgreSQL for the website's own tables (migrations apply on boot); the
  port plan's development Postgres is `localhost:23000`.
- The `account` core running with gRPC (`:20001`) and HTTP (`:20000`; set
  `ACCOUNT_HTTP_ADDR`/`ACCOUNT_GRPC_ADDR` until the core's defaults move)
  and the same internal key as `CORE_INTERNAL_KEY`.
- Traefik (or any TLS terminator) in front; the site must never serve on a
  console-facing hostname.
- Ports: website HTTP `:20010`, metrics/pprof `:20012` (`WEBSITE_METRICS_ADDR`).
  `/metrics` is Prometheus text; `/debug/pprof/` is mounted on the same port.
  Keep both off the public internet (port plan rule 4).

### The internal surface

`POST /internal/avatar` is service-to-service, never a browser, and deliberately
not in the OpenAPI. The Switch adapter calls it when a console uploads its
profile picture, so the picture a person set on their console is the avatar the
website and the app show — an avatar is an account fact, not an adapter one.

It is authenticated with `WEBSITE_INTERNAL_KEY`, compared in constant time.
**An unset key disables the route** rather than leaving it open: a deployment
that has not configured this should refuse the call, not accept it from anyone.
`WEBSITE_INTERNAL_URL` is not read here: it is the **adapter's** variable (where
it finds this site), and the adapter must hold the same `WEBSITE_INTERNAL_KEY`.

## Styling

The stylesheet is built by Tailwind from `web/src/input.css`; run
`./scripts/css.sh` after changing a template or the classes you just wrote will
not exist in `web/static/site.css`.

Tailwind's **standalone binary** is used on purpose: no node, no package.json,
no lockfile, so the only thing this repository gains from Tailwind is a
stylesheet. The built CSS is committed, which is why the container image still
builds with nothing but Go.

The palette stays as plain custom properties that flip with the reader's system
setting, mapped onto Tailwind's colour utilities with `@theme inline`. So
`bg-paper` and `text-ink` follow light and dark on their own, no template
carries a `dark:` variant, and those values remain the single source shared
with the console tool and the app.

To look at a page at a phone's width without deploying:

```sh
PREVIEW_DIR=/tmp/preview go test -count=1 ./internal/pages/ -run WritePreview
cp web/static/site.css web/static/icon.svg /tmp/preview/
chromium --headless --window-size=390,900 --screenshot=/tmp/shot.png file:///tmp/preview/status.html
```

That renders every page, signed-in ones included, from the test harness.

## Status page

`/status` shows game servers, console networks and who is playing. Each game
server row has a `*_HEALTH_URL` (the full list and defaults are in
`internal/pages/gameservers.go` and `example.env`); every one has a default probe.
Set one to `off` for a server this deployment does not run and its row says "not
checked" rather than reporting an absent server as down. The Switch-era NEX
titles run in `switch-nexd` on the host network, so their defaults are
`http://host.containers.internal:<base+2>/healthz`. A server that is checked and
not answering turns the banner red.
`GET /api/v1/status`
is the same counts as JSON, with no bearer, because the people most likely to
check status are the ones who cannot sign in.

## Push notifications

Set `FCM_CREDENTIALS_FILE` to a Firebase service-account JSON and the notifier
starts with the site; leave it unset and the site runs normally with
notifications disabled (the app can still register a token — nothing is
delivered). The notifier polls the account core's event stream, so it sends on
`friend_requested` and `invitation_sent` and on nothing else.

Its first run on a fresh database skips to the present rather than treating the
whole event history as unread. The read position lives in `push_cursor`, so a
restart resumes instead of replaying.

FCM reaches Android directly and iOS through APNs, which means **iOS delivery
additionally needs an APNs key uploaded to the Firebase project, and that key
requires a paid Apple Developer account.** Without one, iOS installs register
successfully and receive nothing.

## Configuration

See `example.env`. Required: `CORE_INTERNAL_KEY` (must equal the core's
`ACCOUNT_INTERNAL_KEY`), `WEBSITE_DATABASE_URL`, `WEBSITE_SESSION_SECRET`
(≥32 bytes). `WEBSITE_ADMIN_ACCOUNT_IDS` lists the account ids allowed to
open `/admin` — empty means nobody, by design (fail closed).

### Operations

- **Health**: `GET /healthz` (liveness), `GET /status` (core reachability,
  database, media storage).
- **Admin** (`/admin`, only for `WEBSITE_ADMIN_ACCOUNT_IDS`; one sub-nav across
  every page). A page whose backend is not configured says so instead of
  failing:
  - `/admin` — overview linking the pages below.
  - `/admin/monitor` — core, database, media, saves and chat health, game
    servers, push-notifier lag, and the external availability JSON (`STATUS_API_URL`; `off` hides it).
  - `/admin/services` — every container on every host, grouped by network,
    with start/stop/restart/update (stop needs a confirm tick) and a re-check (`WEBSITE_OPSD_HOSTS`,
    `WEBSITE_OPSD_KEY`; see [docs/opsd.md](docs/opsd.md)).
  - `/admin/news` — draft, edit, publish and unpublish articles
    (`NEWS_GRPC_ADDR`, `NEWS_OPERATOR_KEY`).
  - `/admin/titles` — supported-title statuses (website database; changes are
    audited).
  - `/admin/mods` — the mods review queue (`MODS_INTERNAL_URL`,
    `MODS_INTERNAL_KEY`).
  - `/admin/nodes` — federation regions: create (key shown once), approve,
    last seen (core Federation contract).
  - `/admin/registry` — read-only v2 registry status (`REGISTRY_INTERNAL_URL`,
    `REGISTRY_ADMIN_KEY`).
  - `/admin/config` — registrations open/closed + banner (WEB-07); saves are
    audited in `admin_audit`.
  - `/admin/events` — the core's durable invalidation event stream plus
    website audit entries (newest 200).
  - `/admin/users`, `/admin/reports` — moderation (ban/unban/search, reports)
    is fail-closed pending core contracts CR-5/CR-6 — see "Contract requests".
- **Avatars**: uploaded avatars are validated, center-cropped and resized
  server-side to 256 and 512 px PNG under `WEBSITE_MEDIA_DIR` (filesystem
  first; `internal/media.Storage` is the S3-compatible seam). Serving to
  consoles moves to the adapter once CR-2 lands.
- **Logs**: structured to stderr at info; set `WEBSITE_LOG_LEVEL=debug` to also
  log every request.

### Mobile API (WEB-08)

Contract: [`openapi/openapi.yaml`](openapi/openapi.yaml) (OpenAPI 3.1).
`POST /api/v1/token` exchanges account credentials for a bearer token
bound to a live core web session; every API call re-validates that session,
so bans/logouts/password resets end mobile access with the core's
revocation window. Rate limits: 10 mints/min per client, 120 calls/min per
token.

The app can do everything the account pages do: register, request a reset
mail and complete the reset (`POST /api/v1/password/reset`; public, sharing the
per-client mint limit), change the password, delete the account
(`DELETE /api/v1/me` with the password), link a Wii/DS by friend code and unlink
a console by namespace, upload an avatar (multipart) or pick a preset, accept or
decline a friend request (`/me/friends/requests/decline`), decline an
invitation, read the public title catalogue (`GET /api/v1/titles`), chat, and
manage cloud saves. Where the site and the API do the same thing they share
`internal/accounts`. `contract_test.go` fails the build when a served
`/api/v1` route is missing from the spec.

`/api/v2/network/*` is **not** served by this binary: it belongs to the
registry service (below).

### Network profile (NP-0..NP-4)

`GET /api/v1/network/profile?platform=<switch|wiiu|3ds|wii|ds>` answers what
OpenPak is and what a client should send it, generated from the routing this
server actually runs (PRD `prds/network-profile-service-prd.md`): the Traefik dynamic
config mounted read-only at `NETPROFILE_TRAEFIK_FILE` provides the names,
`internal/netprofile/platforms.yml` is the only authored input, and the
console DNS universe (`internal/netprofile/families.go`) is the ceiling no
family may leave. One conditional GET per launch: ETag in, 304 out.
`GET /api/v1/network/ca.pem` serves the CA the profile's `ca.sha256` pins.

- A routed name no platform claims is omitted and logged loudly;
  `internal/netprofile`'s drift test fails the build when the shipped map
  stops covering the live routing (refresh `testdata/traefik.yml` from the
  box in the same PR as the map change).
- The server may narrow, never widen: every emitted family is inside what
  the console's clients already compile in.
- `nn-sssl-dns` reads the same map (NP-4), so the real-console resolver and
  the emulators cannot drift apart.

## Registry service

The v2 network registry runs as its own process, `cmd/openpak-registry`, from
the same image (command `/app/openpak-registry`): it serves `/api/v2/network/*`
and the operator's `GET /internal/status`, and its signing key and database
never load into the website. Its variables (`REGISTRY_*`) belong to that
container, except `REGISTRY_ADMIN_KEY`, which the website also holds for
`/admin/registry`. Ports, configuration and the steps that must happen before
releasing a website without the in-process registry: [docs/registry.md](docs/registry.md).

## Host agent (openpak-opsd)

`cmd/openpak-opsd` runs once per host, outside any container, and is the only
thing that talks to podman; the website never holds the podman socket.
`/admin/services` asks each agent listed in `WEBSITE_OPSD_HOSTS` with the shared
`WEBSITE_OPSD_KEY`. Install and configuration: [docs/opsd.md](docs/opsd.md).

## Sessions model (WEB-02)

The website cookie (`opak_session`, HttpOnly/Secure/SameSite=Lax) wraps the
core web session id. A website-side index table maps that id to the account
id resolved at login via the Identity contract. When the core session dies
(revoked device, ban, password reset), the next request clears the cookie
and the index row; API bearers bound to it are revoked on sight.

## Friend codes (CR-3 interim)

The friend code shown on the website is the subject of the account's active
`switch` link, read through the core Links contract and rendered as
`SW-xxxx-xxxx-xxxx` when it is 12 hex digits. Codes resolve back to
accounts through `Links.GetLinkBySubject`. This is a projection of an
adapter-owned value — see CR-3.

## Contract requests (raise as `account` issues)

Carried from the PRD, plus what this implementation discovered:

- ~~**CR-1** `Invitations.ListForAccount(account_id) → [pending invitations]`~~ — **delivered
  2026-09-08** as `Invitations.ListInvitations`; also `RejectInvitation`, `Sessions.GetPresence`
  and `Sessions.GetPlayerCounts`. Original request kept for the reasoning:
  read-only, no delivery side effects. Until then `/account/invitations`
  renders a pending notice; the website must not call `PollInvitations`
  (adapter-scoped, delivery side effects).
- **CR-2** Account media: store and serve avatar bytes via the core (or a
  documented shared media store) so adapters can serve them to consoles
  without calling the website. The website-side pipeline exists; core sync
  is the missing half.
- **CR-3** Friend-code projection: define how third-party surfaces read the
  adapter-owned `SW-` code (core-published field vs adapter read contract).
  Interim: Links subject projection (above).
- **CR-4** `GET /v1/whoami` on the core HTTP surface (account id, email,
  display name, status for the session cookie). The core's `POST /v1/login`
  returns no identity, so the website currently re-verifies the password
  over gRPC at login to learn the account id. A whoami route would remove
  the extra verification entirely.
- **CR-5** Admin contract: `SearchAccounts(query)`,
  `SetAccountBanned(account_id, banned, reason)` emitting the existing
  invalidation events. Blocks the W3 gate ("ban stops a live session") —
  the website pages are built and fail closed until this lands.
- **CR-6** Reports read model (queue + resolve) for the moderation queue.
- ~~**CR-7** `SetDisplayName(account_id, name)`~~ — **delivered**: the core
  accepts `display_name` on `POST /v1/profile`, and both the profile form and
  `POST /api/v1/me/profile` send it.

## Testing

`go vet ./...` and `go test -race ./...` are the definition of done and run
in CI. The W1/W2 acceptance journeys (register → verify → login → friend
code → link revoke; request → accept → visible on both sides; block →
unblock → remove) are covered end to end in `internal/pages/journey_test.go`
and `internal/api/api_test.go` against contract-faithful fakes.
