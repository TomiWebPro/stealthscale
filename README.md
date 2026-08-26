# StealthScale

> A self-hosted Tailscale control server that replaces WireGuard with the
> **VLESS + XRay + uTLS** transport for maximum stealth.

StealthScale is a fork of [Headscale](https://github.com/juanfont/headscale)
that keeps the full control-plane feature set — node registration, IP
allocation, policy, MagicDNS, DERP — while changing the wire protocol so the
tailnet is not recognisable as Tailscale/Headscale traffic.

## Why

WireGuard handshakes and endpoints are fingerprintable, which makes a
self-hosted tailnet easy to spot and block on monitored networks. StealthScale
replaces the data path with a per-node **VLESS** endpoint behind **XRay**,
shaped with **uTLS** ClientHello fingerprinting. Node traffic looks like
ordinary TLS to a network observer.

## How it works

```
+------------------+   VLESS (deterministic port + UUID)   +------------------+
|  Patched client  | -------------------------------------> |  StealthScale   |
|  (tailscaled)    |   TLS-shaped ClientHello (uTLS)        |  server         |
+------------------+   noise handshake + HTTP/2 machine API  |  (stscale)     |
                                                             +------------------+
```

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

### 5. Connect a patched client

On the client:

```shell
tailscale up \
  --login-server https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls'
```

Fetch the `vless://` URI for a node on the server:

```shell
stscale nodes vless <node-id>
```

## Documentation

The full docs live in [`docs/`](docs/) (MkDocs):

- [StealthScale overview](docs/stealthscale/overview.md)
- [Install & deploy](docs/stealthscale/install.md)
- [Connecting a patched client](docs/stealthscale/clients.md)
- [XRay/VLESS reference](docs/ref/xray-vless.md)
- [Client modification guide](docs/client-modification.md)
- [Configuration reference](docs/ref/configuration.md)

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
