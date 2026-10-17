# OpenPak website and account portal PRD

Status: **live** at openpak.org (repo `website`); this document is the original brief (2026-09-07), kept as the reference for the API the phone app speaks.

## 1. Product objective

openpak.org: the public site and the account portal. A player registers, verifies e-mail,
logs in, sees their friend code and linked consoles, manages friends and invitations,
recovers a password, and reads status. Moderators ban, unban and review reports.
Everything the console and emulator cannot do themselves lives here.

## 2. Relationship to existing plans

- Bootstrap plan: repo `website`, Go HTTP service with server-rendered templates, "keep
  account logic in `account`". This PRD is that repo's first real scope.
- S1 workplan T3 remainder: "web-API surface for friends (website)". This PRD consumes the
  core's Social contract; if a web-facing HTTP route is missing in the core, request it
  (Contract requests) rather than calling gRPC from the browser.
- Companion apps PRD: the mobile API is served by this same Go service under `/api/v1`.

## 3. Evidence and current baseline

- Core HTTP already exists: `POST /v1/register`, `GET /v1/verify`, `POST /v1/login`,
  `POST /v1/logout`, `GET/DELETE /v1/sessions`, forgot/reset-password
  (`account/internal/web`). Core gRPC: Identity, Links, Social, Sessions, Invitations, Events.
- Observed earlier behaviour (evidence only): registration open/closed state, profile
  (name, avatar, Mii), friend code `SW-xxxx-xxxx-xxxx`, friends list with
  accept/decline/remove/block/favourite, play history, online counts per game, session
  list with revoke, admin ban/unban/reports, Discord link. Source: route list of
  `openpak-account` (paths only).

## 4. Users and journeys

1. New player: register, verify e-mail, log in, see friend code, see the console link the
   console created, revoke it.
2. Friends: search by friend code, request, other side accepts, both lists update; remove;
   block. Incoming invitations visible with title and time.
3. Recovery: forgot password, e-mail, reset, old sessions revoked.
4. Moderator: ban an account; console access stops within the core's revocation window;
   review reports; unban.
5. Operator: registrations open/closed toggle; status page.

## 5. Scope and non-goals

In: everything above; avatar upload (stored via the core as account media, served to
consoles by the adapter); public landing, supported-titles table, status page; mobile API.
Out: mods UI (mods PRD), news authoring (news PRD), game media, Discord linking, cloud
save browsing (saves PRD, link only), any console-facing hostname.

## 6. Independent implementation rules (apply verbatim)

- OpenPak servers are AGPL-3.0-only and OpenPak-authored.
- Go 1.27, module `github.com/openpak/<repo>`, AGPL-3.0-only, `LICENSE` verbatim from
  gnu.org. Server-rendered templates and minimal browser JavaScript for web surfaces.
  Native mobile code is the recorded non-Go exception.
- Talk to the account core **only** through its versioned contracts (gRPC: Identity, Links,
  Social, Sessions, Invitations, Events; HTTP `/v1/*`). Never read its database. If a
  contract is missing, write the proposal in this PRD's "Contract requests" section and
  raise it as an `account` issue; do not fork the core.
- Do not touch `Openpak/nx-baas` (the Switch adapter, owned by the T4/T5 agent) or the
  console-facing hostnames it serves. Console-side needs go into "Contract requests".
- No secrets, tokens, saves, or copyrighted game material in any repository. `example.env` only.
- Definition of done for every milestone: `go vet ./...` and `go test -race ./...` green in
  CI, a runbook in `README.md`, and the acceptance journey below reproduced by the owner.

## 7. Functional requirements

- WEB-01 Registration, verification, login, logout, recovery use the core `/v1/*` routes only.
- WEB-02 Sessions: cookie session bound to a core web session id; list and revoke devices.
- WEB-03 Profile: display name, avatar (PNG/JPEG, resized server-side to 256 and 512 px),
  friend code (read-only; ownership per CR-3), linked platforms (Links).
- WEB-04 Friends: list, search by friend code, request, accept, decline, remove, block,
  unblock; incoming and outgoing requests. Every write goes through Social.
- WEB-05 Invitations: read-only view of pending invitations. **Delivered 2026-09-08** —
  `Invitations.ListInvitations` (non-consuming, every namespace, no payload or nonce) plus
  `RejectInvitation`; the site and the app both read it, and `PollInvitations` remains the
  delivery path so a phone can never take an invitation away from a console.
- WEB-06 Moderation: role-gated admin pages: search users, ban/unban with reason, reports
  queue, audit log (core Events).
- WEB-07 Site config: registrations open/closed, banner message; website-owned table.
- WEB-08 Mobile API `/api/v1/*` (companion apps PRD) with bearer tokens minted from a core
  web session; rate-limited; versioned; OpenAPI 3.1 published.
- WEB-09 Accessibility and i18n basics: keyboard-navigable forms, strings in one catalogue.
- WEB-10 No JavaScript required for any journey; progressive enhancement only.

## 8. Architecture and ownership

Repo `website`: `cmd/website`, `internal/` packages for pages, api, admin and media,
Postgres for site-only tables (config, admin audit, media metadata), object storage for
avatars (filesystem first, S3-compatible interface). Talks to `account` over gRPC with its
own internal key. Deployed behind the existing Traefik on its own hostname; never on a
`*.nintendo.*` name.

