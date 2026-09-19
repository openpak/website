# OpenPak supporter PRD (paid extras on a free network)

Status: **proposal**, 2026-09-19. Nothing here is built, and nothing here may launch before the
public launch of the network itself or before the legal gate (SP-0) passes. Repo `website` owns
the supporter surfaces; the entitlement itself lives in the account core (CR-8); the features
sold live in `saves` and in the title servers.

Design rules:

- **The core network stays free.** Sign-in, friends, presence, notifications, chat, online play
  and cloud saves are free for every account, forever. No title server, adapter or client may
  gate any of them on an entitlement. The paid tier sells *tools and extras*, never access.
- **Support, not a purchase of Nintendo's service.** The tier is framed and priced as supporting
  the project, with perks. Pretendo's Patreon-with-beta-perks model is the precedent.
- **OpenPak never holds payment data.** A processor does the money; OpenPak stores an account
  id, a tier, an expiry and an opaque reference, nothing more.
- **Lapsing is graceful.** When support ends, a perk stops; nothing the player made is taken
  away. A ported world, an edited save, a friend list — all stay.
- **Research is not a promise.** Cross-console multiplayer is a spike with a written verdict,
  not a feature on the supporter page.

## 1. Product objective

Give people who want to fund OpenPak a clear way to do it, and give them something worth
having in return: move a save from one console to another, edit a save in the browser, and —
if research proves it possible — play one game across console families. Pay for the servers
without making the network a paid service.

## 2. Relationship to existing plans

- [`website-prd.md`](website-prd.md): adds `/account/support`, a supporter field on
  `/api/v1/me`, and an admin supporter list. Contract requests continue that PRD's numbering
  (CR-1..CR-7 there; CR-8 here).
