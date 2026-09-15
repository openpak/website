# OpenPak network profile PRD (one source of truth for what a client redirects, and where)

> **V2 planning, 2026-09-14:** The [Universal TLS forwarding PRD suite](../../emulators/prds/universal-tls-forwarding/README.md) defines the proposed migration to certified build-time distribution, signed bundles and console setup integration. This document remains the historical v1 implementation/transition contract. Its public per-launch fetch requirements do not govern v2; endpoint withdrawal happens only at the new rollout gate.

Status: v1, 2026-09-12. Server side. **NP-0 through NP-4 implemented 2026-09-12** — the
generator, drift check, ETag/304 and `ca.pem` live in `website/internal/netprofile`, and
`nn-sssl-dns` reads the same map. Deployment needs the Traefik dynamic config mounted
read-only into the website container (`NETPROFILE_TRAEFIK_FILE`). Its counterpart is
[`emulator-network-profile-prd.md`](../../emulators/prds/emulator-network-profile-prd.md), which is what the
emulator agents implement.

Design rules:

- **Generated, never authored.** The profile is derived from the routing this server already
  runs. A hand-maintained list would be a seventh copy of the thing this PRD exists to delete.
- **The server may narrow, never widen.** A client compiles in the domain families it will ever
  redirect; the profile chooses within them. A profile that names a domain outside those
  families is a client-side redirect of somebody else's traffic, and no server should be able
  to ask for one.
- **Stale beats broken.** A client that cannot reach this endpoint keeps its last good profile,
  and failing that the one compiled into it. Nothing about fetching a profile may stop a game
  from launching.
- **One conditional GET per launch.** ETag in, 304 out. The BCAT work already proved the console
  itself does exactly this; there is no reason for the emulator path to be heavier.

## 1. Problem

Six clients hardcode which hostnames belong to OpenPak, in five languages, and they disagree:

| Client | Shape | Names |
| --- | --- | --- |
| Ryujinx | wildcard list written into the guest's `atmosphere/hosts/default.txt` | `*.nintendo.net`, `*.nintendo.com`, `*.nintendo.co.jp`, `*.nintendowifi.net`, `*.nintendo-europe.com` |
| Dolphin | C++ suffix array, **and the server IP as a literal default in `MainSettings.cpp`** | `.nintendowifi.net`, `.gamespy.com`, `.nintendo.net`, `.openpak.org` |
| melonDS | `LAN.OpenPakServer` + a Net_Slirp DNS override | same idea, separately maintained |
| Cemu | ten explicit service URLs in an `OpenPakURLs` struct | `account.openpak.org`, `ecs.wup.shop.openpak.org`, … |
| Azahar | host map in `HTTP_C` | its own list |
| `nn-sssl-dns` (real consoles) | suffix array in TypeScript | `.nintendo.net`, `.nintendowifi.net`, `.nintendo.com`, `.openpak.org`, `.gamespy.com` |

Nobody is wrong; they are just different. Dolphin redirects `.gamespy.com` and Ryujinx does not.
Ryujinx redirects `.nintendo.co.jp` and `.nintendo-europe.com` and Dolphin does not. Adding a
title that lives on a new hostname means six pull requests across six release cycles, and a
player whose emulator is one version behind silently talks to nobody.

That is the failure being debugged on 2026-09-12: a game reached no OpenPak host and the server
logged nothing at all, because a redirect list decided the question before a packet was sent.

## 2. Product objective

A client asks one question at launch — *"what is OpenPak, and what should I send you?"* — and
gets an answer generated from the routing that is actually live. Adding a title, a service or a
whole console family becomes a server deploy, not six client releases.

## 3. Where it lives

`GET https://openpak.org/api/v1/network/profile?platform=<platform>`

Served by `website` under `/api/v1`, like every other public surface. This is not a preference:
the Cloudflare tunnel is token-based with its routes in the dashboard and no API token on the
box, so a new public hostname cannot be created from here. New public HTTP goes through the
website proxy or it is not public.

`platform` ∈ `switch | wiiu | 3ds | wii | ds`. Omitted means every platform, which is what a
diagnostic or the website itself wants and no emulator does.

## 4. The profile

```jsonc
{
  "version": 17,                   // monotonic; bumps when anything below changes
  "generated_at": 1789233000,
  "platform": "switch",

  "server": {
    "address": "145.241.199.19",   // what a redirected name resolves to
    "https_port": 443,             // consoles cannot be told a port; this is for clients that can
    "http_port": 80
  },

  "ca": {
    "url": "https://openpak.org/api/v1/network/ca.pem",
    "sha256": "…",                 // the client checks this before trusting it
    "not_after": 1900000000
  },

  "redirect": {
    "suffixes": [".nintendo.net", ".nintendowifi.net", ".nintendo.com",
                 ".nintendo.co.jp", ".nintendo-europe.com", ".gamespy.com", ".openpak.org"],
    "exact":    ["gamesync.npln.nintendo.net"],
    "never":    ["conntest.nintendowifi.net"]   // names that must reach the real internet
  },

  "services": [                     // for clients that need a URL, not a resolver (Cemu)
    {"id": "act",  "url": "https://account.openpak.org"},
    {"id": "ecs",  "url": "https://ecs.wup.shop.openpak.org/ecs/services/ECommerceSOAP"}
  ],

  "recheck_after": 21600            // a hint, not a contract
}
```

