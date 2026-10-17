# The v2 network registry (`openpak-registry`)

> **Current Switch client (2026-10-13): Harbor**, at
> `~/REPOS/Openpak/emulators/harbor`. Harbor is an independent
> OpenPak project based on Eden. Citron, Ryujinx (desktop and Android), and
> the former OpenPak Eden checkouts have been removed. References to those
> forks below record historical evidence only: do not run their commands,
> reinstall them, or select them for current work. Use Harbor's README and
> current handoff for builds and testing; old results do not establish Harbor parity.

`cmd/openpak-registry` serves the protected v2 distribution surface
(`/api/v2/network/*`, PRD `emulators/prds/universal-tls-forwarding/01`): build
sessions, channel/digest resolution, signed TUF metadata, bundle manifests,
artifacts and `clients/self`. Routes, status codes, the error shape and the
AUTH-007 quotas are unchanged from when the website served them.

It is a separate process because it is a separate trust domain: its own
database, blob store, online Ed25519 signing key and CI identity (OIDC). The
website no longer loads any of that, and no longer links sigstore/go-tuf.
Both binaries ship in the one image: `/app/app` (the website, the default
command) and `/app/openpak-registry`.

## Ports

Registry block 20140 (the next free platform-neutral block in the workspace port plan):

| Listener | Variable | Default |
| --- | --- | --- |
| HTTP (`/api/v2/network/*`, `/internal/status`, `/healthz`) | `REGISTRY_HTTP_ADDR` | `:20140` |
| metrics/pprof (`/metrics`, `/healthz`, `/debug/pprof/`) | `REGISTRY_METRICS_ADDR` | `:20142` |

Only 20140 is routed by Traefik, and only for `/api/v2/`. 20142 stays on the internal network.

## Environment

All of these belong to the registry container; the website ignores them.

| Variable | Meaning |
| --- | --- |
| `REGISTRY_DATABASE_URL` | registry Postgres (tables `registry_*`, migrated on start) |
| `REGISTRY_BLOB_DIR` | content-addressed blob directory (bundles, manifests, metadata) |
| `REGISTRY_ONLINE_KEY_FILE` | the online Ed25519 signing key |
| `REGISTRY_ROOT_METADATA_FILE` | the root metadata the offline root provisioned |
| `REGISTRY_POLICY_ID` | policy id, default `openpak-production` |
| `REGISTRY_OIDC_AUDIENCE` | GitHub Actions OIDC audience; set in production |
| `REGISTRY_OIDC_ISSUER` | default `https://token.actions.githubusercontent.com` |
| `REGISTRY_STAGING_CI_TOKEN`, `REGISTRY_STAGING_CI_REPOSITORY` | staging-only static CI credential, used only when no OIDC audience is set |
| `REGISTRY_ADMIN_KEY` | bearer for `GET /internal/status`; empty makes the route answer 404 |
| `REGISTRY_HTTP_ADDR`, `REGISTRY_METRICS_ADDR` | listeners, above |
| `REGISTRY_LOG_LEVEL` | `debug` also logs every request |

The first four are all-or-nothing: none set answers 503 on every v2 route
(a supported state); some set logs which ones are missing and also answers 503.

## `GET /internal/status`

Read-only operator view, used by the website's admin page. Requires
`Authorization: Bearer <REGISTRY_ADMIN_KEY>` (constant-time compare; 401
otherwise). Answer:

```json
{"channels":[{"name":"switch/stable","revision":"switch/stable/3","sequence":3,"updated_at":"2026-09-19T10:00:00Z", "platform":"switch","channel":"stable","bundle_sha256":"…"}],
 "revisions":[{"id":"switch/stable/3","policy_id":"openpak-production","created_at":"2026-09-19T09:59:58Z","withdrawn_at":null, "platform":"switch","channel":"stable","sequence":3,"bundle_sha256":"…","manifest_sha256":"…"}],
 "clients":[{"id":"client-1","name":"openpak/citron","status":"production-approved", "kind":"emulator","environment":"production"}]}
```

Mapping to the tables: a channel's `name` is `platform/channel` (its key);
a revision's `id` is `platform/channel/sequence` (the table's primary key),
and a channel's `revision` is that same id for the row it points at;
`created_at` is the revision's `issued_at`; a client's `name` is its
`repository`. Revisions are the 50 newest, withdrawn ones included.

## Deploying: do this BEFORE deploying a website version without v2

The website from this version on no longer serves `/api/v2/`. Bring the
registry up first, or v2 clients get the website's 404 instead of the registry:

1. Run a second container from the same image as the website, with command
   `/app/openpak-registry`, on the same internal network as Traefik:
   - move every `REGISTRY_*` variable (and the mounted key, root metadata and
     blob directory) from the website container to this one;
   - add `REGISTRY_ADMIN_KEY` (a fresh random secret; give the same value to
     the website's admin page configuration when that lands);
   - nothing else from the website's environment is needed.
2. Check it: `curl http://<container>:20140/healthz` and
   `curl http://<container>:20142/metrics`.
3. Add a Traefik router for the registry, on the same entry point(s) and TLS
   settings as the website's openpak.org router, with a higher priority, so it
   wins for `/api/v2/` only:

   ```yaml
   http:
     routers:
       openpak-registry:
         rule: "Host(`openpak.org`) && PathPrefix(`/api/v2/`)"
         priority: 100   # above the website router for Host(`openpak.org`)
         service: openpak-registry
         # entryPoints / tls: copy from the website router
     services:
       openpak-registry:
         loadBalancer:
           servers:
             - url: "http://<registry-container>:20140"
   ```

4. Verify through Traefik: `curl -i https://openpak.org/api/v2/network/clients/self`
   must answer the registry's JSON `401 unauthorized` (or `503 unavailable` if
   unconfigured), not the website's HTML 404.
5. Deploy the new website version, then remove the `REGISTRY_*` variables and
   registry mounts from the website container.

The website container's command is unchanged (`/app/app` is the image's
default). The image now declares it as `CMD` rather than `ENTRYPOINT`, so the
registry container's command replaces it instead of being passed to it as an
argument.

## Keeping the TUF timestamp fresh

The signed `timestamp.json` lives 7 days (`timestampTTL` in `internal/registry/sign.go`) and
only a publication renews it. A week without a policy change used to leave every certified
fetch failing with `registry: timestamp metadata expired` (2026-10-04: all ten channels had
lapsed since 2026-09-30). `openpak-policy refresh [--older-than 72h]` re-signs each channel's
current content at a higher sequence (what `rollback` does, for every channel). The box runs
it daily from `openpak-registry-refresh.timer`:
`podman exec openpak-registry /app/openpak-policy refresh`.
