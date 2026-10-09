# Changelog — website

Written by hand for recent releases; older entries are the commit subjects of each
release, taken from git history. `git log` stays the source of truth.

## v0.70.0 — 2026-10-09

- **Game tiles on /titles.** Every title is a square tile as on the HOME shelf: the art, then the name, console and status underneath (nothing sits on the art). A title not served yet is dimmed, like archived software on a console.
- **Real cover tiles for the other consoles.** 276 of the 296 non-Switch titles get a 256 px tile cut from the game's real art: a SteamGridDB square grid that is only the game's own art and title logo, otherwise the published cover cropped past its platform band and badges. Sources and SteamGridDB authors are in `web/static/art/titles/SOURCES.tsv`; `scripts/title-art` rebuilds them and makes pick sheets for new titles. The titles grid, the title dialog and the HOME shelf use them; a title without one keeps its console's mark.
- **OpenPak is a real console or Harbor.** A Switch title's compatibility grid is the Switch and Harbor (Windows, macOS, Linux, Android, iOS); other consoles list only the consoles that play them. Playability accepts `Harbor`. The downloads page lists Harbor (not public yet) in place of the retired emulator forks, with Harbor's own mark.

## v0.69.0 — 2026-10-09

- **Redesign: the site is a console's system UI.** The landing page is a HOME screen: a shelf of title tiles (what is being played right now first, with real Switch icons, then the featured titles, then "See all supported titles") and a row of round system buttons. The account page is a user page with a large round user icon and the friend codes, and `/admin` is laid out like System Settings, with the sections on the left and the page on the right.
- The selection cursor, a cyan ring that breathes around whatever is hovered or focused, is the site-wide keyboard focus indicator. Cyan only marks where you are; indigo stays for links, primary actions and beta.
- New palette: a cool console-shell grey with white tiles in light mode, charcoal in dark mode. The gradients, glass effects and all-caps labels are gone. Headings and UI text use M PLUS Rounded 1c, self-hosted under `/static/fonts` (OFL).
- Switch title icons are fetched at 256 px instead of 128, so tiles stay sharp.
- The page preview harness also renders the titles, saves, parental and admin pages.

## v0.68.0 — 2026-10-08

- **Save Data Cloud management.** `/account/saves` lists the account's console backups per game (name and icon from the catalogue) with date, size and which console made each (`Harbor` or `Switch`; never a device id). A game's "Manage backups" page deletes one backup or all of them. Deleting is two steps: a confirmation page, then a POST carrying `confirm=yes` and the CSRF token, for the signed-in account only. A deleted backup leaves the console's own list at once.
- API: `DELETE /api/v1/me/saves/{platform}/{title}` deletes every backup of a title (`{"deleted": n}`, 404 when none); each version in `GET /api/v1/me/saves` carries `console` instead of `device`, and the list's `usage` is the account's use against its quota.
- breaking: the emulator save routes `GET`/`PUT /api/v1/me/saves/{platform}/{title}` answer 410 Gone; the cloud is console archives only (saves v0.4.0).

## v0.66.0 — 2026-09-30

