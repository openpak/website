# Changelog — website

Written by hand for recent releases; older entries are the commit subjects of each
release, taken from git history. `git log` stays the source of truth.

## v0.49.0 — 2026-09-19

- Catalog entries carry `title_id`: the console's 16-hex title id, upper case, unique when
  set, empty when unknown. Written through the catalog API like any field (logged, bumps
  last_updated) and returned by `/requests`, `/titles` and `/catalog.json`, so emulators and
  tools join on the id instead of the name. Migration 0006 fills it from notes that lead with
  one (14 Switch titles).

## v0.48.0 — 2026-09-19

The database is the catalog. openpak/catalog's catalog.json no longer feeds the site.

- Write API: `POST /api/v1/admin/titles`, `PATCH` and `DELETE /api/v1/admin/titles/{id}`,
  for titles and features alike. Auth is a signed-in admin (plus `X-CSRF-Token`) or the new
  `WEBSITE_CATALOG_KEY` bearer. Status must be one of live, working, beta, alpha, partial,
  planned, requestable, not-feasible (`scaffold` is gone from the admin forms). A duplicate
  (kind, name, console) and deleting a title with votes are 409s.
- `requests` gains `date_added` and `last_updated`; a new `request_changes` table logs every
  create, update and delete (actor, time, old and new value per field) in the write's
  transaction. The admin title forms go through the same store call.
- `GET /api/v1/requests` carries `sort`, `date_added` and `last_updated`; `GET
  /api/v1/catalog.json` exports the catalog in the old file's shape plus the two dates.
- Removed: the catalog refresh loop, the embedded catalog.json, `CATALOG_URL`,
  `CATALOG_REFRESH` and `WEBSITE_SEED_TITLES`.
- `scripts/backfill-request-dates`: the one-off that dates existing rows from the catalog
  repository's history, run once after migration 0005.

## v0.47.0 — 2026-09-19

The site speaks German, French, Brazilian Portuguese, Italian and Spanish.

- Languages: the UI language follows Accept-Language (q-values; `pt` and `pt-PT` read
  `pt-BR`, `de-AT` reads `de`); a `lang` cookie set by a footer form (`GET /lang`, no
  JavaScript) beats it. Five catalogues (`internal/i18n/catalogue_*.go`, machine-drafted and
  reviewed against `docs/glossary.md`, the shared terms and form of address for the site and
  the app); admin pages stay English. A test fails when a non-admin key is missing from any
  language. Page titles, flash messages, nav landmarks and long dates follow the language.
- Account emails: calls to the account core carry the page language as `Accept-Language`,
  so verification, email-change and reset mails arrive in it (needs account core with
  `internal/mailtext`, 2a69299; older cores send English).
- API errors carry a stable `code` beside the English `error` (33 codes, listed in
  `openapi.yaml`) for the app to translate; a test fails when a mapped message is reworded.
- Atom feeds: `/feed/news.xml` (published articles) and `/feed/titles.xml` (title status
  changes from the `title.status` audit rows; notes and the admin stay out), linked from
  every page and cached five minutes.

## v0.46.0 — 2026-09-19

Wired to the account core v0.9.0 contracts (stubs refreshed from account 7b87201).

- Sign-in: the core names the session's account (`GET /v1/whoami`, CR-4); the password is no
  longer checked a second time over gRPC. Two-factor accounts get a second step on the web
  (`POST /login/2fa`, authenticator or recovery code) and in the API (`POST /api/v1/token`
  answers `202 {two_factor_required, challenge}`; `POST /api/v1/token/2fa` mints the bearer).
- Account security: two-factor setup (key and otpauth URI as text), enable (ten recovery codes
  shown once) and disable, with a warning first that consoles signing in with the password
  (Switch, Wii U, 3DS) cannot complete a second factor yet. Email change with a confirmation page
  at `/email/confirm`; a resend button while the address is unconfirmed. API:
  `/api/v1/me/2fa/setup|enable|disable`, `/api/v1/me/email`, `/api/v1/email/confirm`,
  `/api/v1/me/verify/resend`; `/api/v1/me` gains `email_verified` and `two_factor_enabled`.
- Deleting a 2FA account asks for a code as well as the password (web and `DELETE /api/v1/me`).
- Blocked accounts: listed on the account page with unblock; `GET /api/v1/me/blocked`.
- Friend codes: the code the adapter publishes on the link (`public_code`) is shown and looked
  up first; the adapter answer stays the fallback until adapters publish.
