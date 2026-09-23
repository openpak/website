# Changelog — website

Written by hand for recent releases; older entries are the commit subjects of each
release, taken from git history. `git log` stays the source of truth.

## v0.55.1 — every friend code in /me

- `GET /api/v1/me` returns `friend_codes`: the list the profile banner shows (kind, console,
  label, code), so the app can show the 3DS code and, once published, the Wii U NNID.
  `openpak_code` and `friend_code` stay, read from the same list. The banner and `/me` share
  `accounts.FriendCodes`, so they cannot list different codes.

## v0.55.0 — friend codes and an OpenPak code

- **OpenPak codes.** Every account has one (`OP-XXXX-XXXX`, minted on first use, table
  `openpak_codes`, migration `0012`), for adding friends whatever console either of you has.
  Website-owned: only the site and the app take one. `GET /api/v1/me` returns `openpak_code`.
- **The profile banner lists every code** people can add you by: OpenPak, Switch, the 3DS
  friend code (derived from the 3DS link's principal id exactly as nn-account derives it), and
  Wii U's NNID once nn-account publishes it on the link. Each has a copy button. The email and
  its verified state sit under the name.
- **Add friend** is a button on the Friends card that opens a dialog: pick the kind of code
  (OpenPak, Switch, 3DS), type it into a field masked to that shape, send. Works without
  script (one pattern accepts all three). `POST /api/v1/me/friends/requests` takes an optional
  `kind`; omitted, an OpenPak-shaped code is taken as one and anything else as Switch.
- Wii U and 3DS links now show under Linked platforms, without a revoke button: they are
  nn-account's console accounts.

## v0.54.1 — account page fixes

- The account section nav becomes the sidebar from 48rem instead of 60rem, so a laptop-sized
  window no longer cuts the row off; on a phone the row fades at its right edge to show it scrolls.
- The profile card: only the avatar reaches into the cover, the name and friend code sit below
  it, and the code has room before its copy button.
- `/titles?console=…` scrolls the chosen console chip into view on a phone.

## v0.54.0 — redesign

- **New look across the site.** Same indigo from the logo, a warmer dark palette, pill buttons,
  larger cards and one set of status colours: live green, **beta indigo** (it runs, so it is no
  longer amber), alpha/partial amber, planned grey, down red (`badgeClass` returns `beta`).
- **Mobile first chrome.** Glass header; below 60rem the links move into a bottom tab bar (Home,
  Titles, Status, Get started, Account). Plain links, so every page still works without script.
  The footer is three link columns with the language picker, which now submits on change.
- **Landing** is a live dashboard: a hero with the people online, in a game and the titles
  being played right now (from the session leases, hidden if they cannot be read), the
  platform tiles, how it works, a feature grid with cloud saves and chat, news and titles.
- **Titles and features** share one filter bar: search (`/` focuses it), a state toggle and
  console chips over a card grid. `/titles?console=Wii` opens with that console chosen.
- **Status** is a summary banner with counts and four panels. **Downloads** is a card per
  client. **Account** has a section nav (sidebar on desktop, a swipeable row on a phone), a
  profile card with a copyable friend code, and every section in its own card.
- `web/static/site.js`: the copy button and the language auto-submit; both optional.
- New strings in all six languages (`nav.home`, `nav.getstarted`, `landing.live.*`,
  `landing.trust.*`, `landing.f4/f5.*`, `landing.cta.*`, `landing.head.accent`, `footer.*`,
  `account.copy/copied`).

## v0.53.1 — voice chat hardening

- A phone may send at most 40 signaling frames a second; the rest are ignored. Before, one
  phone flooding a room could fill another phone's queue and get that phone dropped.

## v0.53.0 — voice chat

- **Voice chat for the phone app** (`internal/voice`). As on Nintendo's service the audio
  never touches the console: the app talks WebRTC phone to phone and this process only
  holds rooms and relays signaling. `GET /api/v1/me/voice` finds the room of the game you
  are in (the matchmaking gathering, asked of the title servers in `VOICE_GATHERING_URLS`;
  switch-nex v0.3.1 answers), `POST /api/v1/me/voice/rooms` makes a code room,
  `GET /api/v1/me/voice/rooms/{code}` shows one, and `GET /api/v1/me/voice/ws?code=` is the
  signaling WebSocket (bearer in the header, or subprotocol `bearer.<token>` from a
  browser). Full mesh, 8 phones a room. The welcome carries STUN and a time-limited TURN
  credential for coturn's shared secret (`VOICE_TURN_SECRET`, `VOICE_TURN_URLS`,
  `VOICE_STUN_URLS`). `GET /internal/voice/room?account_id=` (`WEBSITE_INTERNAL_KEY`) is the
  console lobby page's data, through nx-baas. New error codes `voice_room_unknown`,
  `voice_room_full`. New dependency `github.com/coder/websocket`.

## Unreleased (released in v0.52.0)

- **Help tickets.** Signed-in people open a ticket on `/help` (`/support` redirects there):
  subject, topic, message and an optional file (10 MB), then follow and answer the thread.
  The app gets the same under `/api/v1/me/tickets` (list, open, thread, reply, attachment;
  JSON or multipart; in openapi.yaml). `/admin/tickets` is the queue (open, waiting,
  closed, assigned to me) with replies, internal notes, reply-and-close, assign, close and
  reopen, all audited. The website has no mail path, so nothing is emailed: replies show
  on the site and in the app, plus an FCM push (`data.type = ticket_reply`) when push is
  configured. Migration 0010.
