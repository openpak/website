# Deploy: website

- Module `github.com/openpak/website`, Go 1.27, AGPL-3.0-only (`LICENSE`).

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