- Avatars: both PNG sizes are also published to the core's Media service (CR-2), best effort.
- Admin: `/admin/users` (search, ban/unban with a reason) and `/admin/reports` (queue, resolve)
  over the core's Moderation service (CR-5, CR-6), audited in the core under the admin's id and
  in `admin_audit`.
- `GET /api/v1/news`: the latest published articles for the app; empty when no news service is
  configured.
- Admin pages follow the public site's design: row lists with status badges instead of wide
  tables, one page head, consistent buttons.
- Account page: the profile, password, Wii/DS link and delete forms draw their inputs again (they
  used an undefined `stack` class); the avatar picker is a modal again.
- Downloads: the titles list's rows — logo, name, status, a Download button for the public
  releases — instead of step lists, per-platform paragraphs and a four-column table.
- Status: the switch-nexd titles are probed on their host health ports (switch-nex v0.2.16), and
  3D World, Kirby's Dream Buffet and Clubhouse Games 51 get rows.
- OpenAPI: the Switch, Wii U and 3DS emulator identity responses have schemas.

## v0.45.0 — 2026-09-19

- Registry: the v2 network registry runs as its own service, `cmd/openpak-registry` (same image,
  command `/app/openpak-registry`, ports 20140/20142); the website no longer serves `/api/v2`,
  holds the signing key or links sigstore/TUF. `GET /internal/status` for the admin page.
- Admin: `/admin/monitor`, `/admin/services` (every container on every host via
  `openpak-opsd`, filter by network, restart/start/stop/update, update badge), `/admin/news`,
  `/admin/titles`, `/admin/mods`, `/admin/registry`; a shared sub-nav; title-status and banner
  changes are audited; events show the newest 200.
- `openpak-opsd`: the per-host agent behind the services page (docs/opsd.md).
- Account: delete the account (password asked again), web and `DELETE /api/v1/me`; display name
  editable on the web; friends list shows who is online; decline an invitation on the web.
- Chat asks the core before sending, so a block is refused up front.
- API: decline a friend request, `GET /api/v1/titles`, `POST /api/v1/password/reset`; a profile
  edit that leaves out country or birthday keeps them.
- Landing: the latest news articles, when the news service is configured.
- Push: a fresh install starts at the newest event instead of replaying the history.
- Pages and API share the Wii/DS link and storage OAuth state (`internal/accounts`).
- OpenAPI validates; docs, example.env and CI (Go 1.27) brought up to date.

## v0.44.4 — 2026-09-19

- Security: every `/admin` form checks a CSRF token (config, federation node create and
  approve/revoke); signing out from an admin page works again.
- Status: the Switch-era NEX titles run inside `switch-nexd`, which has no health endpoint, so
  their rows say "Not checked" instead of reporting running servers as down. The banner now
  counts a game server that is not answering.
- Status: Wii U, 3DS, Wii and DS networks read "Beta" (DS was missing).
- Downloads: links to the published Wii U, 3DS, Wii and DS clients (Inkay, nimbus, the WFC
  patcher, Azahar, Dolphin, melonDS); platform states match the deployed networks.
- Copy: supported titles no longer claim to be "known to work"; invitations say joining from one
  does not work yet; landing mentions chat, cloud saves and Wii/DS linking; stale feature
  strings removed from the catalogue.
- Catalog: feature statuses follow the catalog (title statuses stay admin-owned), and a row the
  catalog drops is removed unless someone voted for it. The duplicate "Nintendo DS network"
  row is gone.
- Repository: the accidentally committed `openpak-policy` binary is removed and ignored.

## v0.44.3 — 2026-09-19

- Catalog: Dinkum and Kirby's Dream Buffet are beta.
- Saves: the list is sorted by game name.
- API: `POST /api/v1/register` and `POST /api/v1/password/forgot` (public, rate limited per IP,
  same answer whether or not the address exists); `POST /api/v1/me/password`.
- API: `POST /api/v1/me/links/wfc` links a Wii/DS by friend code; `DELETE /api/v1/me/links/{namespace}`
  unlinks the caller's own console.
- API: `POST /api/v1/me/avatar` also takes a multipart photo upload (`avatar`, PNG/JPEG ≤ 4 MiB);
  choosing an avatar through the API now stamps the media row, so consoles pick up the change.
