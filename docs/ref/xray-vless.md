# XRay / VLESS Reference (VLESS + Reality + uTLS)

StealthScale replaces WireGuard with **VLESS + Reality (xtls/reality) + uTLS**
as the stealth transport. The server steals the TLS handshake of a real
Internet site (Reality) via `github.com/xtls/reality` (MPL-2.0) and shapes the
ClientHello with `github.com/refraction-networking/utls` (BSD-3-Clause). This
document is the reference for config, URI, and stealth-gated DERP fallback.

## Defaults (Stealth)

```yaml
xray:
  enabled: true
  listen_addr: 0.0.0.0
  listen_port: 10001
  max_listen_port: 10100
  security: reality_xtls   # VLESS+Reality via XTLS+uTLS (default, true Reality)
  cert_file: ""            # only for tls/xtls; empty for reality_xtls (uses dest)
  key_file: ""
  timeout: 30s
  utls_fingerprint: chrome # chrome, firefox, safari, randomized, ios
  secret: ""               # per-server secret; auto-persisted to .xray_secret next to db.sqlite; MUST be set for postgres
  reality:
    dest: "www.cloudflare.com:443"  # decoy dest whose TLS is stolen; www.microsoft.com works but its 8273-byte cert exceeds 8192 pre-read limit
    server_names:           # SNI values that pass Reality verification
      - www.cloudflare.com
      - www.microsoft.com
      - cloudflare.com
      - microsoft.com
    private_key: ""        # hex 32 bytes, auto-derived from secret if empty
    public_key: ""         # hex, derived from private_key
    short_id: ""           # hex 0-8 bytes, auto-derived from secret (compat)
    short_ids: []          # list of accepted shortIds; empty => [short_id]; may contain "" for empty shortId
    spider_x: "/"          # spiderX prefix
  stealth:
    enforce: true          # gate DERP fallback on stealth (fail-closed)
    enforce_control: true  # gate /ts2021 Noise endpoint (only VLESS when true)
    probe_interval: 30s
    probe_timeout: 5s
```

Alternatives: `security: none` (plain VLESS), `tls` (requires `cert_file`/`key_file`), `xtls`, and `reality_xtls` (alias `reality`). `reality_xtls` is true Reality via `github.com/xtls/reality` — it steals the dest's TLS handshake via `reality.DetectPostHandshakeRecordsLens` and `reality.Server`; the client uses the vendored `hscontrol/xray/reality_client.go` (`RealityUClient`) with uTLS and validates the server via its Reality public key.

## Why reality_xtls

- **VLESS**: lightweight proxying, looks like TLS.
- **Reality (xtls/reality)**: the server replays the dest site's real TLS handshake (cert, EncryptedExtensions) so the endpoint is indistinguishable from the decoy (e.g. `www.cloudflare.com`). Uses `github.com/xtls/reality` (MPL-2.0). The 2044-byte EncryptedExtensions of Cloudflare vs 32 for a local self-signed dest hits the `>512` branch in `reality/tls.go:341` — handled.
- **uTLS**: ClientHello fingerprint mimics Chrome/Firefox (`chrome`, `firefox`, `safari`, `randomized`, `ios` via `hscontrol/xray/client.go:212` `fpToClientHelloID`), defeating active probing.
- **Reality public-key validation**: the client proves knowledge of the server's X25519 public key via the SessionId AEAD trick (see `hscontrol/xray/reality_client.go:66` `VerifyPeerCertificate`); the server's temporary ed25519 cert signature is `HMAC-SHA512(pub, authKey)`.

Together, node traffic is not fingerprintable as Tailscale and not distinguishable from a browser visiting the decoy.

## Deterministic Per-Node Endpoints

Every registered node gets its own VLESS listener on a deterministic port + UUID, keyed by the per-server secret (`xray.secret` / `.xray_secret`):

