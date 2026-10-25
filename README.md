# Website

OpenPak's Website.

| | |
| --- | --- |
| Network | Switch, Wii U, 3DS, Wii, DS |
| Role | Public site, account portal and the `/api/v1` mobile API at openpak.org |
| Depends on | [`account`](https://github.com/openpak/account), PostgreSQL |
| Image | `ghcr.io/openpak/website` |
| Licence | AGPL-3.0-only |

## What it serves

| Endpoint | What |
| --- | --- |
| Site and account pages | Registration, sign-in, recovery, profile with friend code and console links, friends, invitations, moderation |
| `/api/v1` | Versioned mobile API, contract in [`openapi/openapi.yaml`](openapi/openapi.yaml) |
| `/admin` | Operator and moderation pages, for accounts in `WEBSITE_ADMIN_ACCOUNT_IDS` |
| `/status`, `/healthz` | Game servers, console networks and players; liveness |

## Run

```sh
cp example.env .env   # then export the values
go run ./cmd/website
```

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)
- [Ban lookup](docs/ban-lookup.md)
- [Glossary](docs/glossary.md)
- [Handles](docs/handles.md)
- [Host agent (openpak-opsd)](docs/opsd.md)
- [Registry](docs/registry.md)
- [Docs index](docs/README.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
