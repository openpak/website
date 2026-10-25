# Notes: website

openpak.org: the public site and the account portal (PRD `prds/website-prd.md`).
Server-rendered Go service — registration, verification, sign-in, recovery,
profile with friend code and console links, friends, invitations view,
moderation, site configuration, status, and the versioned mobile API under
`/api/v1`. No JavaScript is required for any journey (WEB-10).

## Layout

```
cmd/website/            the openpak.org server (site, portal, /admin, /api/v1)
cmd/openpak-registry/   v2 network registry service, /api/v2/network/* (docs/registry.md)
cmd/openpak-opsd/       per-host agent behind /admin/services (docs/opsd.md)
cmd/openpak-policy/     operator console for v2 policy: publish, rollback, withdraw, inspect
cmd/openpak-network/    CI-side fetch tool: authenticate, fetch, verify a v2 bundle
internal/accounts/      account actions shared by the pages and the API
internal/adapter/       client for the Switch (and Wii U/3DS) adapter internal API
internal/admin/         role-gated operator/moderation pages under /admin
internal/attachments/   ticket and crash-report files on disk (WEBSITE_ATTACHMENTS_DIR)
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

## Styling

Tailwind's **standalone binary** is used on purpose: no node, no package.json,
no lockfile, so the only thing this repository gains from Tailwind is a
stylesheet. The built CSS is committed, which is why the container image still
builds with nothing but Go.

The palette stays as plain custom properties that flip with the reader's system
setting, mapped onto Tailwind's colour utilities with `@theme inline`. So
`bg-paper` and `text-ink` follow light and dark on their own, no template
carries a `dark:` variant, and those values remain the single source shared
with the console tool and the app.

## Status page

The playing lists and the homepage's "In a game" count include only titles in
the website's database. Unknown games and apps, such as DBI, still count toward
the online total and their console network's online count.

## Sessions model (WEB-02)

The website cookie (`opak_session`, HttpOnly/Secure/SameSite=Lax) wraps the
core web session id. A website-side index table maps that id to the account
id the core names for it at login (`GET /v1/whoami`, CR-4); the password is
checked once, by the core's `POST /v1/login`. When the core session dies
(revoked device, ban, password reset), the next request clears the cookie
and the index row; API bearers bound to it are revoked on sight.

## Friend codes (CR-3)

The Switch adapter mints friend codes and publishes them on the account's
active `switch` link (`Links.SetLinkPublicCode`); the website shows that
`public_code` and resolves a typed code with `Links.GetLinkByPublicCode`.
Adapters do not publish codes yet, so while `public_code` is empty the
website asks the adapter itself (`ADAPTER_INTERNAL_URL`), and a pasted link
subject still resolves through `Links.GetLinkBySubject`. The order lives in
`internal/accounts` and is the same on the site and the API.

## Contract requests (raise as `account` issues)

Carried from the PRD, plus what this implementation discovered:

- ~~**CR-1** `Invitations.ListForAccount(account_id) → [pending invitations]`~~ — **delivered
  2026-09-08** as `Invitations.ListInvitations`; also `RejectInvitation`, `Sessions.GetPresence`
  and `Sessions.GetPlayerCounts`. Original request kept for the reasoning:
  read-only, no delivery side effects. Until then `/account/invitations`
  renders a pending notice; the website must not call `PollInvitations`
  (adapter-scoped, delivery side effects).
CR-2 to CR-6 were delivered by account v0.9.0 and are wired in (stubs from
account revision 7b87201):

- ~~**CR-2** Account media~~ — **delivered** as the core's `Media` service;
  the website publishes both avatar PNGs with `PutAvatar` after every store.
- ~~**CR-3** Friend-code projection~~ — **delivered** as the link's
  `public_code` (`SetLinkPublicCode`, `GetLinkByPublicCode`,
  `ListAccountLinks`). The adapter publishes, the website reads; until
  adapters publish, the adapter-answer fallback above stays.
- ~~**CR-4** `GET /v1/whoami`~~ — **delivered**; sign-in on the site and
  `POST /api/v1/token` use it instead of re-checking the password over gRPC.
- ~~**CR-5** Admin contract~~ — **delivered** as `Moderation.SearchAccounts`
  and `SetAccountBanned` (reason and actor, audited in the core);
  `/admin/users`.
- ~~**CR-6** Reports read model~~ — **delivered** as `Moderation.ListReports`
  and `ResolveReport` (plus `FileReport`, which nothing on the website calls
  yet); `/admin/reports`.
- ~~**CR-7** `SetDisplayName(account_id, name)`~~ — **delivered**: the core
  accepts `display_name` on `POST /v1/profile`, and both the profile form and
  `POST /api/v1/me/profile` send it.
