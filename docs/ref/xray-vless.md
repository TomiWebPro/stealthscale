# XRay / VLESS Reference (VLESS + uTLS-shaped TLS with decoy cert)

StealthScale replaces WireGuard with **VLESS + uTLS-shaped TLS presenting a decoy
certificate (Reality-style)** as the stealth transport. This document is the
reference for config, URI, and stealth-gated DERP fallback.

## Defaults (Stealth)

```yaml
xray:
  enabled: true
  listen_addr: 0.0.0.0
  listen_port: 10001
  max_listen_port: 10100
  security: reality_xtls   # VLESS+Reality via XTLS+uTLS (default)
  cert_file: ""
  key_file: ""
  timeout: 30s
  utls_fingerprint: chrome # chrome, firefox, safari, randomized
  reality:
    dest: ""               # decoy dest, e.g. www.microsoft.com:443; empty => derived from server_url
    server_names: []       # SNI list for Reality
    private_key: ""        # hex, auto if empty
    public_key: ""         # hex, derived
    short_id: ""           # hex 0-8 bytes
    spider_x: ""           # spiderX prefix
  stealth:
    enforce: true          # gate DERP fallback on stealth (fail-closed)
    probe_interval: 30s
    probe_timeout: 5s
```

Alternatives: `security: none` (plain VLESS), `tls` (requires `cert_file`/`key_file`), `xtls`, and `reality_xtls` (alias `reality`). `reality_xtls` uses `utls` for the ClientHello and requires a `reality.dest` (decoy); the client validates the server via the Reality public key rather than the certificate.

## Why reality_xtls

- **VLESS**: lightweight proxying, looks like TLS.
- **Decoy certificate**: the server presents a self-signed certificate for the `reality.dest` SNI, so the endpoint resembles a legitimate TLS site.
- **uTLS**: ClientHello fingerprint mimics Chrome/Firefox, defeating active probing.
- **Reality public-key validation**: the client authenticates the server by its Reality public key, not by trusting the presented certificate.

Together, node traffic is not fingerprintable as Tailscale.

> **Not a vendored XTLS-Reality library:** the current build uses Go's standard
> `crypto/tls` with the decoy certificate plus `utls` for ClientHello shaping.
> Full XTLS-Reality replay of a real destination's live certificate (the
> `xtls-rprx-vision` flow) requires the `xray-core`/`go-reality` dependency and is
> planned.

## Deterministic Per-Node Endpoints

Every registered node gets its own VLESS listener on a deterministic port + UUID:

```go
uuid = UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "stealthscale:<nodeID>")
port = base + hash("stealthscale-port:<nodeID>") % (max-min+1)
```

- `hscontrol/xray/vless.go:103` `NodeUUID()`
- `hscontrol/xray/vless.go:110` `NodePort()`
- Never changes across restarts → static client config.
- Fetch: `stscale nodes vless <node-id>` or `GET /web/api/vless/{id}`.

Example URI (reality_xtls):

```
vless://a1b2c3d4-...@192.0.2.1:10443?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision
```

- `security=reality_xtls` selects the decoy-cert + uTLS transport (`reality.dest` is required; the client validates via the Reality public key).
- `fp=chrome` is `utls_fingerprint` (ClientHello shaping).
- `flow=xtls-rprx-vision` is accepted for URI compatibility, but the current build performs `utls`-shaped `crypto/tls` with a decoy certificate rather than the full XTLS-Reality flow.

For `none`, `tls`, `xtls`, URI is `vless://<uuid>@<addr>:<port>?security=<mode>` (no fp/flow).

## Stealth-Gated DERP Fallback (Fail-Closed)

DERP relays are fingerprintable; stealth mode gates them.

- Checker: `hscontrol/stealth/stealth.go` `Checker.IsSatisfied()`
- If `xray.enabled && xray.security==reality_xtls && xray.stealth.enforce && !IsSatisfied()` → `FilterDERPMap()` returns empty `Regions: map[int]*DERPRegion{}`.
- Then `h.state.SetDERPMap(filtered)` in `hscontrol/app.go` (both initial load and periodic ticker).
- Netmap sent to clients has **no DERP regions** → clients fail-closed, no relay fallback leaks.

When stealth satisfied, DERPMap is complete as usual (`derp.GetDERPMap` merged + shuffled).

Verify:
```bash
go test ./hscontrol/stealth -v
curl -s http://127.0.0.1:8080/web/api/derp | jq .stealth_satisfied
```

See prompt `prompts/derp-stealth-fallback.md`.

## Server + Client Unified

No code difference: both import `hscontrol/xray`:

- Server: `hscontrol/xray_server.go:StartXRayServer` calls `xray.NewServer(&cfg.XRay, handler)` and `EnsureNodeListener`.
- Client: `hscontrol/xray/client.go` `DialVLESS(ctx, cfg)` and `WriteVLESSRequest` — writes VLESS header and awaits version byte. Used by `stscale up --coordinator --vless-uri`.

Same `XRayConfig`, same `VLESSConfig.URI()`. Single binary `stscale` can serve or dial (`stscale serve` and `stscale up`).

Holepunching (STUN/DERP direct path discovery, NAT traversal) is **exempt** from the VLESS requirement: it may use plain UDP/STUN and is only offered when stealth is satisfied (`derp.ShouldIncludeDERP` / `stealth.Checker`). All other transport — node-to-coordinator, node-to-node control, and data via the stealth listener — is VLESS. See `docs/stealthscale/overview.md#bootstrap-discovery-and-steady-state-transport`.

See `prompts/unified-server-client.md`.

## Config Validation

`hscontrol/types/config.go:723` validates:

- `xray.listen_port`/`max_listen_port` sane
- `security` in `none,tls,xtls,reality_xtls,reality`
- `tls`/`xtls` require `cert_file`+`key_file`; `reality_xtls` does NOT.
- Defaults set via `viper.SetDefault` to `enabled:true`, `security:reality_xtls`, `utls:chrome`, `stealth.enforce:true`.

## Tests

Coders: see `prompts/tests-config.md` and `prompts/vless-reality-xtls.md`.

```bash
go test ./hscontrol/xray -v
go test ./hscontrol/stealth -v
go test ./hscontrol/types -run TestXRay -v
make test
```

## WebUI

VLESS tab shows per-node URI/port/UUID, DERP tab shows stealth badge. See `docs/usage/webui.md` and `prompts/webui-headscale.md`.

## References

- `hscontrol/xray/server.go`
- `hscontrol/xray/vless.go`
- `hscontrol/stealth/stealth.go`
- `hscontrol/types/config.go:159` XRayConfig
- `hscontrol/app.go:372` DERPMap updates