Notes that matter:

- **`never` exists because a redirect that is too broad is its own outage.** A connection test
  pointed at OpenPak measures OpenPak, not the player's internet.
- **`services` is only for clients that address a URL rather than resolve a name.** Cemu is the
  one today. Emulators that redirect DNS ignore it entirely.
- **`exact` catches names outside the suffix families**, such as `gamesync.npln.nintendo.net`
  when a suffix list is narrowed.

## 5. Generation

The profile is built from three live sources, at request time or on a short cache:

1. **Traefik's dynamic configuration** — every `HostSNI(...)` and `Host(...)` rule (20 TCP and
   ~36 HTTP today, including the `openpak.org` aliases). This is the authoritative list of names
   this network answers on.
2. **`nn-sssl-dns`'s suffix list** — the families a real console is allowed to redirect, which
   is the same question an emulator is asking.
3. **A per-platform map** naming which of those belong to which console family, since a Wii
   must not be told about `*.npln.srv.nintendo.net`.

Only (3) is new configuration, and it is a small static file in the service, reviewed like code.
If a name appears in Traefik and in no platform map, the profile omits it and the build logs it
loudly — an unrouteable name is a bug worth seeing, not worth shipping.

## 6. Caching and the update signal

- **ETag over the serialised profile.** A client sends `If-None-Match`; an unchanged profile is
  a 304 and costs nothing.
- **`version`** is for humans and logs; the ETag is the machine's answer.
- **`recheck_after`** is a hint the client may honour or ignore.
- **No push.** A client fetches on launch, and when a person asks it to. A signalling channel
  for a list that changes a few times a month is infrastructure with no job.

## 7. Security

An endpoint that tells a client *"send these hostnames to this address"* is a redirection
primitive, and it must be treated as one.

- **Served over TLS with a publicly trusted certificate**, from `openpak.org`. Never over plain
  HTTP, and never from an address a profile itself supplied.
- **The client's compiled-in allowlist is the ceiling.** The server may name a subset of the
  domain families a client already ships with; anything else the client drops and logs. This is
  the load-bearing mitigation: a compromised or spoofed profile still cannot redirect a bank.
- **The CA is fetched separately and pinned by digest**, so a profile cannot swap the trust
  anchor without the hash changing.
- **No credentials.** The profile is public, identical for everyone, and cacheable. It must
  never carry a token, an account, or anything that varies per user.
- Serving it is `read-only`: this endpoint has no write side.

## 8. Milestones

| # | Milestone | Definition of done | Status |
| --- | --- | --- | --- |
| NP-0 | Platform map + generator, against the live Traefik config | `GET …/profile?platform=switch` returns the names this server actually routes | **Done 2026-09-12** — `website/internal/netprofile` parses the mounted dynamic.yml (the Traefik join is in code, not left to the operator), `platforms.yml` is the only authored input, and the emitted suffixes narrow each client's compiled-in families to what is actually routed |
| NP-1 | ETag + 304 | A second launch costs one conditional request | **Done 2026-09-12** — strong ETag over the profile body; `generated_at` is excluded from the identity (the clock must not turn 304s into 200s) and `version` bumps only when meaning changes |
| NP-2 | `ca.pem` endpoint with a published digest | A client can bootstrap trust from nothing but `openpak.org` | **Done 2026-09-12** — `GET /api/v1/network/ca.pem` serves the CA with its sha256 in the profile's `ca.sha256` and in `X-CA-Sha256`; the CA comes from the adapter (the same source the console path already trusts), cached, last-good on failure |
| NP-3 | Drift check in CI | A Traefik name with no platform map fails the build | **Done 2026-09-12** — the drift test runs the generator against `testdata/traefik.yml` (the committed snapshot of the box's dynamic.yml); a routing change ships its snapshot refresh in the same PR, and unmapped names are also logged loudly at request time |
| NP-4 | `nn-sssl-dns` reads the same map | The real-console resolver and the emulators stop being two sources | **Done 2026-09-12** — the resolver fetches the union profile on boot and per `recheck_after`, answers exactly the profile's families and exact names, keeps its compiled-in list as the fallback, and deliberately ignores `redirect.never` (a console whose only setting is a DNS server has no other resolver) |

NP-4 is the one that makes this a single source of truth rather than a second one. Until it
lands, the DNS server and the profile can still disagree.

## 9. Out of scope

Per-user or per-region profiles (federation is deferred). Discovery of *other* OpenPak
deployments. Anything that requires a client to trust a profile more than its own allowlist.