- **Playability: how far a game itself runs, per client.** A second axis beside `status`, which
  says how far OpenPak serves a title's online play. Every title row in `GET
  /api/v1/catalog.json` and in the `titles` array of `GET /api/v1/titles` gains `playability`,
  always present and `{}` when nothing is known: `{"Ryujinx": "playable", "Citron": "ingame"}`.
  The five words are `playable`, `ingame`, `menus`, `boots`, `nothing` — the ones Ryujinx, Eden
  and Citron already localize — and the client is named the way the /titles grid spells it.
- `PATCH /api/v1/admin/titles/{id}` takes `{"playability": {"Citron": "ingame", "Eden": ""}}`.
  It merges: only the named clients change, an empty status deletes that client's row, and a
  client left out keeps what it had. It goes through `store.UpdateRequest` like every other
  title edit, so the `request_changes` line and the `last_updated` bump come from one place.
  Anything but the five words, or a client the grid does not name, is a 400.
- The title dialog on /titles has an admin control per client, next to the product links.
- Ryujinx's own compatibility list seeds the `Ryujinx` column (`scripts/seed-playability`);
  Eden and Citron start empty, since neither ships a working list today.

## v0.65.0 — 2026-09-27

- **Parental controls: play activity says who played** (nx-baas v0.48.0). In `GET
  /api/v1/me/parental-controls/{moonID}/activity` each title gains `players` `[{id, nickname,
  minutes}]`, most first, and `anonymous_minutes`, the time played with no user selected. The
  nickname comes from the console's own player list and is left out for a user it no longer
  lists; a title from an older nx-baas reads as `[]` and 0.
- The page shows, under each title, who played it and for how long, with the player's picture
  ("A user no longer on this console" for one it no longer lists), and "No user selected" as its
  own line only when there was such time. The wording is the app's, in all six languages.

## v0.64.0 — 2026-09-27

- **Parental controls: a parent names each console** (nx-baas v0.48.0), so two under one account
  can be told apart; the console never sends a name of its own. `PUT
  /api/v1/me/parental-controls/{moonID}/name {"name"}` answers 204, 400 `invalid_name` over 32
  characters (characters, not bytes) or with control characters, or 404. Surrounding whitespace
  is trimmed and an empty name clears it. Until nx-baas stores names it answers 502
  `service_unavailable` for a console the caller does manage.
- `GET /api/v1/me/parental-controls`: `nickname` is now the name to show (the parent's, else
  "Switch ···" and the last four of the serial number, else empty), and each console adds `named`
  (the parent chose it), `serial_number` and `firmware` (either may be empty).
- The page: a Rename button on each console (a form post, no password: a name is not a secret),
  and the serial number and system version in the console's status panel when known. The wording
  is the app's, in all six languages.

## v0.63.1 — 2026-09-27

- Parental controls page: removing a console asks for the account password again (and the
  second factor where there is one), like revealing or changing the PIN and saving restrictions
  (owner decision). The confirmation's wording is unchanged. `DELETE
  /api/v1/me/parental-controls/{moonID}` is unchanged: on the app, the phone's own unlock is the
  gate.

## v0.63.0 — 2026-09-27

- **Parental controls: what the parent changes** (nx-baas v0.46.0). `PUT
  /api/v1/me/parental-controls/{moonID}/setting {"setting"}` stores the whole document the console
  is served and answers `{"etag", "updated_at"}`. The client sends back what it was served with its
  changes applied, so keys it does not edit (the per-title entries a console reports) survive.
  Anything the console's parse table would not take is refused, not thinned: 400
  `{"error", "code": "invalid_setting", "path"}`, `path` naming the key, e.g.
  `customSettings.ageRestriction.ratingAge`.
- `PUT …/{moonID}/pin {"unlock_code"}` gives the console a new PIN at its next sync: 204, or 400
  `invalid_pin` unless it is exactly four digits (the console's entry field has four places).
  `Cache-Control: no-store`; the body is never logged.
- `DELETE …/{moonID}` takes a console off the account's list (204). The console is not told: it
  stays linked until it is unlinked there, with the PIN.
- `GET …/{moonID}/activity?days=7` (1 to 30, else 400) is the time used per day, newest first,
  and per title, named from the titles catalogue (`name` absent for a title it does not know).
  A day's `total_minutes` is the console's own count, HOME menu included, so it can be more than
  its titles add up to.
- Every new route answers 404 for a console another account manages, the same as for one that
  does not exist.
- **The page edits too.** `/account/parental` gains, per console, a "Change restrictions" editor
  with the parse table's fields only (level; under Custom, the age limit, rating system,
  communication, social media and VR; the play timer, every day or each day of the week, with
  minutes a day up to 360, bedtime and what happens when time is up), the per-title exceptions
  and always-allowed games read-only by name, "Change PIN" (asked twice), "Remove console" behind
  a confirmation, and play activity for the last 7 days. The editor writes only the controls the
  parent changed into the served document, refuses a form drawn before the document changed
  elsewhere, and after a save says "Saved. Waiting for the console." until the console's receipt
  names it — never "applied". Saving and changing the PIN ask for the password again (and the
  second factor where there is one), like revealing it. Every write is a form post with the
  site's CSRF token; the Custom and per-day controls are shown and hidden by CSS, with no script.
  The wording is the app's, in all six languages; the PIN note now says it is changed here.

## v0.62.0 — 2026-09-27

- **Parental controls: what the parent reads** (nx-baas v0.45.0). `GET /api/v1/me/parental-controls`
  adds per console `nickname`, `setting` (the document as served to the console), `players`
  `[{id, nickname}]`, `synced_at` and `synced` (the console's last receipt names the document now
  served), and `wrong_pin_attempts` (unix seconds, newest first, at most 20). The PIN is not in
  the list.
- `GET /api/v1/me/parental-controls/{moonID}/pin` answers `{"unlock_code"}` with
  `Cache-Control: no-store`, and 404 until the console has reported its PIN.
  `GET …/{moonID}/players/{playerID}/image` relays a player's JPEG. Both answer 404 for a console
  another account manages, the same as for one that does not exist.
- `POST /api/v1/me/parental-controls/unlock {"password"}` answers 204 or 403 (`wrong_password`)
  for a phone that cannot ask for its own unlock: it checks the password the way every step-up
  here does, returns no token and changes nothing. Five tries per account per minute, then 429.
- **A parental-controls page**, `/account/parental`, linked from the account menu: the app's
  screen with the same fields, words and order. The code while one is live, then each console
  with its sync state, restrictions, players, PIN and wrong-PIN attempts, and the pair button.
  The PIN is shown only after the account password is given again (and a code, where the account
  has two-factor authentication), by a form post whose answer is the page itself, so it is never
  in a URL. The strings are the app's, in all six languages.

## v0.61.1 — 2026-09-27

- Parental controls: minting a pairing code sends the account's display name to nx-baas, so the
  console's confirmation screen names the parent instead of showing "OpenPak". A failed name
  lookup costs the name, not the pairing.

## v0.61.0 — 2026-09-26

- **Parental controls for the app** (app v0.15.0): `GET /api/v1/me/parental-controls` lists the
  consoles an account manages and `POST /api/v1/me/parental-controls/pairing` mints the code to
  type on one. Both relay to nx-baas with `X-Internal-Key` — the device records live with the
  console-facing service because that is what the console asks for them — so this needs
  `NX_BAAS_URL` and `NX_BAAS_INTERNAL_KEY`; unset, the routes answer 503 and say they are not
  configured, which is kept distinct from 502 when the service itself answers badly.
- The account is always the bearer's: no request field names an account, or one household could
  read another's consoles.

## v0.60.3 — 2026-09-26

- Titles: a shop without a stored product page falls back to that shop's own search (GameStop,
  Micromania), not a Google search. The product pages themselves are being filled from web
  searches.

## v0.60.2 — 2026-09-26

- Titles: the compatibility line under the heading is one short sentence ("Coloured: played
  online. Grey: untested."). v0.60.0 and v0.60.1 were never built; this carries both.

## v0.60.1 — 2026-09-26

- Titles: a French reader gets Micromania where everyone else gets GameStop (chosen by the page
  language). The admin form still edits all three shops. v0.60.0 was never built; this carries it.

## v0.60.0 — 2026-09-26

- Titles: buy buttons for CeX, GameStop and Micromania link the game's own product page when one
  is stored (table `title_shop_links`, pasted by an admin in the title's dialog — only https links on
  that shop's own site are accepted) and search until then: CeX's own search, a Google site search
  for GameStop and Micromania. ↗ marks a product page, a magnifier a search. All three shops sit
  behind Cloudflare, which challenges scripted lookups, so the links are not filled automatically.

## v0.59.2 — 2026-09-26

- Titles: the dialog's buttons sit in even columns that fill its width and stack full-width on a
  narrow phone; the store buttons carry an ↗ (they open another site) and Close is the primary one.

## v0.59.1 — 2026-09-26

- Titles: CeX and GameStop are two plain buttons side by side; the CeX storefront picker is gone
  (webuy.com already sends a visitor to their own country's store).
- Titles: the dialog no longer shows the category (NEX, NPLN, …); only when it was added and updated.
- Titles: player reports are switched off for now: no "it worked / it did not" form and no
  `POST /titles/report`. The grid runs on playtime; `title_reports` and its store stay.

## v0.59.0 — 2026-09-26

- Titles: playtime marks a combination tested. The core (account v0.11.0 `GetPlaytime`) reports
  how long each title has been played online per client and OS across every player; a cell turns
  green at 15 minutes in one session or 30 in total, and amber when failure reports disagree (to
  investigate, neither side outvotes the other). Emulators name their OS in `X-OpenPak-Client`
  (`ryujinx/1.3.2 (macos)`); a console counts on its hardware cell; an emulator that named no OS
  is left out. The tooltip shows the minutes played. Without the core call the page keeps player
  reports alone.
- Titles: a "Find it on GameStop" button (the US store; its other storefronts closed or changed
  hands) next to CeX.

## v0.58.2 — 2026-09-26

- Titles: the compatibility grid is icons — a row per console or emulator, a column per OS
  (Windows, macOS, Linux, Android, iOS). Colour means players report it working, greyscale means
  untested; an amber or red ring means mixed or failing reports. A blank cell is a client that does
  not ship there. The dialog drops network, title ids and notes, and no longer lists Switch 2.
  Citron, Azahar and PPSSPP gain iOS.

## v0.58.1 — 2026-09-26

- Titles: a "Report a problem" button in each title's dialog opens a help ticket with the
  subject, category (Switch, Wii U/3DS or other) and a message naming the title already filled
  in. `/help` accepts `subject`, `category` and `message` query parameters for this.

## v0.58.0 — 2026-09-26

- Titles: clicking a title opens its details (category, network, title ids, notes, dates), a
  compatibility grid of every console, backwards-compatible console and emulator × OS that can
  play it (untested until someone reports), a signed-in "it worked / it did not" report per
  combination (one per account, latest wins; table `title_reports`), and a CeX search link —
  webuy.com by default, which sends a visitor to their own country's store, or a remembered
  storefront. `#t-<id>` links straight to a title.

