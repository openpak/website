# openpak-opsd — the host agent behind the services page

`openpak-opsd` (`cmd/openpak-opsd`) lets the admin dashboard see and control the OpenPak
containers on one host: list them with state, health, network and pending updates, and
restart, stop, start or update one. It is a small stdlib-only binary that runs **on the host,
not in a container**, as the user who owns the rootless podman containers and their quadlet
`systemd --user` units.

## Why a separate process

The website runs in a container and must never be given the podman socket. A process holding
that socket can run any image with any mount on the host; a single bug in a public-facing web
app would become host compromise. opsd is the narrow alternative: it holds the host access
itself and offers the website exactly five verbs (list, restart, stop, start, update) on
containers whose name starts with a prefix, behind a bearer key. There is no exec, no logs,
no arbitrary unit, no arbitrary image — the unit and image always come from podman, never from
the request.

It has to be a plain binary because it needs the user's `systemd --user` instance and the
user's podman storage; inside a container it would need exactly the socket and privileges this
design exists to keep out of containers.

## API

Every route except `GET /healthz` needs `Authorization: Bearer <OPSD_KEY>`. The website may send
`X-OpenPak-Actor: <admin>`; it is logged with every action and trusted for nothing else.

| Route | Answer |
| --- | --- |
| `GET /healthz` | `200 ok` (no auth) |
| `GET /v1/services` | `{"host", "checked_at": RFC3339\|null, "services": [{"name", "unit", "image", "state", "health", "started_at", "networks", "update_available", "protected"}]}` |
| `POST /v1/services/{name}/{action}` | `action` is `restart`, `stop`, `start` or `update`; `200 {"ok": true, "message"}` or 4xx/5xx `{"ok": false, "message"}` |
| `POST /v1/updates/check` | `202 {"ok": true, "message"}`; the check runs in the background |

- `state` is podman's (`running`, `exited`, `created`, `paused`, ...); `health` is
  `healthy`/`unhealthy`/`starting` for containers with a healthcheck, else `none`.
- `unit` is the `PODMAN_SYSTEMD_UNIT` label quadlet sets. A prefixed container without it is
  listed with `"unit": ""` and every action on it is refused with 409.
- `restart`/`stop`/`start` run `systemctl --user <verb> <unit>`. `update` runs
  `podman pull <image>` (2 min limit) then `systemctl --user restart <unit>`, then re-checks
  updates in the background.
- Protected services may be restarted and updated but never stopped (409).
- One action per unit at a time; a second click while one is running gets 409 rather than
  queuing behind it.
- `update_available` comes from `podman auto-update --dry-run --format json` (`"Updated":
  "pending"`), run at start, every `OPSD_UPDATE_CHECK` and on `POST /v1/updates/check`, and
  cached. `GET /v1/services` never waits on a registry. It only covers containers with an
  `AutoUpdate=registry` (or `local`) policy.
- Errors: 401 wrong key, 503 no key configured, 404 unknown or non-prefixed container or
  unknown action, 409 refused, 502 podman/systemctl failed (message carries its last line).

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPSD_KEY` | — | Bearer key. **Unset refuses every call with 503** (fail closed). |
| `OPSD_ADDR` | `127.0.0.1:20013` | Listen address. |
| `OPSD_PREFIX` | `openpak-` | Only containers whose name starts with this are managed. |
| `OPSD_PROTECTED` | `openpak-website,openpak-account` | Never-stop list; container names or unit names without `.service`. |
| `OPSD_NETWORK_MAP` | — | Path to a JSON file mapping a regexp on the container name to a network, or several comma-separated (`"wiiu,3ds"`). A container whose name lacks the prefix but whose quadlet unit has it (switch-nexd) is managed too. |
| `OPSD_UPDATE_CHECK` | `15m` | Interval of the background update check (Go duration). |
| `OPSD_HOST_NAME` | `os.Hostname()` | The `host` shown on the dashboard. |

### Port

**20013** — the first spare port of the website's block (20010 HTTP, 20012 metrics; see
[ports.md](ports.md)). opsd exists only for the website, so it lives in the website's block
rather than taking a service block. The canonical `Openpak/ports.md` should get the same row.

## Networks

The dashboard groups services by network. In order:

1. The container label `org.openpak.network`, comma-separated for a service shared by several
   (`switch`, or `wiiu,3ds`). In a quadlet `.container` file:

   ```ini
   [Container]
   Label=org.openpak.network=wiiu,3ds
   ```

   A label change needs `systemctl --user daemon-reload` and a restart of the unit, because
   quadlet recreates the container with it.

2. Otherwise `OPSD_NETWORK_MAP`, first match wins in file order:

   ```json
   {
     "^openpak-(nn-|nex-)": "wiiu",
     "^openpak-(switch-|nx-baas|npln-)": "switch",
     "^openpak-(account|website|saves|news|chat|mods)$": "core"
   }
   ```

3. Otherwise `unassigned`.

## Build and run

```sh
CGO_ENABLED=0 go build -trimpath -o openpak-opsd ./cmd/openpak-opsd
install -m 0755 openpak-opsd ~/.local/bin/
```

Tagged releases also attach static `openpak-opsd-linux-{amd64,arm64}` builds next to
`openpak-network`.

Key and settings in `~/.config/openpak-opsd.env` (mode 0600):

```sh
OPSD_KEY=<openssl rand -hex 32>
OPSD_ADDR=<private or gateway address>:20013
OPSD_HOST_NAME=openpak-home
OPSD_NETWORK_MAP=/home/openpak/.config/openpak-opsd-networks.json
```

`~/.config/systemd/user/openpak-opsd.service` — a plain user service, not a quadlet, since it
is not a container:

```ini
[Unit]
Description=OpenPak host agent (admin dashboard services API)
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%h/.config/openpak-opsd.env
ExecStart=%h/.local/bin/openpak-opsd
Restart=on-failure
RestartSec=5
# No NoNewPrivileges: rootless podman re-execs through the setuid newuidmap
# helper, and without it every podman call fails ("cannot re-exec process").

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now openpak-opsd
loginctl enable-linger "$USER"   # already true on a host running rootless quadlets
curl -s http://127.0.0.1:20013/healthz
```

The website then needs the agent's URL and the same key (its own configuration; see the admin
dashboard's documentation once the services page lands).

## Security notes

- **Bind address.** The default is loopback, which the website container cannot reach. Widen
  it deliberately to an address the website container reaches the host on and nothing public
  does: a private interface, or the podman network's gateway. Rootless podman keeps bridge
  networks in its own namespace, so check what the container actually sees
  (`podman exec openpak-website getent hosts host.containers.internal`) and bind that address,
  never `0.0.0.0` on a host with a public interface. The port stays off the public firewall
  like every 20xxx port.
- **The key is the whole authentication.** Generate it with `openssl rand -hex 32`, keep it in
  the 0600 env file here and in the website's secret, nowhere else. Compared in constant time.
- **Rotation.** The key is read at start. Write the new key to both sides, then
  `systemctl --user restart openpak-opsd` and restart the website; calls in between get 401,
  which the dashboard shows as an error, not an outage.
- **Blast radius.** A stolen key can restart, stop (except protected), start or update prefixed
  containers — a denial of service at worst. It cannot run an image other than the one a unit
  already names, change a unit, read logs or reach anything outside the prefix.
- Every action is logged at info with the service, action, actor and remote address:
  `journalctl --user -u openpak-opsd`.
