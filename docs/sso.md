# Sign-in for openpak.org subdomains (SSO)

The website's session cookie is host-only, so `splat.openpak.org`, `kart.openpak.org`,
`maker.openpak.org`, `verse.openpak.org` and `rewards.openpak.org` cannot see it. They sign people in through
openpak.org instead: an authorization-code flow with a one-minute single-use code and a
server-to-server exchange. The subsite then keeps its own session; nothing of openpak.org's
session leaves openpak.org.

## Flow

1. The subsite creates a random `state` (and optionally a PKCE `code_verifier`), stores them in
   its own short-lived cookie, and sends the browser to

       GET https://openpak.org/account/sso/authorize
           ?client=<id>&redirect_uri=<url>&state=<random, 8-256 chars>
           [&code_challenge=<base64url(sha256(verifier))>&code_challenge_method=S256]

2. openpak.org checks `client` and `redirect_uri` (see below). A bad pair is a 400 page, never a
   redirect. Signed out, the person goes through the normal login and comes back to the same URL.
   Signed in, the browser is redirected (303) to `redirect_uri?code=<code>&state=<state>`.
3. The subsite checks that `state` equals what it stored, then from its server calls

       POST /internal/sso/token        (form fields, or JSON is not accepted)
       Authorization: Basic base64(<client id>:<client secret>)     (or client_id / client_secret fields)
       code=<code>&redirect_uri=<the same url>[&code_verifier=<verifier>]

   `200 {"account_id": "...", "display_name": "...", "wiiu_pid": 1800000123 | null}`.
   `wiiu_pid` is the account's Wii U / 3DS PID (its PNID, created if needed) when nn-account
   answers; `null` otherwise. Errors are `{"error": "invalid_client" | "invalid_grant" |
   "invalid_request" | "rate_limited" | "temporarily_unavailable"}` (401, 400, 400, 429, 502).
4. The subsite starts its own session for `account_id`.

A code is valid for 60 seconds, works once, is bound to its client and its exact `redirect_uri`,
and is spent by any failed attempt. With a `code_challenge` the exchange must also present the
matching `code_verifier`. Codes live in the website's memory (a restart costs one more click).

Server to server, call the website on the private network (`http://openpak-website:20010`);
`/internal/sso/token` is not a browser route and needs the client secret.

## redirect_uri

Must be `https`, on a host registered for that client exactly (no wildcards, no other port),
without credentials or a fragment. Default per client: its own host.

## Sign out

A subsite clears its own session. To clear openpak.org's too, send the browser to

    GET https://openpak.org/account/sso/logout?redirect_uri=<url on any registered origin>

which shows a confirmation (a link on another site cannot sign anybody out by itself); confirming
is a CSRF-protected POST that ends the openpak.org session and redirects to `redirect_uri`.
Signed out already, it redirects straight back.

## Registering a client

Environment of the website (the shared `.env`); a client with no secret is not registered:

| Client | `client` (id) | Secret variable | Allowed origins (default) |
| --- | --- | --- | --- |
| OpenSplat | `opensplat` | `SSO_CLIENT_OPENSPLAT_SECRET` | `https://splat.openpak.org` |
| OpenKart | `openkart` | `SSO_CLIENT_OPENKART_SECRET` | `https://kart.openpak.org` |
| OpenMaker | `openmaker` | `SSO_CLIENT_OPENMAKER_SECRET` | `https://maker.openpak.org` |
| OpenVerse | `openverse` | `SSO_CLIENT_OPENVERSE_SECRET` | `https://verse.openpak.org` |
| Rewards | `rewards` | `SSO_CLIENT_REWARDS_SECRET` | `https://rewards.openpak.org` |

`SSO_CLIENT_<NAME>_REDIRECTS` (comma-separated https origins) replaces the default, for example
a staging host. `NN_ACCOUNT_INTERNAL_URL` and `NN_ACCOUNT_INTERNAL_KEY` (already used by the app
API) give `wiiu_pid`. Add a new client by adding a row to `builtin` in `internal/sso/sso.go`.
