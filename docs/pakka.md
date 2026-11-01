# Pakka chat

Pakka is OpenPak's assistant (platforms/core/pakka). The website and the app talk to it through
this site; nothing talks to Pakka's chat API from a browser or phone directly.

## Who may use it

Only active accounts whose recorded date of birth makes them 18 or over (`pages.PakkaEligible`).
Child, supervised, under-18, no-birthday and unreadable-status accounts are refused, with no
parental override, because the model provider's terms require end users to be 18. The check is
made here and again by Pakka (it asks `/internal/pakka/tool/eligibility` on every gated call and
refuses when the answer is no or cannot be obtained). Such accounts see no entry point; opening
`/account/pakka` shows a short "not available on this account" page.

## Routes

- Page: `GET /account/pakka`. Panel calls: `/account/pakka/api/...` (cookie session; changes need the
  page's CSRF token in `X-CSRF`).
- App: `/api/v1/pakka/...` (bearer), documented in openapi.yaml.
- Both resolve the person, then `pakkachat.Bridge.Forward` calls Pakka with `Authorization: Bearer
  PAKKA_CHAT_KEY`, `X-Account-Id` (what this site resolved; a client-supplied one is ignored) and
  `X-Pakka-Grant`.
- Pakka reads about the person with `GET /internal/pakka/tool/{status,consoles,friends,rewards,
  eligibility}`. The service key must match, and the grant (random, valid for the one request, at most
  three minutes) decides whose data is read; no tool takes an account argument. Answers are minimal:
  platform names, a friends count, Rewards balance and missions, public service status.

## Configuration

`PAKKA_URL` (http://openpak-pakka:24800) and `PAKKA_CHAT_KEY` (same value as Pakka's). Either empty
removes every Pakka route and entry point.