- [`saves/prds/cloud-saves-prd.md`](../../saves/prds/cloud-saves-prd.md): save porting and
  editors run against the cloud copy. CS-10 (title-owned conversion hook, "interface defined, no
  implementation") is the seam porting fills; CS-05 platform isolation stays intact — a port
  writes a *new* save under the target platform, it never relabels a blob.
- [`prds/platform-parity-prd.md`](../../prds/platform-parity-prd.md): C8 (cloud saves) is the
  capability porting depends on. The Wii U has no console-side save surface today (Wii U PRD
  gap 1); there is no Xbox 360 network at all.
- Account core: changes only through an issue and a reviewed PR (workspace collision rule 3).

## 3. Evidence and current baseline

- `saves` stores per-platform, per-title versions and has a Switch save-data cloud path; the
  `wiiu` adapter contract is proven by test but no console drives it.
- Minecraft: Legacy Console Edition (Xbox 360, PS3, Wii U, Vita, Switch before Bedrock) shares
  one developer (4J) and closely related world formats; community converters to Bedrock and Java
  exist. **Their licences are unverified** and must be read before any code is reused.
- Open-source save editors exist per game (e.g. PKHeX, GPL-3.0). GPL code can run server-side
  inside an AGPL service only where the licences are compatible; each is checked individually.
- Minecraft Bedrock cross-play rides Xbox Live; the legacy editions speak a related 4J protocol
  but authenticate against three different platform networks (Nintendo, Xbox Live, PSN).
- The project is pre-release and private; the account core has no notion of payment or tier.

## 4. Users and journeys

1. Supporter: signs in, opens `/account/support`, links a Patreon account (or pays via
   Checkout, later), sees the tier and expiry; perks appear on the saves page immediately.
2. Porting: a supporter picks a Minecraft world from their Switch (legacy) cloud saves, chooses
   Wii U, and gets a new Wii U save version they can restore through any Wii U save surface.
3. Editing: a supporter opens a save on the saves page, edits supported fields in a form, and
   the edit is written as a new version; the previous one stays restorable.
4. Lapse: support ends; porting and editing buttons disappear with a clear note; every save,
   version and port already made remains.
5. Free player: sees none of this as a wall. At most one unobtrusive link on the saves page.
6. Operator: sees supporters (account, tier, source, expiry), grants a comp tier by hand,
   revokes on chargeback.

## 5. Scope and non-goals

In: supporter tier and billing link (Patreon first); CR-8 entitlements; `/account/support`;
`/api/v1/me` supporter field; admin list and manual grant/revoke; Minecraft legacy save porting;
per-game save editors; a written cross-play feasibility verdict.

Out, deliberately:

- Gating online play, friends, chat, notifications or cloud saves (including the free
  allowance, CS-12) on payment. Bring-your-own storage stays free.
- Lifetime tiers. A one-time payment buys a fixed term, never "forever".
- Selling games, DLC, keys, firmware or anything copyrighted; ROM or key distribution of any
  kind. Editors never unlock paid content the player does not own.
- Save porting or editing that runs on the device; everything runs server-side on the cloud
  copy, so there is one implementation to audit.
- Cross-play as a launch promise; an Xbox 360 or PS3 network as part of this PRD.
- Storing card numbers, bank details or billing addresses.

## 6. Independent implementation rules (apply verbatim)

The rules block of [`website-prd.md`](website-prd.md) §6 applies unchanged, plus:

- Payment is the processor's: OpenPak never sees, logs or stores card data, and receives only
  signed webhooks and OAuth tokens scoped to membership reads.
- Every reused converter or editor has its licence recorded in the repo that runs it (`NOTICE`
  and the PRD's evidence section) before its code is imported. Unknown licence = not used.
- **AGPL §13 stays honoured for paid features.** Porting and editing run as network services,
  so their full source (including any imported GPL code) is offered to every user who reaches
  them — supporters included. Paid does not mean closed.
- Entitlement checks happen only at the edge of a supporter feature. When the core cannot
  answer, that feature fails closed; free play never calls the check, so it cannot degrade.

## 7. Functional requirements

Tier and billing:

- SUP-01 One supporter tier to start, recurring **monthly and discounted yearly**. A one-time
  payment buys a fixed-term pack (e.g. 12 months) and then lapses like any other term.
- SUP-02 Patreon account linking first: OAuth from `/account/support`, membership read, tier
  mapped to entitlement via CR-8; Patreon handles VAT, fraud and refunds. Webhooks (pledge
  create/update/delete) grant, extend and revoke; a nightly reconcile catches missed webhooks.
- SUP-03 Stripe Checkout + webhook as a later second source, behind the same CR-8 calls. Not
  built until Patreon is live and the processor's acceptable-use terms are cleared (SP-0).
- SUP-04 Manual grant/revoke by an admin (comps, contributors, chargebacks), audited.

Entitlements (see CR-8):

- SUP-05 Entitlements live in the account core, keyed by account id: `{name, source,
  source_ref, granted_at, expires_at}`. Names are coarse (`supporter`), never per-feature SKUs.
- SUP-06 Grant, revoke and expiry are emitted on the core event stream so adapters, `saves`
  and title servers can cache them; checks go through `CheckEntitlement`.
- SUP-07 Expiry is honoured with a grace window (placeholder: 7 days) for failed renewals.

Website:

- SUP-08 `/account/support`: tier, source, renews/expires on, link/unlink Patreon, what the
  perks are, and the line "the network is free; this funds it".
- SUP-09 `/api/v1/me` gains `supporter: {active, expires_at}`; OpenAPI updated. No payment
  fields.
- SUP-10 Admin: supporter list (account, tier, source, expires), grant, revoke; every action in
  the admin audit log.
- SUP-11 Free-core check: an automated test asserts no route outside the supporter surfaces
  reads an entitlement.

Save porting (in `saves`, filling CS-10):

- SUP-12 Minecraft Legacy Console Edition world porting between the families OpenPak can get a
  save off: Switch (legacy) ↔ Wii U first; Xbox 360/PS3/Vita when an ingest path exists
  (upload of a save the player extracted themselves).
- SUP-13 A port reads a version, writes a **new** save under the target platform and title,
  and never modifies the source; the source version is pinned automatically.
- SUP-14 A port that loses data (items, structures, biomes absent on the target) says so
  before writing, with a per-world report kept alongside the new version.
- SUP-15 Ingest for platforms with no console-side surface: authenticated upload on the saves
  page, format-sniffed and size-capped; the same path serves Wii U until a console surface
  exists.

Save editors (in `saves`, one module per game):

- SUP-16 Editors run server-side against the cloud copy; the browser gets a form, not a binary.
- SUP-17 One game at a time, chosen by demand; each ships with its licence record, a
  round-trip test (load → save unchanged = byte-identical), and a field allowlist.
- SUP-18 Every edit writes a new version; restore-as-current (CS-09) undoes it.

Cross-play (research only):

- SUP-19 A spike answers, in writing: can two legacy families (e.g. Wii U and Switch legacy)
  join one Minecraft session through OpenPak servers, what the 4J protocol differences are, and
  what authentication each family needs. Verdict: build, defer, or drop.

## 8. Architecture and ownership

- `website`: `internal/support` (Patreon OAuth + webhook receiver, reconcile job), the
  `/account/support` page, the admin list, the `/api/v1/me` field. Stores only the Patreon
  OAuth refresh token (encrypted at rest) and the last webhook id per account.
- `account` (core): entitlement table and the CR-8 contract. The single source of truth.
- `saves`: porting and editor modules behind CS-10, gated by `CheckEntitlement` at the API
  edge only; the storage layer never knows about tiers.
- Title servers: untouched unless the SP-4 verdict is "build".

## 9. Contract requests (raise as `account` issues)

- CR-8 **Entitlements.** New gRPC service `Entitlements`, internal key only:

  ```proto
  service Entitlements {
    rpc GrantEntitlement(GrantRequest) returns (Entitlement);     // idempotent on (source, source_ref)
    rpc RevokeEntitlement(RevokeRequest) returns (Entitlement);   // reason required; audited
    rpc CheckEntitlement(CheckRequest) returns (CheckResponse);   // {active, expires_at}
    rpc ListEntitlements(ListRequest) returns (ListResponse);     // by account, or admin: all active
  }
  message Entitlement {
    string account_id = 1; string name = 2;        // "supporter"
    string source = 3;                             // "patreon" | "stripe" | "manual"
    string source_ref = 4;                         // opaque processor id; never payment data
    int64 granted_at = 5; int64 expires_at = 6; int64 revoked_at = 7;
  }
  ```

  Events on the existing stream: `entitlement.granted`, `entitlement.extended`,
  `entitlement.revoked`, `entitlement.expired`, each `{account_id, name, expires_at}`.
  Account deletion deletes entitlements and emits `revoked`. Bans do **not** revoke (a refund
  is a billing decision, not a moderation one).
- CR-8a (`saves`, not core) A porting/editor API under the existing internal HTTP surface,
  consumed by the website saves page; specified in `saves/prds` when SP-2 starts.

## 10. Data and privacy

- Stored by OpenPak: account id, entitlement rows, Patreon user id and encrypted refresh
  token, webhook ids. Nothing else about the payer.
- Never stored: card data, billing address, legal name, tax data, amount paid. The processor's
  receipt is the receipt.
- Supporter status is private by default: not on public profiles, not on the console.
  A public badge, if ever, is opt-in.
- Unlinking Patreon deletes the token immediately; the entitlement runs to its expiry.
- Privacy policy updated before SP-1 ships, naming the processor(s) as data processors.

## 11. Abuse and refunds (placeholders — owner to decide)

- Refunds: via the processor's own policy; a refund revokes on the webhook. Placeholder:
  no pro-rata refunds from OpenPak itself.
- Chargebacks: revoke immediately; repeated chargebacks block re-linking (manual).
- Shared Patreon across accounts: one Patreon membership maps to one OpenPak account; a second
  link attempt is refused with an explanation.
- Porting/editing abuse (cheated saves in online play): the free-core rule means online
  servers treat edited saves like any other; per-title anti-cheat policy is the title
  server's, not this PRD's. Placeholder: editors expose no fields that affect other players.
- Rate limits: porting and editing jobs per account per day (placeholder: 20).

## 12. Milestones and acceptance

| # | Milestone | Gate |
| --- | --- | --- |
| SP-0 | Legal review and model decision | Written legal opinion covering reverse-engineered console protocols, vendor IP and trademarks, DMCA §1201 exposure of porting/editing, processor acceptable-use (Patreon, Stripe), and the AGPL/GPL mix. Owner confirms recurring vs one-time, price points, and the §14 answers. **No later milestone starts without it.** |
| SP-1 | Supporter tier, CR-8, Patreon linking | CR-8 merged in `account`; a test Patreon pledge grants, a cancel revokes, a missed webhook is fixed by the nightly reconcile; `/api/v1/me` shows the field; SUP-11 test green; privacy policy updated. After public launch only. |
| SP-2 | Minecraft save porting | One real Switch-legacy world ported to Wii U and loaded on hardware or Cemu with a correct loss report; source untouched; converter licences recorded. Wii U ingest by upload works. |
| SP-3 | Save editors, one game at a time | Per game: round-trip test byte-identical, an edit loads on hardware or emulator, licence recorded, AGPL source offer reachable from the editor page. |
| SP-4 | Cross-play research | The SUP-19 written verdict, with packet evidence from two families. Only a "build" verdict becomes a new PRD. |

Definition of done for every milestone: as `website-prd.md` §6 (vet, race tests, runbook,
owner-reproduced journey), in each repo touched.

## 13. Risks

- **Legal (highest).** Porting and editing touch vendor formats and, for some titles, encrypted
  or signed saves; circumvention could fall under DMCA §1201 regardless of intent. Charging
  money changes how a rights holder and a court read "non-commercial preservation". SP-0 is a
  hard gate, and any feature the opinion flags is dropped, not reworded.
- **Processor.** Patreon or Stripe may classify console-network or save-modding revenue as
  prohibited; an account closure freezes funds. Mitigation: the "support the project" framing,
  Patreon first, no dependence on one processor for server costs.
- **Perception.** A paid tier on a free-network project invites "pay-to-play" accusations.
  Mitigation: the free-core rule, SUP-11, and saying so on the page.
- **Support load.** Paying users expect answers; a failed port is a ticket. Mitigation: loss
  reports, never-destructive ports, one tier, a documented support channel and response
  expectation (none guaranteed) on `/account/support`.
- **AGPL obligations.** Paid network features are still AGPL services; source must stay
  offered to their users, and imported GPL code must be compatible. A proprietary converter
  cannot be used, however good.
- **Ingest dependency.** Porting is only as good as the ability to get a save off a console;
  Wii U and Xbox 360 have none today, and upload-your-own shifts effort to the player.
- **Scope creep.** "One more editor" is endless; SUP-17 ties each to demand and a test.

## 14. Open questions for the owner

1. Recurring (recommended: monthly + discounted yearly) or one-time fixed-term packs, or both?
2. Price points and currency; is there more than one tier ever, or one tier forever?
3. Patreon only at launch, or Patreon plus Ko-fi/GitHub Sponsors as further linkable sources?
4. Which legal counsel, and in which jurisdiction does OpenPak operate for this purpose?
5. Which game gets the first editor — and is porting enough on its own to launch the tier?
6. Beta access (Pretendo-style early access to new title servers) as a perk: yes or no? It
   is cheap to give but sits close to "paying for access".
7. Are existing contributors and testers comped, and for how long?
8. What happens to supporter revenue beyond server costs — published, or kept private?

## 15. Evidence references

`saves/prds/cloud-saves-prd.md` (CS-05, CS-09, CS-10, CS-12); `prds/platform-wiiu-prd.md`
(gap 1); `prds/platform-parity-prd.md` (C8); `website-prd.md` (§6, §9); Pretendo Network's
public Patreon tiers (observed perks only). Converter and editor licences: to be recorded at
SP-2/SP-3.
