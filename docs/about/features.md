# Features (StealthScale)

StealthScale keeps the full Headscale control-plane feature set while replacing the WireGuard data path with **VLESS + Reality + uTLS** for stealth. The embedded Web UI is the primary control plane.

## Stealth transport (StealthScale-only)

- [x] **VLESS + Reality (xtls/reality) + uTLS** — per-node deterministic UUID/port listeners, decoy dest `www.cloudflare.com:443` (dual decoy `www.microsoft.com:443`), ClientHello shaped as `chrome`/`firefox`/`safari`/`ios`/`randomized` (`hscontrol/xray/client.go:247`). See [XRay/VLESS reference](../ref/xray-vless.md) and [Threat model](../ref/threat-model.md).
- [x] **Unified binary** — single `stscale` binary: `stscale serve` (coordinator) and `stscale up --coordinator --vless-uri` (node, `hscontrol/xray/client.go` `DialVLESS` + `RealityUClient`). No separate headscale server or patched `tailscaled` required, but patched client reference exists (`client/patch/direct.go.diff`). See [StealthScale clients](../stealthscale/clients.md).
- [x] **Stealth-gated DERP (fail-closed)** — `hscontrol/stealth/stealth.go` `Checker.IsSatisfied()` + `FilterDERPMap()`; when Reality not satisfied DERP map is empty, no relay leak. See [XRay/VLESS reference](../ref/xray-vless.md).
- [x] **Deterministic per-node endpoints** — `UUIDv5(HMAC(secret,"uuid-namespace")[:16], "node:<id>")` and `HMAC(secret,"node-port:<id>")` mapped into `[xray.listen_port, xray.max_listen_port]` (`hscontrol/xray/vless.go:152`). Static `vless://` URI with `pbk`/`sid`/`dest`/`spx`/`fp`.
- [x] **Embedded Web UI** — `hscontrol/webui` at `/web` and `/admin` (same binary for server and client), hardened (`enforce_control:true` → `401` without `Authorization: Bearer <api-key>`). Tabs: Nodes/Users/Keys/Policy/DERP/VLESS/Health. See [Web UI usage](../usage/webui.md).

## Control plane (inherited from Headscale, unchanged where not conflicting)

- [x] Full "base" support of Tailscale features via management API compatible with Headscale
- [x] [Node registration](../ref/registration.md) — web auth and [pre-auth keys](../ref/registration.md#pre-authenticated-key) (tags XOR user ownership, `IsTagged()` authoritative)
- [x] [DNS](../ref/dns.md) — MagicDNS, global/split nameservers, search domains, extra records (A/AAAA)
- [x] File sharing — Taildrive/Taildrop
- [x] [Tags](../ref/tags.md)
- [x] [Routes](../ref/routes.md) — subnet routers, exit nodes, `Via` filtering, HA probing (`node.routes.ha.probe_interval`)
- [x] Dual stack, ephemeral nodes, peer relays
- [x] Embedded [DERP server](../ref/derp.md) — enabled via `derp.server.enabled`, STUN `udp/3478`, verify_clients
- [x] [Policy](../ref/policy.md) — ACLs, Grants, autogroups (`self`, `member`, `internet`, `tagged`), auto-approvers, Tailscale SSH, nodeAttrs, tests
- [x] [OIDC](../ref/oidc.md) — generic OIDC, PKCE, domain/user/group filters
- [ ] Funnel — not supported (tracking via GitHub issue)
- [ ] Serve — not supported
- [ ] Network flow logs

## Compatibility

!!! warning

    StealthScale is **not compatible** with stock Tailscale clients or the original Headscale server under default `enforce_control:true`. The transport is VLESS+Reality, not WireGuard. See [StealthScale overview](../stealthscale/overview.md) and [Client modification guide](../client-modification.md).

For per-feature limitations see the respective `ref/` pages and the honest gap list in [StealthScale overview](../stealthscale/overview.md#current-status-known-gaps-for-future-agents).
