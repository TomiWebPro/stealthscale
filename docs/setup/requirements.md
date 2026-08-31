# Requirements (StealthScale)

StealthScale should just work as long as the following requirements are met:

- A server with a public IP address for `stscale`. Dual-stack (public IPv4 + IPv6) recommended.
- StealthScale served via HTTPS on `tcp/443` for the control plane **and** the per-node VLESS+Reality listeners on `tcp/10001`–`10100` (default `xray.listen_port` … `xray.max_listen_port`, configurable via [`config-example.yaml`](https://github.com/tomiwebpro/stealthscale/blob/main/config-example.yaml) and `hscontrol/types/config.go:723`). Expose the full VLESS range publicly — each node gets one deterministic port in that range (`HMAC(secret,"node-port:<id>")` mapped into the range, `hscontrol/xray/vless.go:172`).
- A reasonably modern Linux or BSD OS.
- Go `1.26.1+` for building (`go.mod:3`, `nix develop` pins the toolchain).
- A little command-line knowledge.

## Ports in use

The ports vary with scenario. Don't change them without also updating `xray.listen_port`/`max_listen_port` and firewall rules.

- `tcp/80`
  - HTTP, Let's Encrypt HTTP-01 challenge. Only if `tls_letsencrypt_hostname` with `HTTP-01` is used. See [TLS](../ref/tls.md).
- `tcp/443`
  - HTTPS, control plane (`server_url`). Required for Tailscale clients that assume `443` (see `issue 2164` in Headscale track) and for [embedded DERP](../ref/derp.md) if enabled.
  - For full stealth the control-plane TLS should present the decoy certificate too: either wrap `listen_addr` with `reality.Config` (same `Dest`/`ServerNames`) or place `server_url` behind a Reality-enabled reverse proxy (per-node VLESS listeners at `xray.reality.dest` already do via `github.com/xtls/reality`). See [Threat model](../ref/threat-model.md).
- `tcp/10001`–`10100` (default)
  - **VLESS+Reality_XTLS** per-node listeners (`xray.enabled:true`, `xray.security:reality_xtls`, default). Each node's `stscale nodes vless <id>` URI exposes its port. The range bounds the deterministic derivation (`HMAC(secret,"node-port:<id>") % (max-min+1)`). Expose the entire range or at least the ports for your nodes. Requires `xray.secret` stable (auto to `.xray_secret` for sqlite, must be set explicitly for postgres).
  - Alternatives: `security:none` (plain VLESS), `tls`/`xtls` (require `cert_file`/`key_file`). But `reality_xtls` is the default stealth transport.
- `udp/3478`
  - STUN, only if [embedded DERP](../ref/derp.md) `derp.server.enabled:true` (STUN helps NAT traversal). The DERP map is **gated fail-closed** on stealth (`xray.stealth.enforce:true` + `Checker.IsSatisfied()` → `FilterDERPMap()` empty when not satisfied) — clients get no relay when stealth not ready.
- `tcp/9090`
  - Metrics and debug (`metrics_listen_addr`, default `127.0.0.1:9090`). Keep private. Disable with `metrics_listen_addr: null`. See [Debug & metrics](../ref/debug.md).

## Assumptions

Docs and examples assume:

- StealthScale runs as a system service via a dedicated local user. Debian package default is `stealthscale` (check `/usr/share/doc/stealthscale/`), `nix` example uses `stealthscale`, systemd unit at `packaging/systemd/stealthscale.service` ships `ExecStart=/usr/bin/stealthscale serve` with `User=coordination` in the current packaging — adjust to your setup and keep the user consistent with `ReadWritePaths`/`WorkingDirectory`.
- Config loaded from `/etc/stealthscale/config.yaml` (also `$HOME/.stealthscale` or CWD; override with `-c`/`--config` or `STSCALE_CONFIG`).
- SQLite as database (`/var/lib/stealthscale/db.sqlite` plus sibling `.xray_secret` for VLESS identity). For `postgres`, `xray.secret` must be set explicitly (`openssl rand -hex 32`).
- Data directory `/var/lib/stealthscale` (or `/var/lib/coordination` for the current deb). URLs/placeholders use `stscale.example.com` or `ctl.example.com`.

[^1]: Tailscale assumes HTTPS on `443` in certain situations. HTTP or non-443 HTTPS is possible but `443` is strongly recommended for production.

For the authoritative StealthScale setup, see [StealthScale Install & deploy](../stealthscale/install.md) (VLESS+Reality_XTLS) and [Configuration reference](../ref/configuration.md) / [XRay/VLESS reference](../ref/xray-vless.md).
