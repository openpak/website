# Discord link and the Pakka lookup

To message Pakka on the OpenPak Discord server, a person first links their
Discord account to their OpenPak account (owner decision, 2026-11-01). The
link lets Pakka apply the website's rules: the Meta data-use notice and its
acceptance, no child accounts, per-account rate limits. A Discord identity is
never an OpenPak account without this verified link.

## Linking (Account > Linked platforms)

- `POST /account/link/discord` (signed in, CSRF) sends the person to Discord's
  OAuth2 consent with scope `identify` only, and a state bound to the browser
  (cookie nonce) and the account (HMAC).
- `GET /account/link/discord/callback` checks the state, trades the code for a
  token, reads the Discord user id and username, **revokes the token**, and
  stores `(account_id, discord_id, username)` in `discord_links`. The website
  never keeps a Discord token.
- `discord_id` is unique: one Discord account maps to one OpenPak account. A
  second account trying the same Discord user is refused ("already linked").
- `POST /account/link/discord/revoke` removes the link. Both directions, and a
  refused attempt, are written to the admin audit (`discord.link`,
  `discord.unlink`, `discord.link.refused`). Erasing the account deletes the row.
- The button only exists when `DISCORD_OAUTH_CLIENT_ID` and
  `DISCORD_OAUTH_CLIENT_SECRET` are set.

### Discord developer portal (what the owner must add)

In the application that owns the client id used above, OAuth2 > General:

- Redirect URI, exactly: `https://openpak.org/account/link/discord/callback`
- Scope used: `identify` (nothing else is requested).
- Copy the application's Client ID and Client Secret into the website env as
  `DISCORD_OAUTH_CLIENT_ID` and `DISCORD_OAUTH_CLIENT_SECRET`.

The bot token is not used by the website. The redirect URI is built from the
request host, so it is `https://<the host the person used>/account/link/discord/callback`;
register every host that serves the account page.

## Lookup for Pakka

`GET /internal/discord/{discord_id}` with `Authorization: Bearer <WEBSITE_INTERNAL_KEY>`
(or `X-Internal-Key`). Not in the OpenAPI. Never cached (`Cache-Control: no-store`).

```json
{"linked": false}
```
```json
{"linked": true, "account_id": "...", "display_name": "...",
 "supervised_or_minor": false, "pakka_notice_accepted": null}
```

- `400` for an id that is not a Discord snowflake, `401`/`503` for a wrong or
  missing key, `502` when the link store or the account core does not answer.
  Callers treat anything that is not a 200 with `linked: true` as "not allowed".
- Banned or closing accounts answer `linked: false`.
- `supervised_or_minor` is true unless the account's birthday is a real date
  at least 18 years ago. The website has no console-supervision flag, so an
  unknown or missing birthday counts as a child (fail closed). Accounts that
  predate the birthday field must set it on the account page.
- `pakka_notice_accepted` is `null` until a source is wired in
  (`api.Server.PakkaNotice`): the acceptance is stored by the Pakka chat
  service (`internal/chat/consent.go`, per account and notice version), which
  is on Pakka's `chat` branch and not merged. Callers must treat `null` as
  "not accepted". When chat merges, implement `PakkaNotice` against it (or
  move the store here) and the field fills in.
