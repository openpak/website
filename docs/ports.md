# Ports — website (trimmed)

> Trimmed copy for this repository; the canonical document lives at
> `Openpak/ports.md` and governs. Last synchronised 2026-09-15.

One block per concern, nothing below 20000, nothing at or above 27000 (Photon-Reference and
Steam live there). Every service reads its listeners from `<SVC>_HTTP_ADDR`, `<SVC>_GRPC_ADDR`,
`<SVC>_METRICS_ADDR`; the values below are the defaults and the local-run convention. Each
service owns a block of ten: +0 HTTP, +1 gRPC, +2 metrics/pprof, +3..+9 spare.

## Blocks built from this repository

| Block | Service | HTTP | gRPC | metrics |
| --- | --- | --- | --- | --- |
| 20010 | `website` (site, portal, mobile `/api/v1`) | 20010 | — | 20012 |
| 20140 | `openpak-registry` (v2 network registry, `/api/v2/network/*`; same image, own container; docs/registry.md) | 20140 | — | 20142 |

## Health

| Service | Endpoint |
| --- | --- |
| `website` | `/healthz` |
| `openpak-registry` | `/healthz` (20140 and 20142) |

Other services' rows live in the canonical ports.md. The 20140 block was
allocated here on 2026-09-19 (next free platform-neutral block); the canonical
file is not under version control, so add the row there by hand.
