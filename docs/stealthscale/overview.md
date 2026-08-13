# StealthScale overview

StealthScale is a fork of the [Headscale](https://github.com/juanfont/headscale)
control server that replaces the WireGuard data path with the
**VLESS + XRay + uTLS** transport. It keeps the full control-plane feature
set — node registration, IP allocation, policy, MagicDNS, DERP — while
changing the wire protocol so the tailnet is not recognisable as
Tailscale/Headscale traffic.

## Why not WireGuard?

WireGuard handshakes and endpoints are fingerprintable: an observer can
recognise the WireGuard UDP protocol, the static public keys, and the
noise-in-noise handshake of Tailscale's control plane. In networks that
monitor or throttle by protocol, this makes a self-hosted tailnet easy to
spot and block.

StealthScale replaces the transport so that **all** node-to-control traffic
looks like ordinary TLS to a network observer:

- **VLESS** — the lightweight proxying protocol from the
  [Xray-core](https://github.com/XTLS/Xray-core) ecosystem. A VLESS session
  looks like a plain TLS connection to anything inspecting the stream.
- **uTLS** — ClientHello fingerprinting. The TLS handshake is shaped to
  mimic a real browser's ClientHello, defeating active fingerprinting.
- **Noise** — the standard Tailscale control protocol (TS2021) still runs,
  but *inside* the VLESS stream instead of on the wire.

## Architecture

```
+------------------+   VLESS (deterministic port, UUID)   +------------------+
|  Patched client  | -------------------------------------> |  StealthScale   |
|  (tailscaled)    |   TLS-shaped ClientHello (uTLS)        |  server         |
+------------------+   noise handshake + HTTP/2 machine API  |  (stscale)     |
                                                             +------------------+
```

The server runs **one VLESS listener per node**:

- Every registered node gets its own port, derived deterministically from
  its node ID: `port = hash("stealthscale-port:<id>")` mapped into the
  configured range (`xray.listen_port` … `xray.max_listen_port`).
- Every node is authenticated by a deterministic UUID:
  `UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "stealthscale:<id>")`.
- Because the UUID and port are derived from the node ID, they **never
  change across restarts** — which is what makes a static client
  configuration possible.

When a client connects:

1. It opens a TCP (or TLS/XTLS) connection to its node's VLESS endpoint.
1. It sends the VLESS header: protocol version, its node UUID, and addon
   metadata. The server verifies the UUID against the node the listener
   belongs to and replies with a version byte.
1. The client and server run the standard Tailscale **noise** handshake
   (`controlbase`) over the authenticated stream.
1. The machine API (registration, map/poll, SSH actions) is served over
   HTTP/2 on top of the noise connection.

## What stays the same

The control plane is unchanged, so all existing knowledge applies:

- The management API is compatible with Headscale.
- Registration, pre-auth keys, tags, users, policy (HuJSON), MagicDNS,
  routes and DERP relays behave exactly as in Headscale.
- The server also keeps the legacy `/ts2021` noise upgrade path, so the
  HTTP/control endpoints are reachable with a stock client — but the
  stealth transport is only available through the VLESS endpoints.

## What changes

- **Clients must be patched.** A stock Tailscale client dials WireGuard
  (`wgengine`) and speaks noise over a raw TCP connection; a StealthScale
  client dials VLESS and speaks noise over the authenticated stream. See
  [Clients](clients.md) and the
  [client modification guide](../client-modification.md).
- **No WireGuard keys.** Nodes are identified by their VLESS UUID/port, not
  by a WireGuard key pair.

## Naming

- The server binary is built as **`stscale`** (from `./cmd/stealthscale`).
- The module and repository are `github.com/tomiwebpro/stealthscale`.
- The config file, default paths and systemd units use `stealthscale`.

## Next steps

- [Install and deploy the server](install.md)
- [Connect a patched client](clients.md)
- [XRay/VLESS reference](../ref/xray-vless.md)
