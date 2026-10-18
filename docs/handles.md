# Permanent handles

Signup requires `handle`, independent of optional `display_name` (which defaults
to the handle). The core validates and atomically claims it. The form checks
`GET /api/v1/handle/check?h=...`; that endpoint shares the signup IP limiter,
returns no account identifiers and fails closed if the core is unavailable.
Handle errors remain distinct: handle_invalid, handle_reserved, handle_profane
and handle_taken. Email registration still uses the masked response.

The account banner and /me expose `handle` and a friend_codes row with kind
`handle`. Friend lookup resolves it through core Identity.ResolveHandle,
case-insensitively. Switch and 3DS codes remain separate, with existing code
formats accepted. Display names remain editable, handles do not. The retired
network gamertag-edit POST answers Gone and invokes no adapter.

OP codes are fully retired, including minting and lookup; migration
0022_retire_openpak_codes drops their table. There is no old-code alias path.
Deploy only after the account core, adapters and owner-reviewed legacy migration
are ready. See account-core docs/handles.md for the authoritative policy.
