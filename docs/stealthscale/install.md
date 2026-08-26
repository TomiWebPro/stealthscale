# Install & deploy the StealthScale server

This page covers installing, configuring and deploying the StealthScale
server (`stscale`). Deployment options are, in order of convenience:

1. Build from source (this repository)
1. Run as a systemd service
1. Run in a container

!!! note "Prerequisites"

    - A Linux/macOS host with a public IP or a reverse proxy. The server
      must be reachable from the Internet.
    - Go 1.26.1 or newer to build from source (`go.mod`).
    - The [patched client](clients.md) for any node you want to connect
      over VLESS.

## Build the binary

```shell
git clone https://github.com/tomiwebpro/stealthscale
cd stealthscale
make build          # produces ./stscale
```

or directly:

```shell
go build -o stscale ./cmd/stealthscale
```

Verify the build:

```shell
./stscale version
```

## Configure

Copy the example configuration and edit it:

```shell
mkdir -p /etc/stealthscale
cp config-example.yaml /etc/stealthscale/config.yaml
```

Minimal settings to change:

- `server_url` — the public base URL of your control server, e.g.
  `https://ctl.example.com`.
- `listen_addr` / `metrics_listen_addr` — where the HTTP API listens.
- `noise.private_key_path` — the key used for the noise handshake; the
  server generates it on first start.

### Enabling the VLESS transport

The `xray` section controls the stealth transport. The default (and
recommended) security mode is `reality_xtls` — VLESS + XTLS-Reality with a
uTLS ClientHello, which makes every node endpoint indistinguishable from a
legitimate TLS site to a network observer.

```yaml
xray:
  # Enables the VLESS transport listeners. Defaults to true.
  enabled: true

  # Address the per-node VLESS listeners bind to.
  listen_addr: 0.0.0.0

  # First port in the per-node allocation range.
  listen_port: 10001

  # Last port in the per-node allocation range. Each node's port is derived
  # deterministically from its node ID within [listen_port, max_listen_port].
  max_listen_port: 10100

  # Transport security. Default: "reality_xtls" (VLESS + XTLS-Reality with a
  # uTLS ClientHello — the stealth transport). Other modes: "none" (plain
  # VLESS over TCP), "tls" and "xtls" (TLS-wrapped VLESS). "reality" is an
  # alias for "reality_xtls".
  security: reality_xtls

  # cert_file / key_file are OPTIONAL with reality_xtls: when omitted, the
  # server performs a Reality handshake to Reality.Dest instead of presenting
  # a local certificate. Required for "tls"/"xtls", and for reality_xtls when
  # you want to present a local cert rather than use the dest-based handshake.
  cert_file: ""
  key_file: ""

  # How long to wait for a client's VLESS header before closing the
  # connection.
  timeout: 30s

  # XTLS-Reality parameters (only used when security == "reality_xtls").
  reality:
    # Decoy destination the server mimics, e.g. "www.microsoft.com:443".
    # If empty, derived from server_url.
    dest: ""
    # SNI values that pass Reality verification.
    server_names: []
    # Reality private/public keys (hex). Auto-generated if empty.
    private_key: ""
    public_key: ""
    # Reality short ID (hex, 0-8 bytes).
    short_id: ""
    # Reality spiderX path prefix.
    spider_x: ""

  # uTLS ClientHello fingerprint to mimic (chrome, firefox, safari,
  # randomized, ...). Only effective with reality_xtls. Default: chrome.
  utls_fingerprint: chrome

  # Stealth gates DERP fallback on stealth verification. When enforce is
  # true (default with reality_xtls), DERP relays are only offered when the
  # VLESS+Reality transport passes stealth checks (fail-closed).
  stealth:
    enforce: true
    probe_interval: 30s
    probe_timeout: 5s
```

!!! warning "Firewall"

    When `enabled: true`, the server listens on a range of UDP-free **TCP**
    ports (`listen_port` … `max_listen_port`). Open these ports in your
    firewall and forward them through any NAT. Each node only uses the one
    port it is assigned, but the range must be reachable in advance because
    ports are derived from node IDs.

### TLS for the control API and VLESS

Two independent TLS decisions:

- **Control API** — terminate TLS at `server_url` with your reverse proxy
  (recommended) or the server's built-in TLS (`tls_cert_path` /
  `tls_key_path`). See [TLS](../ref/tls.md).
- **VLESS transport** — the default `reality_xtls` mode needs **no local
  certificate**: the server mimics a legitimate TLS site via the Reality
  handshake to `reality.dest`, so the connection is indistinguishable from
  HTTPS without you provisioning a cert. If you instead set `security: tls`
  or `security: xtls`, point `cert_file` / `key_file` at a certificate for
  the host the clients will dial. With `reality_xtls` you may still supply
  `cert_file` / `key_file` to present a local certificate instead of using
  the dest-based handshake.

## Run the server

```shell
stscale serve --config /etc/stealthscale/config.yaml
```

Verify the health endpoint responds:

```shell
curl -s https://ctl.example.com/health
```

### systemd

A minimal unit file:

```ini
[Unit]
Description=StealthScale control server
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/stscale serve --config /etc/stealthscale/config.yaml
Restart=on-failure
User=stealthscale
Group=stealthscale
ReadOnlyPaths=/etc/stealthscale
StateDirectory=stealthscale

[Install]
WantedBy=multi-user.target
```

## Deploying in a container

The repository ships `Dockerfile.integration` (debug build with `dlv`) and
the standard multi-stage build used by CI. A production-style deployment:

```shell
docker build -t stealthscale:dev -f Dockerfile.integration .
docker run --name stealthscale \
  --detach \
  --publish 8080:8080 \
  --publish 10001-10100:10001-10100 \
  --volume "$(pwd)/config:/etc/stealthscale:ro" \
  --volume "$(pwd)/lib:/var/lib/stealthscale" \
  stealthscale:dev serve --config /etc/stealthscale/config.yaml
```

## Register users and pre-auth keys

Once the server is running, create a user and a pre-auth key for it:

```shell
stscale users create alice
stscale preauthkeys create --user <user-id> --reusable
```

The output prints the pre-auth key. Copy it to the client.

!!! note "Node IDs and VLESS endpoints"

    Node IDs are allocated at registration. To hand a client its VLESS
    endpoint, fetch it once the node exists:

    ```shell
    stscale nodes vless <node-id>
    ```

    which prints the endpoint's `id`, `address`, `port`, `security` and the
    `vless://` URI to give the client.

## Next steps

- [Connect a patched client](clients.md)
- [XRay/VLESS reference](../ref/xray-vless.md)
- [Upgrading](../setup/upgrade.md)