- API: chat conversations serialise as `peer`/`newest` (were `Peer`/`Newest`).
- Security: `/account/link/revoke` only revokes one of the signed-in account's own links.
- OpenAPI: chat, saves, storage and `/me/switch*` routes documented; the contract test now reads
  every route file in `internal/api`, not only `api.go`.

## v0.44.2 — 2026-09-18

- API: restore, download and delete a save version (`/api/v1/me/saves/versions/{id}`), the
  same actions the saves page has, for the app.

## v0.44.1 — 2026-09-18

- API: `GET /api/v1/me/saves` names each game and gives its icon path, the same the saves
  page shows, so the app can draw them.

## v0.44.0 — 2026-09-18

- Saves: one row per game — icon, name, console logo, a version dropdown (picking one makes it
  current) and download/delete icon buttons; search and console filters above the list.
- Saves: the pin button is gone; the storage usage bar sits inside its row, themed.
- Saves: the storage section's missing strings (Google Drive, OneDrive, S3, WebDAV) are filled in.

## v0.43.2 — 2026-09-18

- Saves: Switch games show their icon and name from nlib; icons are relayed through
  `/titles/icon/{id}` so a player's game list never leaves this site from their browser.

## v0.43.1 — 2026-09-18

- Saves: each game shows its console logo, name and console instead of a bare title id.

## v0.43.0 — 2026-09-18

- Catalog: Super Mario 3D World + Bowser's Fury, Kirby's Dream Buffet and Clubhouse Games 51
  (served by switch-nexd) and Human Fall Flat and Dinkum (registered in npln) are alpha.
- `POST /api/v1/me/switch/link`: the console-link token for a signed-in emulator.

## v0.42.1 — 2026-09-15

- api: a half-configured registry names its missing variables

## v0.42.0 — 2026-09-15

- registry: the root is provisioned once, not minted at every restart

## v0.41.4 — 2026-09-14

- fetchtool: materialize nested payload paths

## v0.41.3 — 2026-09-14

- openpak-network: authenticate exchanges the CI identity for a build credential

## v0.41.2 — 2026-09-14

- registry: an empty registration scope is an empty array, not a null

## v0.41.1 — 2026-09-14

- Dockerfile: the builder image tracks go.mod's 1.27

## v0.41.0 — 2026-09-14

- netprofile: battle.net joins the universe
- policy: the v2 canonical policy, its schema and deterministic generation
- registry: revisions, sequences, atomic promotion and TUF signing
- api: the /api/v2/network distribution surface
- openpak-network: the certified build-time fetch tool
- openpak-policy: the operator console for the registry
- release: publish static openpak-network binaries and checksums

## v0.40.0 — 2026-09-13

- netprofile: names with an address of their own

## v0.39.0 — 2026-09-12

- release: build on the self-hosted runner with podman; private-repo minutes are metered
- release: job-scoped registry auth file
- WD-0: the app sees the Wii/DS link
- NP-0..NP-3: the network profile, generated from the live routing
- chat + catalog + friend-code lookup: land the remaining working tree
- release: REGISTRY_AUTH_FILE belongs on the steps

## v0.38.0 — 2026-09-11

- API: /me/wiiu and /me/3ds, the account's console identity from nn-account
- gofmt

## v0.37.0 — 2026-09-11

- Titles: the list is what has a server; the long tail is a Request-a-game dialog

## v0.36.0 — 2026-09-11

- Titles: the database is the source of truth for status

## v0.35.0 — 2026-09-11

- Titles: playable first, by console, alphabetically

## v0.34.0 — 2026-09-11

- Catalog: fetch the live openpak/catalog on a timer and reseed

## v0.33.0 — 2026-09-11

- Catalog: Mario vs. Donkey Kong: Tipping Stars

## v0.32.0 — 2026-09-11

- Catalog: fourteen more Wii U/3DS servers and Mario Kart 8 (Wii U)

## v0.31.0 — 2026-09-11

- Catalog: sync with docs/catalog (Wii U/3DS game servers, NPLN and Switch additions, statuses)

## v0.30.0 — 2026-09-11

- Proxy the mod catalogue under /api/v1 when MODS_URL is set

## v0.29.0 — 2026-09-10

- Link a Wii or DS from a friend code

## v0.28.0 — 2026-09-10

- Push: tell the requester when a friend request is accepted

## v0.27.1 — 2026-09-10

- Saves page: one-click clouds first, the bucket and WebDAV forms behind advanced

## v0.27.0 — 2026-09-10

- Storage of every kind: S3, WebDAV, Google Drive and OneDrive

