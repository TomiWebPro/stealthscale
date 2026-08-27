# StealthScale

> A self-hosted, stealthy Tailscale-compatible **mesh** — one binary that is
> both the client and the coordinator — using **VLESS + Reality (XTLS) + uTLS**
> so all traffic is indistinguishable from ordinary TLS.

StealthScale is a fork of [Headscale](https://github.com/juanfont/headscale)
with a different end goal: **there is no "head" server and no special client.**
Every device runs the same binary, becomes a *node* in the network, and
coordinates with its peers. An always-on coordinate server is encouraged for
reliability, but **any node can become a coordinate server by default** — there
is no privileged role. The wire protocol is replaced with VLESS + Reality +
uTLS so node-to-node and node-to-coordinator traffic is not recognisable as
Tailscale/Headscale.

See [docs/stealthscale/overview.md](docs/stealthscale/overview.md) for the
project goals and current status.

## Why

WireGuard handshakes and endpoints are fingerprintable, which makes a
self-hosted tailnet easy to spot and block on monitored networks. StealthScale
replaces the data path with a per-node **VLESS** endpoint behind **XRay**,
shaped with **uTLS** ClientHello fingerprinting. Node traffic looks like
ordinary TLS to a network observer.

## How it works

```
        +-------------------- unified StealthScale node (same binary) -------------------+
        |  coordination (embedded)  <----- VLESS+Reality ----->  coordination (peer)    |
        |        ^   |                                  ^                             |
        |        |   | (this node is also a node)         | (peer is also a node)        |
        |        +---+------------------------------------+-----------------------------+
        |  data plane: node <-> node over VLESS stealth (noise inside)                  |
        +------------------------------------------------------------------------------+
```

Every device runs the **same binary**. Each is a *node*. Any node may act as the
coordinator (on by default); the others treat it as the source of truth but still
coordinate directly with each other. There is no separate "headscale" server.

- Every registered node gets its **own VLESS listener**: a deterministic
  port and UUID derived from its node ID (`UUIDv5("stealthscale:<id>")` and
  `sha256("stealthscale-port:<id>")` mapped into the configured range).
- The UUID/port never change across restarts, enabling static client config.
- The standard Tailscale **noise** handshake and HTTP/2 machine API run
  *inside* the authenticated VLESS stream.

See [docs/stealthscale/overview.md](docs/stealthscale/overview.md) for the
full architecture.

## Compatibility

!!! warning

    StealthScale is **not directly compatible** with stock Tailscale clients
    or the original Headscale server, because WireGuard was replaced with
    Xray/VLESS for data transmission.

You need:

- **Server**: this repository (built as `stscale`). It exposes the Headscale
  management API **and** the VLESS endpoints.
- **Client**: a StealthScale-patched Tailscale client that dials VLESS
  instead of WireGuard — see
  [docs/client-modification.md](docs/client-modification.md).

## Quick start

### 1. Build

```shell
make build     # produces ./stscale
```

### 2. Configure

```shell
cp config-example.yaml /etc/stealthscale/config.yaml
# set server_url, then enable the VLESS transport (reality_xtls is the default):
#   xray:
#     enabled: true
#     security: reality_xtls
#     listen_port: 10001
#     max_listen_port: 10100
```

### 3. Run

```shell
stscale serve --config /etc/stealthscale/config.yaml
curl -s https://ctl.example.com/health
curl http://127.0.0.1:8080/web   # embedded WebUI (also at /admin)
```

### 4. Create a user and pre-auth key

```shell
stscale users create alice
stscale preauthkeys create --user <user-id> --reusable
```

### 5. Run another device as a node

There is no special client. Run the **same binary** on any device; it becomes a
node and can act as the coordinator by default:

```shell
stscale serve --config /etc/stealthscale/config.yaml
```

To join an existing network, point it at a coordinator and (if needed) a
pre-auth key:

```shell
stscale up \
  --coordinator https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls'
```

Fetch the `vless://` URI for a node on the coordinator:

```shell
stscale nodes vless <node-id>
```

> **Discovery vs stealth:** finding a coordinator for the first time may use
> whatever is necessary (including non-stealth discovery). But once two peers
> have identified each other, **all further transport is VLESS stealth** — there
> is no plaintext control path. Real XTLS-Reality + uTLS only; no simulated
> stealth.

### Web UI — the control plane

StealthScale ships an **embedded Web UI** (at `/web` and `/admin`) that is the
primary management surface. It is **not** a read-only dashboard: every
configurable aspect of the network — nodes, users, pre-auth keys, tags, policy
(HuJSON), DERP, VLESS endpoints, and coordinator election — must be configurable
from it.

We treat [headscale-ui](https://github.com/gurucomputing/headscale-ui) and the
[Headscale Web UI reference](https://headscale.net/stable/ref/integration/web-ui/)
as **design guidelines and improve on them**: the StealthScale UI talks to the
live control plane, is embedded in the binary, and must expose full write
operations. The current embedded UI is read-only (see
[docs/stealthscale/overview.md](docs/stealthscale/overview.md) → *Current
status*); closing that gap is a core goal.

## Documentation

The full docs live in [`docs/`](docs/) (MkDocs):

- [StealthScale overview](docs/stealthscale/overview.md)
- [Install & deploy](docs/stealthscale/install.md)
- [Connecting a patched client](docs/stealthscale/clients.md)
- [XRay/VLESS reference](docs/ref/xray-vless.md)
- [Client modification guide](docs/client-modification.md)
- [Configuration reference](docs/ref/configuration.md)
- [Web UI usage](docs/usage/webui.md)

## Development

Requires Go 1.26.1+. The repo ships a Nix dev shell and pre-commit hooks
via `prek` (see `AGENTS.md`).

```shell
make dev          # fmt + lint + test + build
make build        # build ./stscale
make test         # go test ./...
```

Server-level tests live in `hscontrol/servertest/` and include an end-to-end
VLESS test (`hscontrol/servertest/xray_vless_test.go`) that drives the
protocol with a raw VLESS client.

## License

BSD-3-Clause. See [LICENSE](LICENSE). This is a modified version of
Headscale; it is not endorsed by the original Headscale maintainers.
