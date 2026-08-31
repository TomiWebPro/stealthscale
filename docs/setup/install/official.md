# Official releases (StealthScale)

!!! warning "StealthScale defaults differ from Headscale — read the stealth notes"

    Headscale's install guides assume WireGuard. StealthScale defaults to **VLESS+Reality_XTLS** (`xray.enabled:true`, `xray.security:reality_xtls`, `xray.stealth.enforce:true`, `xray.stealth.enforce_control:true`) with per-node listeners on `10001`–`10100` and `xray.secret` auto-persisted to `.xray_secret`. The authoritative guide is [StealthScale Install & deploy](../../stealthscale/install.md) (VLESS+Reality_XTLS, `xray.stealth.enforce_control`, `.xray_secret` backup). This page is a thin wrapper for package users — keep `xray.*` at their defaults unless you need stock-client compat.

Official releases for `stscale` are available as binaries and DEB packages on [GitHub releases](https://github.com/tomiwebpro/stealthscale/releases) (built as `stscale` via `.goreleaser.yml`, `CGO_ENABLED=0`, `binary: stscale`).

## Using packages for Debian/Ubuntu (recommended)

DEB packages configure a local user, default config, and systemd service. Supported: Ubuntu 22.04+ / Debian 12+.

1. Download the latest package:

    ```shell
    STSCALE_VERSION="" # e.g. "0.29.3" without v prefix
    STSCALE_ARCH="" # amd64/arm64/arm
    wget --output-document=stscale.deb \
     "https://github.com/tomiwebpro/stealthscale/releases/download/v${STSCALE_VERSION}/stealthscale_${STSCALE_VERSION}_linux_${STSCALE_ARCH}.deb"
    ```

1. Install:

    ```shell
    sudo apt install ./stscale.deb
    ```

1. Configure by editing `/etc/stealthscale/config.yaml` (example also at `/usr/share/doc/stealthscale/examples/config-example.yaml`). **Keep stealth defaults**:

    ```yaml
    xray:
      enabled: true
      security: reality_xtls
      secret: "" # auto to .xray_secret for sqlite; MUST set for postgres (openssl rand -hex 32)
      utls_fingerprint: chrome
      reality: { dest: "www.cloudflare.com:443", server_names: [www.cloudflare.com, www.microsoft.com, cloudflare.com, microsoft.com], spider_x: "/" }
      stealth: { enforce: true, enforce_control: true }
    ```

    Also set `server_url`, `base_domain`, `prefixes`, and expose `10001`–`10100` (or your `xray.listen_port` … `max_listen_port`) plus `443` (and `3478/udp` if `derp.server.enabled:true`).

1. Restart and verify:

    ```shell
    sudo systemctl restart stealthscale  # or stscale per package name — check systemctl list-units
    sudo systemctl status stealthscale
    stscale nodes vless 1  # stable URI (pbk/sid/dest) when xray.secret stable
    curl -s http://127.0.0.1:8080/health | jq
    curl -s http://127.0.0.1:8080/web | head  # embedded WebUI at /web and /admin
    ```

Continue at [StealthScale clients](../../stealthscale/clients.md) (`stscale up --coordinator --vless-uri`). For container/source/community see the sibling pages, but all point back to [StealthScale Install & deploy](../../stealthscale/install.md) for VLESS identity and WebUI hardening.