## v0.26.0 — 2026-09-10

- Storage settings on the mobile API

## v0.25.1 — 2026-09-10

- Send the adapter the header it checks

## v0.25.0 — 2026-09-10

- The emulator surface: /api/v1/me/switch and the CA

## v0.24.2 — 2026-09-10

- Console marks: their real colours, and a variant that survives dark mode

## v0.24.1 — 2026-09-10

- Console marks: the real logos in a 3:2 slot, with credits

## v0.24.0 — 2026-09-10

- Cloud saves: the saves page, your own storage, and the emulator API

## v0.23.0 — 2026-09-10

- catalog: games for all eleven consoles, not just Switch

## v0.22.0 — 2026-09-10

- titles/features: match the app — coloured status pills, single-line feature rows

## v0.21.0 — 2026-09-10

- titles: console logo + name left, status pill or Request button right; ranking is admin-only

## v0.20.0 — 2026-09-10

- requests: votes for not-yet-playable games only; features display-only
- requests: enforce the votable rule server-side, not just in the UI

## v0.19.1 — 2026-09-10

- requests API: optional-auth list + path-param vote for the app

## v0.19.0 — 2026-09-10

- requests: votable titles + features seeded from the catalog
- site: embed the full 157-entry catalog (132 titles) for request seeding

## v0.18.1 — 2026-09-10

- site: fix /titles 500 (use $.Lang inside the titles range) + render test

## v0.18.0 — 2026-09-10

- site: featured titles on the landing page + a filterable/searchable /titles catalogue

## v0.17.0 — 2026-09-10

- site: list the 15 NEX titles + Splatoon 3 in the catalog and game-servers health page
- site: alphabetize the title catalog and game-servers list

## v0.16.0 — 2026-09-09

- Region nodes: public list, announce relay, admin page

## v0.15.5 — 2026-09-08

- Say online where the number means online
- Show the friend code, not the console's user id dressed as one

## v0.15.4 — 2026-09-08

- console.jpg answers for every account, not just consoles that uploaded

## v0.15.3 — 2026-09-08

- Every account has a face

## v0.15.2 — 2026-09-08

- The console's avatar reaches the site but never moves its version

## v0.15.1 — 2026-09-08

- docs: bring website's docs in line with what is deployed
- A core that cannot answer must not sign a phone out

## v0.15.0 — 2026-09-08

- An avatar set on a console is the account's avatar

## v0.14.1 — 2026-09-08

- Status rows are one line

## v0.14.0 — 2026-09-08

- Status rows carry their state in the tint

## v0.13.0 — 2026-09-08

- Rebuild the site on Tailwind, mobile first

## v0.12.0 — 2026-09-08

- Document the status page's health checks and the off switch
- Make the site behave on a phone

## v0.11.0 — 2026-09-08

- Check the game servers for real, and publish the numbers

## v0.10.0 — 2026-09-08

- Game servers and console networks on the status page

## v0.9.0 — 2026-09-08

- A status page about games and players, not about processes

## v0.8.0 — 2026-09-08

- Profile editing and avatars on the mobile API

## v0.7.0 — 2026-09-08

- Push notifications, driven by the core's event stream

## v0.6.0 — 2026-09-08

- Presence and an invitations inbox on the mobile API

## v0.5.1 — 2026-09-08

- Country and date of birth on the account page

## v0.5.0 — 2026-09-07

- Ask for country and date of birth when creating an account

## v0.4.0 — 2026-09-07

- Give the site the brand's colour and more presence

## v0.3.0 — 2026-09-07

- Change your password from the account page, and stop caches serving stale CSS

## v0.2.1 — 2026-09-07

- Adopt the OpenPak logo: site icon and header mark

## v0.2.0 — 2026-09-07

- Site UI pass: new landing/features/downloads, avatar presets, friend codes

## v0.1.2 — 2026-09-07

- CI: drop the sibling account checkout, the contracts are vendored
- Verification links worked nowhere: JoinPath ate the query string

## v0.1.1 — 2026-09-07

- Vendor the account-core stubs so the image builds outside the workspace

## v0.1.0 — 2026-09-07

- Ship a container image on tag: rootless-ready Dockerfile + ghcr release workflow [d7df6d3]
- store: idempotent migration SQL (safe re-run after schema_migrations loss) [f970d78]
- website: openpak.org site, account portal, mobile /api/v1 (W1-W4) [dac898e]
