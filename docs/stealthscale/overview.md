# StealthScale overview

StealthScale is a self-hosted, stealthy Tailscale-compatible **mesh network**.
It is a fork of [Headscale](https://github.com/juanfont/headscale), but with a
different end goal, captured in [Project goals](#project-goals) below. In short:
**there is no "head" server and no special client.** Every device runs the same
binary, becomes a *node* in the network, and coordinates with its peers. An
always-on coordinate server is encouraged for reliability, but **any node can
become a coordinate server by default** — there is no privileged role.

The wire protocol is replaced with **VLESS + uTLS-shaped TLS presenting a decoy
certificate (Reality-style)** so node-to-node and node-to-coordinator traffic is
indistinguishable from ordinary TLS to a network observer.

## Project goals (what "done" means)

Future work is measured against these targets:

1. **Unified server + client.** One binary does both. A device *is* a node; the
   same code that serves the coordination plane also runs on every endpoint. No
   separate "headscale" server binary and no patched/forked Tailscale client.
2. **Everything is a node, coordinating peer-to-peer.** Nodes discover and
   authenticate each other and share state; they do not depend on a single
   controller. A coordinator is just a node that other nodes trust as the source
   of truth — and any node can take that role.
3. **Stealth from first contact — no fake stealth.** The transport must be
   genuinely fingerprint-resistant using **uTLS-shaped TLS with a decoy
   certificate (Reality-style)**. *Discovery* (finding a coordinator/peer for the
   first time) may use whatever it needs to, including non-stealth mechanisms. But
   **once two peers have identified each other, all further transport is VLESS
   stealth** — including the registration handshake that follows discovery. There
   is no "simulated" stealth: this is a deployable project, not a demo. (Full
   XTLS-Reality replay of a real destination's certificate is the target
   enhancement.)
4. **The Web UI is the control plane.** It is not a read-only dashboard. Every
   configurable aspect of the network — nodes, users, pre-auth keys, tags,
   policy (HuJSON), DERP, VLESS endpoints, coordinator election — must be
   configurable from the Web UI. We use
   [headscale-ui](https://github.com/gurucomputing/headscale-ui) and the
   [Headscale Web UI reference](https://headscale.net/stable/ref/integration/web-ui/)
   as **design guidelines and improve on them**: the StealthScale UI is embedded,
   talks to the live control plane, and is the primary management surface.
5. **Deployable.** Defaults are safe and production-shaped; `make build` produces
   a single binary that runs as a node or a coordinator out of the box.

## Why not WireGuard?

WireGuard handshakes and endpoints are fingerprintable: an observer can
recognise the WireGuard UDP protocol, the static public keys, and the
noise-in-noise handshake of Tailscale's control plane. In networks that monitor
or throttle by protocol, this makes a self-hosted tailnet easy to spot and block.

StealthScale replaces the transport so that **all** node-to-control traffic looks
like ordinary TLS to a network observer:

- **VLESS** — the lightweight proxying protocol from the
  [Xray-core](https://github.com/XTLS/Xray-core) ecosystem. A VLESS session
  looks like a plain TLS connection to anything inspecting the stream.
- **uTLS** — ClientHello fingerprinting. The TLS handshake is shaped to mimic a
  real browser's ClientHello, defeating active fingerprinting.
- **Noise** — the standard Tailscale control protocol (TS2021) still runs, but
  *inside* the VLESS stream instead of on the wire.
- **Reality (uTLS + decoy cert)** — the TLS surface is provided by a uTLS-shaped
  `crypto/tls` handshake that presents a decoy certificate for the configured
  `reality.Dest` SNI, so the endpoint resembles a legitimate TLS site. Full
  XTLS-Reality replay of a real destination's live certificate is planned (see
  [Current status](#current-status-known-gaps-for-future-agents)).

## Architecture

```
        +-------------------- unified StealthScale node (same binary) ------------------+
        |  coordination (embedded)  <----- VLESS+Reality ----->  coordination (peer)   |
        |        ^   |                                   ^                            |
        |        |   | (this node is also a node)         | (peer is also a node)       |
        |        +---+-----------------------------------+----------------------------+
        |  data plane: node <-> node over VLESS stealth (noise inside)                 |
        +------------------------------------------------------------------------------+
```

Every device runs the **same binary**. Each is a *node*. Any node may act as the
coordinator (on by default); the others treat it as the source of truth but still
coordinate directly with each other. There is no separate "headscale" server.

The coordination plane runs **one VLESS listener per node**:

- Every registered node gets its own port, derived deterministically from its
  node ID: `port = hash("stealthscale-port:<id>")` mapped into the configured
  range (`xray.listen_port` … `xray.max_listen_port`).
- Every node is authenticated by a deterministic UUID:
  `UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "stealthscale:<id>")`.
- Because the UUID and port are derived from the node ID, they **never change
  across restarts** — which is what makes a static client configuration possible.

When a peer connects (after discovery has identified it):

1. It opens a TCP (or TLS/XTLS) connection to its node's VLESS endpoint.
1. It sends the VLESS header: protocol version, its node UUID, and addon
   metadata. The server verifies the UUID against the node the listener belongs
   to and replies with a version byte.
1. The client and server run the standard Tailscale **noise** handshake
   (`controlbase`) over the authenticated stream.
1. The machine API (registration, map/poll, SSH actions) is served over HTTP/2
   on top of the noise connection.

## Bootstrap, discovery, and steady-state transport

This is the rule future agents must enforce (goal #3):

- **Discovery** — finding a coordinator or a peer for the first time — may use
  whatever mechanism is necessary, *including non-stealth methods*. This is the
  only phase allowed to be visible on the wire.
- **Stealth is mandatory for everything else.** The moment two peers have
  exchanged identity, all communication runs over **VLESS + Reality + uTLS**.
  This includes the registration/bootstrap handshake that follows discovery — it
  is not done over a plaintext control port. There is no separate, un-stealthed
  Tailscale/noise control endpoint as the primary path.

The legacy `/ts2021` noise upgrade path and the plaintext control port are, at
most, an opt-in escape hatch for bootstrapping onto a hostile network, never the
default.

## What stays the same

The control plane is unchanged where it does not conflict with the goals above, so
existing knowledge applies:

- The management API is compatible with Headscale.
- Registration, pre-auth keys, tags, users, policy (HuJSON), MagicDNS, routes and
  DERP relays behave as in Headscale.
- Nodes are identified by their VLESS UUID/port, not by a WireGuard key pair.

## What changes

- **No special client.** A stock Tailscale client dials WireGuard and speaks
  noise over raw TCP; a StealthScale node dials VLESS and speaks noise over the
  authenticated stream — and it runs the *same binary* as the coordinator. See
  [Clients](clients.md) and the
  [client modification guide](../client-modification.md).
- **Coordination is distributed.** While one node usually acts as coordinator,
  every node can; the network does not require a single controller.

## Reality_XTLS and stealth-gated DERP

StealthScale's default transport is **VLESS + uTLS-shaped TLS with a decoy
certificate** (`xray.security: reality_xtls`). The server presents a decoy
certificate for the configured `reality.Dest` SNI, and `utls` shapes the
ClientHello to mimic Chrome/Firefox and defeat active probing. The client
validates the server via the Reality public key rather than by trusting the
presented certificate.

> **Implementation note — this is not the real XTLS-Reality library (yet).**
> The current build does **not** vendor `xray-core`/`go-reality`. It uses the Go
> standard `crypto/tls` with a self-signed decoy certificate plus `utls` for
> ClientHello shaping. Full XTLS-Reality replay of a real destination's live
> certificate requires the `xray-core`/`go-reality` dependency and is planned.

Because DERP relays are themselves fingerprintable, StealthScale **gates DERP
fallback on stealth** (`xray.stealth.enforce: true`, the default). When the
stealth checker determines Reality is not satisfied, the DERP map sent to clients
is empty (fail-closed): no relay regions are advertised, so a client cannot leak
tailnet traffic through a recognisable relay. When stealth is satisfied, the full
DERP map is served as usual.

See the [XRay/VLESS reference](../ref/xray-vless.md) for the full configuration,
the deterministic per-node UUID/port derivation, and the fail-closed DERP gating
algorithm.

## Current status (known gaps for future agents)

This section is the honest gap list so a future agent knows what is TODO versus
done. It reflects the codebase at the time of writing:

- **Web UI is now read-write for core resources.** `hscontrol/webui/webui.go`
  now exposes `POST /api/users`, `POST /api/preauthkeys`, `PUT /api/policy` and
  `DELETE /api/nodes/{id}` (plus `GET` for all). The `State` interface has
  optional `WriteState` methods; handlers validate JSON and delegate to
  `state.State` when available, otherwise stub for coverage. Remaining: coordinator
  election UI and advanced node tag/route management. Use headscale-ui as the
  interaction-model reference.
- **VLESS stealth transport implemented (not a log-only stub).**
  `hscontrol/xray/server.go` generates an ephemeral self-signed certificate for
  `reality.Dest` (the decoy SNI) and shapes the TLS `ClientHello` via
  `applyUTLSFingerprint` (chrome/firefox/safari/randomized cipher suites and
  curves). `hscontrol/xray/client.go` `DialVLESS` writes the VLESS header,
  verifies the version ack over the (optionally TLS-wrapped) transport, and
  validates the server by its Reality public key.
  **Limitation:** this is `crypto/tls` + `utls` shaping, **not** the real
  XTLS-Reality library — replaying a real destination's live certificate requires
  the `xray-core`/`go-reality` dependency and is still TODO.
- **DERP fail-closed stealth gate is now enforced.** `hscontrol/stealth/stealth.go`
  `Checker.IsSatisfied()` drives `FilterDERPMap()` so that when stealth is not
  satisfied the advertised DERP map is empty (fail-closed). This previously was a
  stub and is now wired into both the initial DERP map load and the periodic
  ticker in `hscontrol/app.go`.
- **Bootstrap now supports VLESS stealth.** `hscontrol/xray/client.go` and
  `stscale up --coordinator --authkey --vless-uri` let a node dial its VLESS
  endpoint directly; `hscontrol/auth.go` still creates per-node listeners after
  registration, but post-discovery all transport is VLESS per
  [bootstrap, discovery, and steady-state transport](#bootstrap-discovery-and-steady-state-transport). The legacy `/ts2021` plaintext control port remains only as an opt-in
  escape hatch for hostile-network bootstrapping, never the default.
- **Unified client/coordinator now exists.** `hscontrol/xray/client.go`
  (`DialVLESS`, `WriteVLESSRequest`, `ParseVLESSURI`) is the client side of
  the transport, and `stscale up` is the unified client command (single binary
  `stscale` can `serve` as coordinator and `up` as node). The same
  `XRayConfig`/`VLESSConfig` is used server and client. Full peer-to-peer
  coordination where any node can transparently become coordinator (distributed
  state sync) is still future work for goal #1/#2.

## Naming

- The server binary is built as **`stscale`** (from `./cmd/stealthscale`).
- The module and repository are `github.com/tomiwebpro/stealthscale`.
- The config file, default paths and systemd units use `stealthscale`.

## Next steps

- [Install and deploy the server](install.md)
- [Connect a node (same binary)](clients.md)
- [XRay/VLESS reference](../ref/xray-vless.md)
- Web UI control-plane plan (see goal #4 above; start from `hscontrol/webui/`)
