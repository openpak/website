# Protocol: website

- The account core is consumed **only** through its versioned contracts:
  HTTP `/v1/*` for the web journeys and gRPC (Identity, Links, Social,
  Events) for everything else. The website never reads core storage and
  never forks the core.

Switch tiles use `/titles/icon/{title_id}`. Published icons for games missing from the
upstream database can be placed in `web/static/art/switch/{BASE_TITLE_ID}.jpg` (uppercase),
with their source recorded in that directory's `SOURCES.tsv`. The icon route serves these
files before querying nlib, including when a caller supplies an update title ID.

## Runbook

For Harbor's per-title BCAT schedules, set `BCAT_URL` to the internal
HTTP origin serving OpenPak's dataset renderer, for example
`http://openpak-bcat:20044`. The website relays the public index,
manifest and file routes under `/api/emulator/v1/bcat/titles`, preserving
paths and cache validators. Unset configuration returns 503, upstream
connection failures return 502, and a reachable service's missing-title
404 is preserved. No player session is needed for these downloads. See
the [BCAT delivery runbook](../../news/docs/emulator-bcat-delivery.md) for
dataset validation and the separate native Switch requirements.

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

The same key guards `GET /internal/bans/{account_id}` (and `?namespace=&subject=`), the
HTTP form of the ban lookup in [ban-lookup.md](ban-lookup.md), and `POST
/api/v1/internal/crash-reports`, where nx-baas relays console error reports. Both accept
`Authorization: Bearer <key>` or `X-Internal-Key: <key>`. The crash route is documented in
the OpenAPI so nx-baas has a schema to code against; the ban lookup is not.

### Mobile API (WEB-08)

Contract: [`openapi/openapi.yaml`](../openapi/openapi.yaml) (OpenAPI 3.1).
`POST /api/v1/token` exchanges account credentials for a bearer token
bound to a live core web session; every API call re-validates that session,
so bans/logouts/password resets end mobile access with the core's
revocation window. A 2FA account's credentials answer `202
{two_factor_required, challenge}` instead, and `POST /api/v1/token/2fa`
(`challenge` plus `code` or `recovery_code`) mints the bearer. Rate limits:
10 mints/min per client, 120 calls/min per token.

The app can do everything the account pages do: register, request a reset
mail and complete the reset (`POST /api/v1/password/reset`; public, sharing the
per-client mint limit), change the password, delete the account
(`DELETE /api/v1/me` with the password), link a Wii/DS by friend code and unlink
a console by namespace, upload an avatar (multipart) or pick a preset, accept or
decline a friend request (`/me/friends/requests/decline`), decline an
invitation, read the public title catalogue (`GET /api/v1/titles`), chat, and
manage cloud saves. Account security has the same parity: two-factor
(`/me/2fa/setup|enable|disable`), email change (`POST /me/email`, and
`POST /email/confirm` for an app that opens the link itself), verification
resend (`POST /me/verify/resend`) and the block list (`GET /me/blocked`).
The title and feature catalog is the `requests` table; the database is its only source.
`POST /api/v1/admin/titles`, `PATCH`/`DELETE /api/v1/admin/titles/{id}` write it, as a
signed-in admin (with the page's CSRF token as `X-CSRF-Token`) or with the
`WEBSITE_CATALOG_KEY` bearer (unset: no bearer path). Every write bumps `last_updated` and
logs each changed field's old and new value in `request_changes`, in the same transaction;
the admin title forms call the same store method. `GET /api/v1/catalog.json` is a public,
read-only export in the old `openpak/catalog` file's shape plus `date_added` and
`last_updated`. Each entry has a `title_id` (16 hex, unique when set) for tools that
join on the console id.
Help tickets: `GET`/`POST /api/v1/me/tickets`, `GET /me/tickets/{id}`, `POST
/me/tickets/{id}/messages` and `GET /me/tickets/{id}/attachments/{message}` (JSON, or
multipart with a 10 MB `attachment`). Crash reports: `POST /api/v1/crash-reports` is public
(20 per 10 minutes per client, an optional bearer links the account; multipart `meta` +
`attachment` up to 20 MB, or bare JSON), and `POST /api/v1/internal/crash-reports` is
nx-baas's, with `X-Internal-Key` = `WEBSITE_INTERNAL_KEY`. Files for both live under
`WEBSITE_ATTACHMENTS_DIR` and are only ever served as downloads.
`GET /api/v1/news` is public: the latest published articles from the news
service (`NEWS_URL`), an empty list when none is configured. Where the site and the API do the same thing they share
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
