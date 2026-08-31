# Frequently Asked Questions (StealthScale)

## How is StealthScale different from Headscale?

StealthScale is a fork of [Headscale](https://github.com/juanfont/headscale) but replaces the WireGuard transport with **VLESS + Reality (xtls/reality) + uTLS**. Every device runs the same `stscale` binary — there is no privileged "head" server, and `stscale serve` (coordinator) and `stscale up` (node) are the same binary. See [StealthScale overview](../stealthscale/overview.md).

## Why not WireGuard?

WireGuard handshakes are fingerprintable. StealthScale's VLESS+Reality endpoint steals the TLS handshake of a real decoy site (`www.cloudflare.com:443` by default, `www.microsoft.com:443` second decoy) via `github.com/xtls/reality` (MPL-2.0) and shapes ClientHello with `utls` (`chrome`/`firefox`/`safari`/`ios`/`randomized`). Traffic looks like ordinary browser TLS. See [Threat model](../ref/threat-model.md) and [XRay/VLESS reference](../ref/xray-vless.md).

## Can I use a stock Tailscale client?

No, by default. Stock Tailscale dials WireGuard and speaks `noise` over raw TCP. StealthScale with `xray.stealth.enforce_control:true` (the default) exposes **only VLESS+Reality** — no `/ts2021` plaintext endpoint. You need:

- the unified `stscale` binary (`stscale up --coordinator https://ctl.example.com --authkey <key> --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'`) — recommended, or
- a Tailscale client patched to import `hscontrol/xray` (`DialVLESS` + `RealityUClient`), see [Client modification guide](../client-modification.md).

If you must support stock clients, set `xray.stealth.enforce_control:false` (plaintext `/ts2021` remains fingerprintable).

## How do I get the VLESS URI for a node?

On the coordinator:

```shell
stscale nodes vless <node-id>
# vless://<uuid>@<addr>:<port>?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision&dest=www.cloudflare.com%3A443&pbk=<pubkey>&sid=<shortId>&spx=%2F
```

`pbk` is the Reality public key (hex 32 bytes), `sid` the shortId, `dest` the decoy, `spx` the SpiderX. All derived deterministically from `xray.secret` via `HMAC(secret, label)` — stable across restarts when the secret is stable. See [XRay/VLESS reference](../ref/xray-vless.md).

## What is `xray.secret` and why does postgres require it?

`xray.secret` keys the per-node UUID/port (`UUIDv5(HMAC(secret,"uuid-namespace")[:16], "node:<id>")` and `HMAC(secret,"node-port:<id>")`) and the Reality keypair/shortId. For `sqlite` it is auto-persisted to `.xray_secret` next to `db.sqlite`. For `postgres` there is no local file, so you **must** set `xray.secret` explicitly (`openssl rand -hex 32`) or `stscale` refuses to start. Rotating the secret changes every node's `vless://` URI — re-issue with `stscale nodes vless <id>`.

## Why is DERP empty / why do clients have no relay?

StealthScale **gates DERP on stealth** (`xray.stealth.enforce:true`, default). When `stealth.Checker.IsSatisfied()` is false (Reality not serving), `FilterDERPMap()` returns an empty `DERPMap` (fail-closed) so clients cannot leak traffic through fingerprintable relays. When stealth is satisfied, the full DERP map is served. Verify with `curl http://127.0.0.1:8080/web/api/derp | jq .stealth_satisfied` or `go test ./hscontrol/stealth -v`. See [XRay/VLESS reference](../ref/xray-vless.md).

## Which database should I use?

SQLite is recommended. StealthScale is tested primarily on SQLite. PostgreSQL works but requires `xray.secret` to be set explicitly and is considered legacy.

## How do I upgrade?

Follow [Upgrade](../setup/upgrade.md) and always keep `xray.secret` and `xray.reality.*` stable. Backup `db.sqlite` **and** `.xray_secret` (for sqlite) or `xray.secret` value (for postgres) before upgrading. See [Install & deploy](../stealthscale/install.md#upgrading-from-headscale--old-stealthscale).

## Scaling?

StealthScale inherits Headscale's scaling: the map that computes per-node peer visibility is expensive (`NodeStore` copy-on-write snapshot, `state.State.UpdateNodeFromMapRequest` hot path). It handles hundreds of largely-static nodes well, but many frequently-moving nodes (laptops/phones switching endpoints) keep CPU busy. The VLESS listener per node adds a port in `[xray.listen_port, xray.max_listen_port]` per node — size the range for your tailnet.

## My policy is invalid and StealthScale refuses to start?

Dump, fix, and reload via direct DB access (requires full config with DB settings):

```shell
stscale policy get --bypass-server-and-access-database-directly > policy.json
stscale policy check --file policy.json
stscale policy set --bypass-server-and-access-database-directly --file policy.json
```

Also available via WebUI `PUT /web/api/policy` (`hscontrol/webui/webui.go`).

## Where do I get help?

Join our Discord `discord.gg/c84AZQhmpx` and see [Getting help](./help.md). Report StealthScale-specific stealth/VLESS issues via GitHub issues `github.com/tomiwebpro/stealthscale/issues`.
