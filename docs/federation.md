# How federation works

> Trimmed copy for this repository; the canonical document lives at
> `Openpak/docs/federation.md` and governs. Last synchronised 2026-09-15.

Someone in Australia runs a small box, registers it, and from then on rooms hosted by
Australians are served from Australia. Their friends in France join those rooms the same way
they always did. Nobody in Australia has an Australian account, an Australian friends list or
an Australian matchmaking server: there is one of each, at openpak.org, and the box in Sydney
is only where the packets go.

…

## The parts

```
                           openpak.org (home)
        ┌───────────────────────────────────────────────────────────┐
        │  account core ── federation_nodes, node keys, node_id      │
        │  website ─────── /api/v1/regions  /api/v1/federation/announce  admin › Region nodes
        │  nx-baas ─────── "which country is this player from"       │
        │  photon ──────── Name + Master (+ home's own Game)          │
        │  stardew ─────── NPLN matchmaking + gamesync (+ home TURN)  │
        └───────────────────────────────────────────────────────────┘
                 ▲ announce, once a minute        ▲ room reports
                 │                                │
        ┌────────┴────────────────────────────────┴───────────┐
        │  node au-sydney: photon (Game role only), coturn,   │
        │                  a curl loop that announces         │
        └─────────────────────────────────────────────────────┘
```

…

**The website** is the node's only door. `POST /api/v1/federation/announce` takes the node's
own key as a bearer and relays it to the core, so the core's gRPC never leaves the internal
network. `GET /api/v1/regions` is the public list of approved nodes seen in the last three
minutes, with their services and live player counts, and it is the one thing every
matchmaking server, the status page and the app read. The admin page mints a node's key
(shown once, the core keeps a hash) and flips approval.

…

## The announce cycle

Once a minute the node posts what it runs:

```json
{"services":{"photon":"203.0.113.9:22002","turn":"203.0.113.9:3478","stun":"203.0.113.9:3478"}}
```

The core replaces the node's `services` and touches `last_seen_at`. A node that misses three
announces drops out of `/regions`; the matchmaking servers see that within another minute and
stop placing rooms there. Rooms already on it keep going until they end: nothing tears them
down, and nothing needs to.

A service a node does not run is left out, and home's own is used for it. A node can be
TURN-only, or Photon-only.

## Where the code is

| What | Where |
| --- | --- |
| node register, key resolution, session stamping | `account/internal/federation`, `account/internal/grpcapi/server.go`, migration `0010` |
| public list, announce relay, admin page | `website/internal/api/api.go`, `website/internal/admin/nodes.go` |
| country of a Photon identity | `nx-baas/gameapi.go`, `photonIdentity` |
| room placement, node mode, room reports | `servers/photon/server/OpenPak.Server.LoadBalancing/Federation.cs`, `MasterServerHandler.GameAddressFor`, `docs/federation.md` there |
| regional STUN/TURN | `servers/npln/titles/stardew-valley/npln/federation.go` |
| the node itself | `node/compose.yml`, `node/README.md` |
| region rows on the status page | `status/cmd/statuspage/main.go` |
