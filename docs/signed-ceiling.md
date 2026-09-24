# Signed redirect ceiling — the shared contract

Decided by tobagin, 2026-09-24. Goal: adding a redirect family (`.demonware.net`, …) is a
server change only. No emulator, console tool or DNS server needs a new release for it, and
it stays secure: a compromised production box cannot widen what clients redirect.

This file is the contract every implementation follows. Copy it into your repo's docs if
you implement it; do not change it without changing every implementation.

## The model

- **Ceiling** = per platform, the domain families a client is allowed to redirect.
  Until now it was compiled into every client (`AllowedFamilies`, `families.go`,
  `network_profile_fetch.cpp`, …); now it is a **signed file** fetched at boot.
- It is signed with an **Ed25519 key that never touches the server** (owner-local at
  `~/openpak-ceiling-private/ceiling-signing.pem`). Clients pin the public key.
  The box only *serves* the signed bytes; it cannot produce new ones.
- The **profile** (`GET /api/v1/network/profile?platform=…`, unchanged, unsigned) still says
  what to redirect *now*. Clients apply every profile name that lies inside the verified
  ceiling and **drop, individually and logged, any name outside it** — never the whole
  profile (the 2026-09-24 failure: one new family made Ryujinx discard every redirect).
- The compiled-in list stays only as the **first-boot fallback**, used when no verified
  ceiling has ever been cached. It is never updated again.

## Pinned public key

```
algorithm  Ed25519
public     0fd2a2660868e53d20fc811e3f15710cb1ad157ba7da8019f7a59e8ad604d87c   (hex, 32 bytes raw)
           D9KiZgho5T0g/IEePxVxDLGtFXun2oAZ96WeitYE2Hw=                        (base64)
keyid      36e8bcdd93c2c1a1d7e5d87bbba05a7a4f97882131cf6878f1c053a25a377fe4   (sha256 of the raw key)
```

Clients hold a **list** of pinned keys (one today) so a future key can be added before the
old one is retired.

## Endpoint

`GET https://openpak.org/api/v1/network/ceiling` → `200 application/json`, public, no auth,
`ETag` + `Cache-Control: public, max-age=300`. Served from a file the box holds; the server
never modifies it. Bootstrap rule: fetch it from the pinned `openpak.org` origin with normal
public TLS trust — never through a guest DNS override, the OpenPak CA or a URL taken from the
profile.

### Envelope

```json
{
  "payload": "<base64 of the exact payload bytes>",
  "signatures": [ { "keyid": "<hex>", "sig": "<base64 Ed25519 signature over the payload bytes>" } ]
}
```

Verify the signature over the **decoded payload bytes** (never a re-serialisation), then
parse those bytes. Accept if at least one signature is valid under a pinned key with that
keyid.

### Payload

```json
{
  "type": "openpak-ceiling",
  "version": 3,
  "issued": "2026-09-24T10:30:00Z",
  "platforms": {
    "switch": [".nintendo.net", ".nintendo.com", ".demonware.net"],
    "wiiu":   [".nintendo.net", ".nintendowifi.net", ".openpak.org"]
  }
}
```

- `type` must equal `openpak-ceiling`. `version` is a positive integer that grows with every
  signing. Unknown fields are ignored.
- Platform keys are the profile's platform names: `switch`, `wiiu`, `3ds`, `wii`, `ds`.
- Each family starts with `.`, is lower case, has **at least two labels** (`.ea.com` yes,
  `.com` / `.net` / `.co.uk`-style bare public suffixes no — clients reject a family with fewer
  than two labels, and reject the whole ceiling file if any entry is malformed). A family
  covers its apex and everything below it (`.ea.com` covers `ea.com` and `x.ea.com`).

## Client rules

1. **Boot:** load the cached ceiling (re-verify its signature on load), fetch a fresh one
   (2 s timeout, no retry loop, never blocks emulation), verify, and accept it only if
   `version` ≥ the highest version ever accepted (persist that number: rollback
   protection). Then fetch the profile as today and filter it through the ceiling for this
   client's own platform.
2. **Failure:** bad signature, unknown keyid, malformed, lower version, timeout → keep the
   cached verified ceiling; with none, the compiled fallback. Log the reason once.
3. **Cache:** store the envelope bytes exactly as received (not a re-serialisation), write
   atomically, `0600` where the platform has permissions.
4. **Change notice (only the affected platform):** at boot, record a digest of the
   *effective* redirect set for this client's platform — the sorted list of applied
   `family:`, `exact:`, `override:name=ip` and `address:ip` entries after filtering. While
   running, re-check ceiling + profile every **6 hours** (±10 % jitter) and after an OpenPak
   sign-in. If the newly computed digest differs from the one in use, show **one** notice
   per new digest: *"OpenPak updated this system's network redirects. Restart the game (or
   the emulator) to use them."* Wording per client may say game or emulator depending on when
   that client applies redirects. A change to another platform's list never changes this
   platform's digest, so it never notifies.
5. **Consoles:** the setup tool shows the same notice when opened and the effective set it
   would install differs from the one installed; consoles cannot be told in the background.
   Consoles using OpenPak DNS (`nn-sssl-dns`) need nothing: the resolver re-reads on its own.

## Server / owner side

- `openpak-policy ceiling show|add|remove|sign` (website repo) runs **on the owner's machine**
  with the private key, bumps `version`, writes the envelope, and prints the `scp` line that
  puts it on the box at `~/openpak/deploy/netprofile-ceiling.json` (mounted into the website).
- The profile generator and `nn-sssl-dns` validate platform families against the **signed
  ceiling** instead of a compiled `Universe`, so the server also needs no release for a new
  family.
- Adding a family, end to end: `openpak-policy ceiling add switch .example.net` → scp →
  add the family to `netprofile-platforms.yml` on the box → clients pick it up at next boot,
  running ones show the notice.
