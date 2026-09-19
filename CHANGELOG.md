# Changelog — website

Generated from git history on 2026-09-15. `git log` stays the source
of truth; this file is the readable summary.

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

## v0.42.0 — 2026-09-15



## v0.41.4 — 2026-09-14



## v0.41.3 — 2026-09-14



## v0.41.2 — 2026-09-14



## v0.41.1 — 2026-09-14



## v0.41.0 — 2026-09-14



## v0.40.0 — 2026-09-13



## v0.39.0 — 2026-09-12



## v0.38.0 — 2026-09-11



## v0.37.0 — 2026-09-11



## v0.36.0 — 2026-09-11



## v0.35.0 — 2026-09-11



## v0.34.0 — 2026-09-11



## v0.33.0 — 2026-09-11



## v0.32.0 — 2026-09-11



## v0.31.0 — 2026-09-11



## v0.30.0 — 2026-09-11



## v0.29.0 — 2026-09-10



## v0.28.0 — 2026-09-10



## v0.27.1 — 2026-09-10



## v0.27.0 — 2026-09-10



## v0.26.0 — 2026-09-10



## v0.25.1 — 2026-09-10



## v0.25.0 — 2026-09-10



## v0.24.2 — 2026-09-10



## v0.24.1 — 2026-09-10



## v0.24.0 — 2026-09-10



## v0.23.0 — 2026-09-10



## v0.22.0 — 2026-09-10



## v0.21.0 — 2026-09-10



## v0.20.0 — 2026-09-10



## v0.19.1 — 2026-09-10



## v0.19.0 — 2026-09-10



## v0.18.1 — 2026-09-10



## v0.18.0 — 2026-09-10



## v0.17.0 — 2026-09-10



## v0.16.0 — 2026-09-09



## v0.15.5 — 2026-09-08



## v0.15.4 — 2026-09-08



## v0.15.3 — 2026-09-08



## v0.15.2 — 2026-09-08



## v0.15.1 — 2026-09-08



## v0.15.0 — 2026-09-08



## v0.14.1 — 2026-09-08



## v0.14.0 — 2026-09-08



## v0.13.0 — 2026-09-08



## v0.12.0 — 2026-09-08



## v0.11.0 — 2026-09-08



## v0.9.0 — 2026-09-08



## v0.8.0 — 2026-09-08



## v0.7.0 — 2026-09-08



## v0.6.0 — 2026-09-08



## v0.5.1 — 2026-09-08



## v0.5.0 — 2026-09-07



## v0.4.0 — 2026-09-07



## v0.3.0 — 2026-09-07



## v0.2.1 — 2026-09-07



## v0.2.0 — 2026-09-07



## v0.1.2 — 2026-09-07



## v0.1.1 — 2026-09-07



## v0.1.0 — 2026-09-07



## v0.1.0 — 2026-09-07

- Ship a container image on tag: rootless-ready Dockerfile + ghcr release workflow [d7df6d3]
- store: idempotent migration SQL (safe re-run after schema_migrations loss) [f970d78]
- website: openpak.org site, account portal, mobile /api/v1 (W1-W4) [dac898e]