## 9. Contract requests (raise as `account` issues)

- CR-1 `Invitations.ListForAccount` (read-only, no delivery side effects). **Delivered** as
  `Invitations.ListInvitations` in `account` v0.3.0, with `RejectInvitation` and
  `Sessions.GetPresence` alongside it; `Sessions.GetPlayerCounts` followed in v0.6.0.
- CR-2 Account media: store and serve avatar bytes via the core (or a documented shared
  media store) so adapters can serve them to consoles without calling the website.
  **Observed console contract (2026-09-07).** Derived from what the console itself
  requires, as already captured in `nx-baas` — not from any third-party server's
  source. The console is the client whose expectations must be met; any other
  network is merely another implementation of the same contract.
  - The console takes an **https URL, never inline image data**.
  - User object `iconUri` → `<cdn-image host>/avatar/<pid>.jpg?v=<imageUpdatedAt>`.
    The `v` query is the cache key: bump `imageUpdatedAt` on every upload or the
    console keeps the old picture.
  - Friend records carry **two** thumbnail URLs, `thumbnailUrl` → `<cdn>/1/<userID>`
    and `thumbnail2Url` → `<cdn>/2/<userID>`.
  - Unlinked accounts fall back to `/1/default` and `/2/default`.
  - Responses are `Content-Type: image/jpeg` with `Cache-Control: public, max-age=3600`.
  - `/1/` and `/2/` are currently served the *same* bytes and the console accepts it,
    so a single JPEG per account is a known-good starting point.
  **Gap against the website pipeline today.** `internal/media` produces PNG at 256 and
  512 px in website-owned storage under `/media/avatars/<accountID>/<size>`. The console
  path needs JPEG, the `/1/` and `/2/` naming, an `imageUpdatedAt` version, and a default
  image. So CR-2 must carry: the JPEG bytes, `image_updated_at`, and a documented default.
  **Still unmeasured, and to be measured rather than read:** the pixel dimensions the
  console expects for `/1/` versus `/2/`, and the byte ceiling it will accept. Both are
  observable on a linked emulator or console; neither should be answered by reading a
  source-available competitor.
- CR-3 Friend-code projection: the Switch adapter owns `SW-` codes (T2); define how the
  website reads them (core-published field or adapter read contract).
- CR-4 (from W1 implementation) `GET /v1/whoami` on the core HTTP surface. Core login
  returns no identity, so the website re-verifies credentials over Identity gRPC at login
  to learn the account id. Proposal: return `{account_id, email, display_name, status}`
  for the `openpak_session` cookie.
- CR-5 (from W3 implementation) Admin contract: `SearchAccounts(query)`,
  `SetAccountBanned(account_id, banned, reason)` emitting the existing invalidation
  events. Blocks the W3 gate; website pages exist and fail closed until this lands.
- CR-6 (from W3 implementation) Reports read model (queue + resolve) for moderation.
- CR-7 (from W2 implementation) `SetDisplayName(account_id, name)`; profile display
  name is otherwise read-only.

## 10. Milestones and acceptance

- W1 Landing, register, verify, login, logout, recovery on the core. Gate: two fresh
  accounts created end to end on a clean database.
- W2 Profile, friends, invitations view. Gate: request/accept between the two accounts
  visible on both, and the same friendship visible on a linked emulator through the adapter
  (coordinate with the T4 agent for that single check).
- W3 Moderation, site config, status. Gate: ban stops a live session within the documented
  revocation window.
- W4 Mobile API contract published and exercised by tests. Gate: the companion-app agent
  can build against it without questions.

## 11. Risks

- Friend-code ownership (CR-3) can stall W2; decide early.
- The avatar pipeline touches the console path (adapter serves `/1/<id>`); agree the media
  contract before building uploads.
- Over-building admin: ban/unban/reports only until a need appears.

## 12. Evidence references

`account/internal/web/*.go`; `account/proto/openpak/account/v1/*.proto`; Reference route list
(paths only) from `openpak-account/main.go` as the observed feature set.

## 2026-09-08 — what shipped, and the one structural decision

The site is built with **Tailwind** from `web/src/input.css`, mobile-first, using the standalone
binary so the repository gains a stylesheet and not a node toolchain; the built CSS is committed
so the image still builds with nothing but Go. The palette did not move — it stays as custom
properties mapped onto the colour utilities, shared with the app and the console link pages. See
[../docs/design.md](../../docs/design.md).

`/status` is now about **games and players**, not internal components: what is running, who is
online, which console networks exist and how far each one is. Player counts come from the same
live session leases a friends list reads, so the two can never disagree; counts are distinct
accounts, so someone playing two titles is one player.

**There are two status pages on purpose.** This one runs on the machine it describes and cannot
report an outage that takes the site with it; `status.openpak.org` runs on a different host and
answers when this one cannot. Each says so and links the other. A status page that quietly
cannot fail is worse than none.

New surfaces: `GET /api/v1/status` (public, no bearer — the people most likely to check status
are the ones whose sign-in is broken) and `POST /internal/avatar` (service-to-service, absent
from the OpenAPI on purpose, disabled entirely when `WEBSITE_INTERNAL_KEY` is unset).

