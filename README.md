# OpenPak website

openpak.org: the public site and the account portal (PRD `website-prd.md`).
Server-rendered Go service — registration, verification, sign-in, recovery,
profile with friend code and console links, friends, invitations view,
moderation, site configuration, status, and the versioned mobile API under
`/api/v1`. No JavaScript is required for any journey (WEB-10).

- Module `github.com/openpak/website`, Go 1.26, AGPL-3.0-only (`LICENSE`).
- The account core is consumed **only** through its versioned contracts:
  HTTP `/v1/*` for the web journeys and gRPC (Identity, Links, Social,
  Events) for everything else. The website never reads core storage and
  never forks the core.

## Layout

```
cmd/website/            entrypoint
internal/config/        environment configuration
internal/core/          account-core clients (HTTP /v1/*, gRPC) + interfaces
internal/session/       cookie session wrapper, CSRF, bearer hashing
internal/store/         website-owned Postgres (config, tokens, audit, media)
internal/pages/         server-rendered pages + embedded templates
internal/admin/         role-gated moderation/operator pages
internal/api/           /api/v1 mobile API (+ openapi/openapi.yaml contract)
internal/media/         avatar pipeline (validate, resize 256/512, storage)
internal/i18n/          single string catalogue (WEB-09)
internal/ratelimit/     fixed-window limiter (login, token minting, API)
web/static/             site.css + icon.svg (no build step, no JavaScript)
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

Local development expects a sibling checkout of the account core (the
generated contract stubs are imported from `openpak/account`, pinned in
`go.mod` via `replace`):

```
git clone git@github.com:Openpak/website.git
git clone git@github.com:Openpak/account.git   # sibling directory
cd website
cp example.env .env 2>/dev/null || true        # then export the values
go vet ./... && go test -race ./...
go run ./cmd/website
```

CI checks out `Openpak/account` at the pinned ref next to the workspace —
bump the `ref` in `.github/workflows/ci.yml` (and the local checkout) when
the core's contracts move.

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

### Push notifications

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
- **Registrations open/closed + banner**: `/admin/config` (WEB-07). Saves
  are audited in `admin_audit`.
- **Audit log**: `/admin/events` renders the core's durable invalidation
  event stream plus website audit entries.
- **Moderation (ban/unban/search, reports)**: fail-closed pending core
  contracts CR-5/CR-6 — see "Contract requests".
- **Avatars**: uploaded avatars are validated, center-cropped and resized
  server-side to 256 and 512 px PNG under `WEBSITE_MEDIA_DIR` (filesystem
  first; `internal/media.Storage` is the S3-compatible seam). Serving to
  consoles moves to the adapter once CR-2 lands.
- **Logs**: structured to stderr; requests at debug level.

### Mobile API (WEB-08)

Contract: [`openapi/openapi.yaml`](openapi/openapi.yaml) (OpenAPI 3.1).
`POST /api/v1/token` exchanges account credentials for a bearer token
bound to a live core web session; every API call re-validates that session,
so bans/logouts/password resets end mobile access with the core's
revocation window. Rate limits: 10 mints/min per client, 120 calls/min per
token.

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

- **CR-1** `Invitations.ListForAccount(account_id) → [pending invitations]`,
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
- **CR-7** `SetDisplayName(account_id, name)` — profile display name is
  currently read-only.

## Testing

`go vet ./...` and `go test -race ./...` are the definition of done and run
in CI. The W1/W2 acceptance journeys (register → verify → login → friend
code → link revoke; request → accept → visible on both sides; block →
unblock → remove) are covered end to end in `internal/pages/journey_test.go`
and `internal/api/api_test.go` against contract-faithful fakes.
