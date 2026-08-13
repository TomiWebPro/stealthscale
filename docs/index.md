______________________________________________________________________

hide:

- navigation
- toc

______________________________________________________________________

# Welcome to StealthScale

StealthScale is an open source, self-hosted implementation of a Tailscale
control server that **replaces WireGuard with the VLESS+XRay+uTLS transport**
for maximum stealth.

This page contains the documentation for the latest version of StealthScale.
Please also check our [FAQ](about/faq.md) and the
[StealthScale overview](stealthscale/overview.md).

## Design goal

StealthScale keeps the full Headscale control-plane feature set — node
registration, IP allocation, policy enforcement, MagicDNS and DERP routing —
while replacing the WireGuard data path with a per-node **VLESS** listener
behind **XRay** and **uTLS** fingerprinting. Instead of exposing a WireGuard
endpoint, every node connects to its own deterministic VLESS endpoint,
authenticates with a deterministic UUID, and speaks the standard Tailscale
noise protocol over the authenticated stream.

The result is a self-hosted tailnet whose traffic is indistinguishable from
ordinary TLS to network observers. It targets self-hosters and operators who
need a private mesh that is not recognisable as Tailscale/Headscale traffic.

## Requirements

- **Server**: the StealthScale server (built as `stscale`). It exposes the
  same management API as Headscale plus the VLESS endpoints.
- **Client**: a StealthScale-patched Tailscale client that dials VLESS
  instead of WireGuard. See [Clients](stealthscale/clients.md) and the
  [client modification guide](client-modification.md).

!!! warning "Compatibility"

    StealthScale is **not compatible** with stock Tailscale clients or the
    original Headscale server. The transport protocol has changed.

## Getting started

Follow the [installation guide](stealthscale/install.md) to deploy the
server, then [connect a client](stealthscale/clients.md).

## Contributing

StealthScale is "Open Source, acknowledged contribution": any contribution
will have to be discussed with the Maintainers before being submitted.

Please see [Contributing](about/contributing.md) for more information.