```go
// with secret (production, HMAC-keyed, not enumerable)
uuid = UUIDv5(HMAC(secret,"uuid-namespace")[:16], "node:<id>")
port = base + HMAC(secret,"node-port:<id>") % (max-min+1)
// without secret (tests only, enumerable fallback)
uuid = UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "stealthscale:<id>")
port = base + sha256("stealthscale-port:<id>") % (max-min+1)
```

- `hscontrol/xray/vless.go:156` `NodeUUID()`, `hscontrol/xray/vless.go:172` `NodePort()` — HMAC-keyed when `xray.secret` is set via `types.XRayConfig.InitIdentity`.
- Never changes across restarts when `xray.secret` is stable (persisted to `.xray_secret` for sqlite, must be set explicitly for postgres) → static client config.
- Fetch: `stscale nodes vless <node-id>` or `GET /web/api/vless/{id}`. The `URI()` includes Reality hints so a patched client can dial with uTLS+Reality.

Example URI (reality_xtls, dual decoys):

```
vless://a1b2c3d4-...@192.0.2.1:10042?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision&dest=www.cloudflare.com%3A443&pbk=<32-byte-pubkey-hex>&sid=<shortId-hex>&spx=%2F
```

- `security=reality_xtls` selects true Reality (steals `www.cloudflare.com:443` by default; `dest` carries the decoy).
- `fp=chrome` is `utls_fingerprint` (`chrome`, `firefox`, `safari`, `ios`, `randomized` → `hscontrol/xray/client.go:247` `fpToClientHelloID`).
- `pbk` is the Reality public key (hex, 32 bytes, from `xray.reality.public_key` derived via `InitIdentity`), `sid` the shortId, `dest` the decoy, `spx` the SpiderX path.
- `type=tcp&flow=xtls-rprx-vision` for URI compatibility.

For `none`, `tls`, `xtls`, URI is `vless://<uuid>@<addr>:<port>?security=<mode>` (no fp/pbk/sid).

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

See implementation `hscontrol/stealth/stealth.go` (`Checker` + `FilterDERPMap`).

## Server + Client Unified

No code difference: both import `hscontrol/xray`:

- Server: `hscontrol/xray/server.go:StartXRayServer` calls `xray.NewServer(&cfg.XRay, handler)` and `EnsureNodeListener`.
- Client: `hscontrol/xray/client.go` `DialVLESS(ctx, cfg)` and `WriteVLESSRequest` — writes VLESS header and awaits version byte. Used by `stscale up --coordinator --vless-uri`.

Same `XRayConfig`, same `VLESSConfig.URI()`. Single binary `stscale` can serve or dial (`stscale serve` and `stscale up`).

Holepunching (STUN/DERP direct path discovery, NAT traversal) is **exempt** from the VLESS requirement: it may use plain UDP/STUN and is only offered when stealth is satisfied (`derp.ShouldIncludeDERP` / `stealth.Checker`). All other transport — node-to-coordinator, node-to-node control, and data via the stealth listener — is VLESS. See `docs/stealthscale/overview.md#bootstrap-discovery-and-steady-state-transport`.

See `hscontrol/xray/client.go` + `cmd/stealthscale/cli/up.go` for the client side (`stscale up`).

## Config Validation

`hscontrol/types/config.go:723` validates:

- `xray.listen_port`/`max_listen_port` sane
- `security` in `none,tls,xtls,reality_xtls,reality`
- `tls`/`xtls` require `cert_file`+`key_file`; `reality_xtls` does NOT.
- Defaults set via `viper.SetDefault` to `enabled:true`, `security:reality_xtls`, `utls:chrome`, `stealth.enforce:true`.

## Tests

Tests:

```bash
go test ./hscontrol/xray -v
go test ./hscontrol/stealth -v
go test ./hscontrol/types -run TestXRay -v
make test
```

## WebUI

VLESS tab shows per-node URI/port/UUID, DERP tab shows stealth badge. See [Web UI](../usage/webui.md).

## References

- `hscontrol/xray/server.go`
- `hscontrol/xray/vless.go`
- `hscontrol/stealth/stealth.go`
- `hscontrol/types/config.go:159` XRayConfig
- `hscontrol/app.go:372` DERPMap updates