- **Crash reports.** `POST /api/v1/crash-reports` (public, 20 per 10 minutes per client,
  optional bearer links the account): multipart `meta` JSON + `attachment` up to 20 MB
  streamed to disk, or bare JSON; sources ryujinx, citron, eden, nro, app. `POST
  /api/v1/internal/crash-reports` takes nx-baas's console error reports with
  `X-Internal-Key` = `WEBSITE_INTERNAL_KEY` (only bad input is a 4xx). `/admin/crashes`
  groups by signature (source + error code + title + top frame or message, numbers
  blanked) with counts and versions, filters by source, version, title, error code, date
  and state, and a report shows its fields and file, resolves alone or with its group, and
  links to a ticket. Migration 0011.
- **Console news.** `/admin/console-news` authors the Switch News tab through the news
  service's admin API (`NEWS_ADMIN_URL`, `NEWS_ADMIN_KEY`): channels (create, edit, icon,
  retire/restore), articles (languages, body, footer, lifetime, published-at, pickup,
  priority, essential pickup, motif or image, game/shop/browser button; retire, restore,
  delete), Publish with the build it switched to or why nothing switched, Rollback to the
  previous or any kept build. Hidden when unset.
- **Store.** `/admin/store` manages the homebrew eShop through nx-baas's store admin API
  (`STORE_ADMIN_URL`, `STORE_ADMIN_KEY` or `NX_INTERNAL_KEY`): titles, metadata per
  language, icon, publish/unpublish, delete, per-title download stats, and version upload
  (base or update, with the content list nx-baas verifies the NSP against). The NSP is
  streamed browser → website → nx-baas and never buffered. Hidden when unset.
- **Bans everywhere.** `docs/ban-lookup.md` is the contract other services code against:
  the core's `Identity.GetAccount` status (plus the `account_banned` event), or the
  website's `GET /internal/bans/{account_id}` / `?namespace=&subject=` (internal key) with
  reason, actor and time. `/admin/users` lists banned accounts with who banned them and
  why (migration 0009, backfilled from `admin_audit`), and a ban drops the account's
  website sessions and bearers at once.
- Files for tickets and crash reports live under `WEBSITE_ATTACHMENTS_DIR`
  (default `storage/attachments`; mount a volume) and are always served as downloads.

## v0.51.0 — 2026-09-23

- A title holds every id it has: `title_ids` (one per region, both games of a pair like
  Pokémon X / Y), primary first, with `title_id` kept as the first. Shown in
  `/api/v1/catalog.json`, `/api/v1/titles` and the admin write answers; `PATCH
  /api/v1/admin/titles/{id}` takes `title_ids` to replace the list (a bare `title_id` still
  replaces only the primary, `""` clears them all). No id may belong to two titles.
- Migration 0007 adds the column; 0008 fills every Wii U (27, 84 ids) and 3DS title (39 of
  40, 154 ids) and 104 Switch titles that had none (156 ids), only where a row has no ids
  yet. Sources and cross-checks are in `internal/store/title_ids.go`.
- The title-name lookup (`/internal/titles/name`, and `title_name` in the friends APIs)
  matches any of a title's ids, and a Wii U or 3DS update id (`0005000E…` / `0004000E…`)
  names its game. `/internal/titles/name` also takes `?id=`.

## v0.50.0 — 2026-09-23

- Cross-network presence with client labels. Every online friend in `/api/v1/me/friends`
  gains `platform` (switch, wiiu, 3ds), `client` (switch, ryujinx, eden, citron, wiiu, 3ds,
  cemu, azahar, or empty), `label` (Switch, Ryujinx, Eden, Citron, Wii U, 3DS, Cemu,
  Azahar; the platform's name when the client is unknown) and `title_name` (from the
  catalogue, nlib for Switch titles; empty when unknown), flat on the row as before and
  again under `presence`. `/api/v1/me/switch` friends' `presence` objects gain the same
  four. The phone shows "<title_name> on <label>" / "Online on <label>".
- The account page's friends list says which emulator a friend is on ("Kirby · Ryujinx",
  "Nintendo Switch (Ryujinx)").
- `GET /internal/titles/name?namespace=&title_id=` (internal key): the title name the
  adapters put into a friend's presence line on another platform.

## v0.49.6 — 2026-09-23

- The Switch claims every family it reaches, Nintendo's and the third parties both:
  `.nintendowifi.net` (so the connection test reaches OpenPak's own answer rather than
  measuring somebody else's internet), `.among.us`, `.photonengine.io`, `.exitgames.com`,
  `.demonware.net`, `.epicgames.dev`, `.ea.com`, `.xboxlive.com`, `.live.com`,
  `.mojang.com` and `.microsoft.com`. A console on OpenPak now reaches OpenPak for
  everything it resolves; `never` and the per-name `exact` list are empty, because a
  family covers each of them.
- `Universe`, the ceiling a platform map may narrow from, grows to match. It is still a
  ceiling: a family outside it is a startup error, and no profile can ask a client to
  redirect a name its own build does not already allow.
- Two tests that had stopped reflecting the code are fixed, and `main` builds green again:
  the v2 journey verified a fixture publication against the wall clock, so it began
  failing seven days after it was written, and the rollback journey still required the
  bundle digest to survive a rollback that now reissues and re-signs the bundle.

## v0.49.1 — 2026-09-20

- Avatar URLs carry the media row's version: after changing the picture, the overview
  and `GET /api/v1/me` serve `/media/avatars/<id>/<size>?v=<…>` instead of the bare URL,
  so the browser and the app show the new picture at once instead of the cached one
  (`Cache-Control: max-age=300`).

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
