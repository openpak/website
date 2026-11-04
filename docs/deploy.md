# Deploy: website

- Module `github.com/openpak/website`, Go 1.27, AGPL-3.0-only (`LICENSE`).

## Building

Everything needed is in this repository. The account core's contract stubs come from
the public `github.com/openpak/account-api` module (bump it in `go.mod` when the
core's contracts move), so no sibling checkout of `account` is required —
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

## Logs

Where this service's logs are, how long they are kept and the `/admin/logs` viewer:
see `docs/logs.md` (day files under `~/openpak-logs/openpak-website/`, 90 days).

## Styling

The stylesheet is built by Tailwind from `web/src/input.css`; run
`./scripts/css.sh` after changing a template or the classes you just wrote will
not exist in `web/static/site.css`.

To look at a page at a phone's width without deploying:

```sh
mkdir -p .preview && ln -sfn "$PWD/web/static" .preview/static
PREVIEW_DIR=$PWD/.preview go test -count=1 ./internal/pages/ ./internal/admin/ -run WritePreview
chromium --headless --window-size=390,900 --screenshot=$PWD/.preview/shot.png file://$PWD/.preview/status.html
```

That renders the public, signed-in and admin pages from the test harness
(`--blink-settings=preferredColorScheme=1` forces the light palette).

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
    `WEBSITE_OPSD_KEY`; see [opsd.md](opsd.md)).
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
  - `/admin/users` — search accounts (id, email, name, link subject) and
    ban/unban with a reason; `/admin/reports` — the reports queue (open,
    resolved, all) and resolve. Both go through the core's Moderation service
    with the admin's account id as the actor, and are also written to
    `admin_audit`.
    With no search, `/admin/users` lists every banned account with reason,
    actor and time (`account_bans`). A ban locks the account out of every
    network: see [ban-lookup.md](ban-lookup.md) for the lookup other
    services use (`Identity.GetAccount`, or `GET /internal/bans/{id}`).
  - `/admin/tickets` — help tickets from `/help` and the app: queue by status
    (open, waiting, closed) and assignee; reply, internal note, reply-and-close,
    assign, close, reopen. No email is sent (the website has no mail path);
    replies show on the site and in the app, plus an FCM push when configured.
  - `/admin/crashes` — crash reports grouped by signature, filters (source,
    version, title, error code, date, state), detail with fields and file,
    resolve (one or the group), link to a ticket.
  - `/admin/console-news` — Switch News tab channels and articles, Publish
    (build status and errors) and Rollback, through the news service's admin
    API (`NEWS_ADMIN_URL`, `NEWS_ADMIN_KEY`). Hidden when unset.
  - `/admin/store` — homebrew store titles, metadata, icon, versions (NSP
    streamed to nx-baas, never buffered), publish/unpublish, download stats,
    through nx-baas's store admin API (`STORE_ADMIN_URL`, `STORE_ADMIN_KEY`
    or `NX_INTERNAL_KEY`). Hidden when unset.
- **Avatars**: uploaded avatars are validated, center-cropped and resized
  server-side to 256 and 512 px PNG under `WEBSITE_MEDIA_DIR` (filesystem
  first; `internal/media.Storage` is the S3-compatible seam). Both PNGs are
  also published to the core's Media service (`PutAvatar`, best effort and
  logged) so adapters serve them to consoles; the website keeps serving its
  own copies.
- **Account security** (`/account#security`): email address and whether it
  is confirmed (resend button), email change (the mailed link lands on
  `GET /email/confirm`, a page with a button), and two-factor authentication:
  setup (key and `otpauth://` URI as text — no QR, as nothing in the standard
  library or the current dependencies draws one), enable (ten recovery codes,
  shown once), disable. The page warns before enabling that consoles signing
  in with the password (Switch, Wii U, 3DS) cannot complete a second factor
  yet. Sign-in for a 2FA account is a second form (`POST /login/2fa`), and
  deleting the account asks for a code as well as the password. Blocked
  accounts are listed with an unblock button.
- **Logs**: structured to stderr at info; set `WEBSITE_LOG_LEVEL=debug` to also
  log every request.

## Registry service

The v2 network registry runs as its own process, `cmd/openpak-registry`, from
the same image (command `/app/openpak-registry`): it serves `/api/v2/network/*`
and the operator's `GET /internal/status`, and its signing key and database
never load into the website. Its variables (`REGISTRY_*`) belong to that
container, except `REGISTRY_ADMIN_KEY`, which the website also holds for
`/admin/registry`. Ports, configuration and the steps that must happen before
releasing a website without the in-process registry: [registry.md](registry.md).

## Host agent (openpak-opsd)

`cmd/openpak-opsd` runs once per host, outside any container, and is the only
thing that talks to podman; the website never holds the podman socket.
`/admin/services` asks each agent listed in `WEBSITE_OPSD_HOSTS` with the shared
`WEBSITE_OPSD_KEY`. Install and configuration: [opsd.md](opsd.md).

## Code layout

```
cmd/website/            the openpak.org server (site, portal, /admin, /api/v1)
cmd/openpak-registry/   v2 network registry service, /api/v2/network/* (docs/registry.md)
cmd/openpak-opsd/       per-host agent behind /admin/services (docs/opsd.md)
cmd/openpak-policy/     operator console for v2 policy: publish, rollback, withdraw, inspect
cmd/openpak-network/    CI-side fetch tool: authenticate, fetch, verify a v2 bundle
internal/accounts/      account actions shared by the pages and the API
internal/adapter/       client for the Switch (and Wii U/3DS) adapter internal API
internal/admin/         role-gated operator/moderation pages under /admin
internal/attachments/   ticket and crash-report files in Postgres (old WEBSITE_ATTACHMENTS_DIR imported once)
internal/api/           /api/v1 mobile API (+ openapi/openapi.yaml contract)
internal/chat/          client for the chat service's internal API
internal/config/        environment configuration
internal/erasure/       account-erasure finaliser: after the grace period, every service, then the core
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
internal/store/         website-owned Postgres (config, tokens, audit, media, titles,
                        bans, help tickets, crash reports)
internal/support/       help-ticket flows shared by /help, /api/v1/me/tickets, /admin/tickets
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

`web/static/site.css` is the whole design system, built from `web/src/input.css`.
The site behaves like a console's own system UI: the landing page is a HOME
screen (a shelf of title tiles, a row of round system buttons), the account is a
user page, and `/admin` is laid out like System Settings (`adminnav` opens that
shell, `adminend` closes it). One element carries the personality: the
selection cursor, a cyan ring that breathes around whatever is hovered or
focused, which is also the keyboard focus indicator. Cyan only ever means "you
are here"; indigo is links, primary actions and beta. Headings and UI text use
M PLUS Rounded 1c (self-hosted in `web/static/fonts`, OFL), reading text the
system face. All UI copy lives in `internal/i18n`, and the render CSP is
`style-src 'self'`, so templates carry no inline `style` attributes.

## Testing

`go vet ./...` and `go test -race ./...` are the definition of done and run
in CI. The register/verify/login/friend-code/link-revoke, invitation and block
journeys are covered end to end in `internal/pages/journey_test.go` and
`internal/api/api_test.go` against contract-faithful fakes.
