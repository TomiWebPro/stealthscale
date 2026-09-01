# Configuration (StealthScale)

StealthScale loads YAML config via `hscontrol/types/config.go` (`LoadConfig` → `LoadServerConfig` with `validateServerConfig`). The file is searched at:

- `/etc/stealthscale`
- `$HOME/.stealthscale`
- current working directory

Override with `-c`/`--config` or `STSCALE_CONFIG`. Validate with:

```shell
stscale configtest
# or: stscale serve --config /etc/stealthscale/config.yaml --dry-run (if available)
```

!!! example "Example configuration"

    Always pick the same tag as your release — `main` may contain unreleased `xray.*` changes.

    === "View on GitHub"
        - Development: <https://github.com/tomiwebpro/stealthscale/blob/main/config-example.yaml>
        - Version {{ stscale.version }}: https://github.com/tomiwebpro/stealthscale/blob/v{{ stscale.version }}/config-example.yaml

    === "Download"
        ```shell
        wget -O config.yaml https://raw.githubusercontent.com/tomiwebpro/stealthscale/main/config-example.yaml
        curl -o config.yaml https://raw.githubusercontent.com/tomiwebpro/stealthscale/main/config-example.yaml
        ```

## Key sections (StealthScale vs Headscale)

The full annotated file is `config-example.yaml` (593 lines). StealthScale-specific sections:

### `server_url`, `listen_addr`, `metrics_listen_addr`

```yaml
server_url: https://ctl.example.com:443  # clients dial this; use :443 for Tailscale compat
listen_addr: 127.0.0.1:8080
metrics_listen_addr: 127.0.0.1:9090  # null to disable; debug at /debug/
```

`base_domain` (MagicDNS) must differ from `server_url` domain (validated in `isSafeServerURL`). For full stealth, wrap `listen_addr` with Reality dest or put `server_url` behind a Reality-enabled reverse proxy — see [Threat model](./threat-model.md). Behind a reverse proxy, set `trusted_proxies` and `tls_cert_path: ""`.

### `xray` — VLESS+Reality_XTLS (default stealth transport)

```yaml
xray:
  enabled: true
  listen_addr: 0.0.0.0
  listen_port: 10001
  max_listen_port: 10100  # each node gets one deterministic port in this range
  security: reality_xtls  # none | tls | xtls | reality_xtls (alias: reality)
  cert_file: ""  # only for tls/xtls
  key_file: ""
  timeout: 30s
  utls_fingerprint: chrome  # chrome, firefox, safari, randomized, ios → hscontrol/xray/client.go:247
  secret: ""  # HMAC key for NodeUUID/Port + Reality keypair; auto to .xray_secret for sqlite, MUST set for postgres
  reality:
    dest: "www.cloudflare.com:443"  # decoy; www.microsoft.com works but 8273-byte cert > 8192 pre-read limit
    server_names: [www.cloudflare.com, www.microsoft.com, cloudflare.com, microsoft.com]
    private_key: ""  # hex 32 bytes, auto from secret
    public_key: ""   # hex, derived
    short_id: ""     # hex 0-8 bytes (compat)
    short_ids: []    # may contain "" for empty shortId
    spider_x: "/"
  stealth:
    enforce: true          # gate DERP on stealth (fail-closed)
    enforce_control: true  # hide /ts2021 Noise; only VLESS stealth (fingerprintable if false)
    probe_interval: 30s
    probe_timeout: 5s
```

Validation in `hscontrol/types/config.go:928`: `listen_port`/`max_listen_port` sane, `security` in `none,tls,xtls,reality_xtls`, `tls`/`xtls` require `cert_file`+`key_file`, `reality_xtls` does not, `postgres` requires `xray.secret`. See [XRay/VLESS reference](./xray-vless.md).

### `derp` — relay, gated by stealth

```yaml
derp:
  server: { enabled: false, region_id: 999, region_code: stealthscale, stun_listen_addr: "0.0.0.0:3478", verify_clients: true }
  urls: []  # no third-party DERP by default; embedded DERP is stealth-gated when enforce:true
  paths: []
  auto_update_enabled: true
  update_frequency: 3h
```

When `xray.stealth.enforce:true` and `Checker.IsSatisfied()==false`, `FilterDERPMap()` returns empty (fail-closed) via `hscontrol/app.go`. See [DERP](./derp.md) and [XRay/VLESS](./xray-vless.md).

### Other sections

- `prefixes` (`v4: 100.64.0.0/10`, `v6: fd7a:115c:a1e0::/48`, `allocation: sequential|random`), `noise.private_key_path`, `dns` (MagicDNS), `policy` (`mode: file|database`, `path`), `log`, `database` (`sqlite` recommended), `oidc`, `tls_letsencrypt_*`, `unix_socket`.

For per-node deterministic UUID/port see [XRay/VLESS](./xray-vless.md) and `hscontrol/xray/vless.go:152`. For ops, see [StealthScale Install](../stealthscale/install.md) and `hscontrol/types/config.go:650` `LoadConfig` defaults.
