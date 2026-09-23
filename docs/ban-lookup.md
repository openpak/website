# Ban lookup — the contract every network codes against

A ban made in `/admin/users` must lock the account out of **every** OpenPak
network: the website, the app, the Switch (nx-baas), the Wii U / 3DS
(nn-account and the game servers behind it), chat, and anything added later.
This file is the one contract for asking "is this account banned?". It is
owned by the website repository; the account core owns the data.

## Where a ban lives

- The ban itself is the account core's: `Moderation.SetAccountBanned` flips the
  account's status to `banned`, records reason and actor in the core's own
  moderation log, revokes every web session, and emits `account_banned`
  (`account_unbanned` on the way back) on the core's event stream.
- The website only calls that RPC (as the admin's account id) and keeps a copy
  of who did it and why in its own `account_bans` table and `admin_audit`, so
  the admin and the HTTP lookup below can show reason, actor and time.

There are two ways to ask. Use the first if you already talk to the core.

## 1. Primary: the core's gRPC `Identity.GetAccount` (no new RPC)

This is what nx-baas and nn-account already call; it is one indexed row read
in the core, with no session side effects.

```
service openpak.account.v1.Identity
  rpc GetAccount(GetAccountRequest{account_id}) returns (GetAccountResponse)
```

- Address: the core's gRPC listener (`ACCOUNT_GRPC_ADDR`, `:20001` in the port
  plan). Credentials: metadata `x-internal-key: <ACCOUNT_INTERNAL_KEY>` (or
  `authorization: Bearer <key>`), the same key every internal caller holds.
- Answer: `GetAccountResponse.status` (`AccountStatus`).
  - `ACCOUNT_STATUS_ACTIVE` → allowed.
  - `ACCOUNT_STATUS_BANNED` → **locked out**.
  - `ACCOUNT_STATUS_DELETION_PENDING`, `ACCOUNT_STATUS_DELETED` → locked out
    too (same treatment as banned; the account is going away).
  - gRPC `NotFound` → no such account: locked out.
- The password and credential checks already carry the same status, so a
  sign-in path does not need a second call:
  `VerifyPassword` / `VerifyPasswordByID` answer `status = BANNED` with no
  error for a banned account's correct password, and
  `VerifyAdapterCredential` answers `valid` plus `status`. **Check `status`,
  not only the absence of an error.**
- The reason and actor are deliberately not on this RPC. A service that wants
  to show the reason to the player uses the HTTP lookup below.

## 2. Secondary: the website's internal HTTP lookup

For services that speak HTTP and already hold `WEBSITE_INTERNAL_KEY` (the
adapters use it for `/internal/avatar` and `/internal/titles/name`), or that
want the reason to show the player.

```
GET /internal/bans/{account_id}
GET /internal/bans?namespace=<ns>&subject=<subject>
Authorization: Bearer <WEBSITE_INTERNAL_KEY>
```

- Host: the website's internal address (`WEBSITE_INTERNAL_URL` on the caller;
  `http://website:20010` on the box). Not published through Traefik, never in
  the public OpenAPI.
- The second form resolves an adapter link (`namespace` `switch`, `wiiu`, `3ds`,
  `wfc`, … and the subject the adapter stored with `Links.ReserveLink`) to its
  account first, for a game server that only knows the console-side id.
- `200`:

  ```json
  {
    "account_id": "0192…",
    "banned": true,
    "status": "banned",
    "reason": "cheating in Splatoon ranked",
    "actor": "0191…",
    "since": "2026-09-23T10:15:00Z"
  }
  ```

  - `banned` is the only field a gate needs: true for `banned`,
    `deletion-pending` and `deleted`.
  - `status` is `active`, `banned`, `deletion-pending` or `deleted`, straight
    from `Identity.GetAccount`.
  - `reason`, `actor` (the admin's account id) and `since` (RFC 3339) are set
    only when `status` is `banned` and the ban was made through the website;
    otherwise empty strings. Show `reason` to the player if you like; never
    show `actor`.
- `404 {"error":"no account for that subject"}` / `{"error":"account not found"}`:
  treat as locked out.
- `401` wrong key, `503` the website has no `WEBSITE_INTERNAL_KEY` set, `502`
  the core did not answer.

## Rules for every consumer

1. **Check at every sign-in and every token/ticket issue.** That is the
   minimum. nx-baas: LoginPID / token mint / NSO checks. nn-account: NNAS
   token, NASC login, the v2 gRPC. Game servers: the NEX ticket validation
   they already do through nn-account.
2. **Kill live sessions on the event.** Consume the core's event stream
   (`Events.SubscribeAccountEvents` or `PollEvents`) and on `account_banned`
   drop every session, token and game connection for `account_id`. nx-baas
   and nn-account already listen for it; chat and the game servers should too
   (or re-check on their next heartbeat).
3. **Cache at most 60 seconds**, per account id, and only the `active`
   answer. Never cache "banned" longer than a positive answer would be cached:
   an unban should work within a minute everywhere.
4. **Failure mode.** On sign-in, if the lookup cannot be answered (core down,
   timeout), refuse the sign-in: you could not have checked the password
   either. For a check in the middle of a session, keep the session and retry;
   the event stream is what ends it.
5. Unbanning restores access immediately (status `active`, event
   `account_unbanned`); nothing needs to be re-linked.

## What the website and app do themselves

- Website sign-in and `POST /api/v1/token` refuse a banned account
  (`auth.login.banned` / `403 {"code":"account_suspended"}`).
- Every signed-in page and every bearer call re-validates the core session;
  the ban revoked those sessions, so the next request is signed out and the
  bearer is revoked on sight.
- The website's own new surfaces (help tickets, crash reports with a bearer)
  go through the same checks: a banned account cannot open tickets, and a
  crash report sent with a dead bearer is stored anonymously.