## v0.57.7 — 2026-09-25

- Network status lists Risk of Rain Returns (switch-nexd, 23002). It went live on block 23000
  and was console-verified playable the same evening, but the game-server health map is keyed
  by title id by hand, so it was absent from the page while the catalogue already said `live`.

## v0.57.6 — 2026-09-25

- Network status lists Streets of Rage 4 (switch-nexd, 22882).

## v0.57.5 — 2026-09-25

- Network status lists Moving Out 2. Its sessions will live on Photon (the Fusion stack Outbound
  runs), so it answers on Photon's health endpoint; its Team17 gate is in nx-baas.

## v0.57.4 — 2026-09-25

- Network status lists Terraria (NPLN, openpak-terraria:21117) and Risk of Rain (switch-nexd,
  22892). Both were serving but neither had a health URL, so neither appeared on the page.

## v0.57.3 — 2026-09-24

- healthz: GET /healthz/{saves,chat} answers up (200) or down (503) for the off-box status page
- deps: grpc 1.83.2, x/image 0.46, pgx 5.11 (Dependabot)

## v0.57.2 — signed redirect ceiling (v0.57.1 rebuilt)

- The website serves the signed redirect ceiling at `/api/v1/network/ceiling` and generates
  network profiles against it; `openpak-policy` signs it on the owner's machine
  (docs/signed-ceiling.md).
- v0.57.1 carried this but its image did not build (the CI scratch directory sat inside the
  build context); v0.57.2 is the same code with the image built.

## v0.57.0 — add a friend by email; the address among your friend codes

- Add friend has an "Email address" choice, on the site and in the API (`kind: "email"`). The
  answer is the same whether or not the address has an account, so the field cannot be used to
  find out who is registered; only an exact, active address sends a request.
- Your email is listed with your friend codes (with a copy button) and in `/me`'s
  `friend_codes`, instead of under the name in the banner.
- The banner shows the account's current name. It read a copy saved at sign-in, which could be
  blank, so it showed the email instead.

## v0.56.3 — friend codes one column until a wide desktop

- The profile's friend codes are one column on phones, tablets and laptops, two from 64rem,
  and a code never wraps mid-way (at 940px the Switch code broke onto two lines).

## v0.56.2 — console logos from Wikimedia Commons

- Switch, 3DS, DS, Wii, Wii U, PS3, PSP, Vita and Xbox 360 use their Commons logos, generated
  into `web/static/consoles/` by the new `scripts/consoles.py`: cropped to the drawn art,
  fitted to the 36x24 slot, and given the contrast rule in both directions (lighter for the
  dark file, darker for the light one). `CREDITS.md` lists the sources.
- Console tiles are 3:2 instead of square, and the filter chips' logos wider, so the
  wordmarks are not shrunk to a hairline.

## v0.56.1 — two game servers named

- The status page named two game servers by their title id: Mario Golf: Super Rush (its server
  is keyed by the update's id) and Splatoon 2 (the European edition's id). Both are named now.

## v0.56.0 — add a Wii U friend by NNID

- The add-friend dialog has a **Wii U Nintendo Network ID** option, resolved through the NNID
  nn-account (v0.8.0) publishes on the Wii U link. The API's `kind` takes `wiiu` too, and the
  profile's friend codes show the NNID once it is published.
- The per-kind patterns live in `site.js` only: a single static pattern could not accept a name
  without also accepting a malformed code. Without script the field takes any text and a code
  that is not one gets "no account for that code", as a wrong code always has.

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

## v0.52.0 — 2026-09-23

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

## v0.49.5 — 2026-09-20

- The catalogue names the game servers; the players file only knows where they answer.

## v0.49.4 — 2026-09-20

- Every game server has its title id, so its players are counted.

## v0.49.3 — 2026-09-20

- A game's players are counted whatever case its id arrives in.

## v0.49.2 — 2026-09-20

- Friends: the game they are playing, not a title id.
- docs: the hosts' ssh agent moved off /tmp.

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
